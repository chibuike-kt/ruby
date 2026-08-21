package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/chibuike-kt/ruby/internal/ai"
)

// VisionExtractor implements ai.VisionExtractor: one Structured Outputs
// call, strict mode, the same as Extractor, except the input is an image
// and the schema wraps a list — docs/BRIEF-research-hardening-
// standard.md Part 5 Tier 1's photo input, properly resolving the
// earlier-declined multi-transaction question (extract.go's own system
// prompt still declines more than one transaction per text message —
// that constraint was never about the model's ability, it was about a
// single WhatsApp text message rarely describing more than one
// transaction; a photo of a ledger page routinely does).
type VisionExtractor struct {
	apiKey   string
	model    string
	location *time.Location
}

func NewVisionExtractor(apiKey, model, timezone string) *VisionExtractor {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	return &VisionExtractor{apiKey: apiKey, model: model, location: loc}
}

// visionRequest/visionMessage/visionPart mirror responsesRequest/
// responsesMessage from client.go, except content is a multipart array
// (text + image), not a plain string — the Responses API's multimodal
// input shape. The response shape is identical either way, so
// postResponses' own responsesResponse/responsesOutput/responsesContent
// types are reused unchanged for parsing.
type visionRequest struct {
	Model string          `json:"model"`
	Input []visionMessage `json:"input"`
	Text  *responsesText  `json:"text,omitempty"`
}

type visionMessage struct {
	Role    string       `json:"role"`
	Content []visionPart `json:"content"`
}

type visionPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type visionTransactions struct {
	Transactions []ai.RawIntent `json:"transactions"`
}

func (v *VisionExtractor) ExtractFromImage(ctx context.Context, image []byte, mimeType string) ([]ai.RawIntent, error) {
	now := time.Now().In(v.location)
	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(image))

	reqBody := visionRequest{
		Model: v.model,
		Input: []visionMessage{
			{Role: "system", Content: []visionPart{{Type: "input_text", Text: visionSystemPrompt(now)}}},
			{Role: "user", Content: []visionPart{{Type: "input_image", ImageURL: dataURL}}},
		},
		Text: &responsesText{
			Format: responsesFormat{
				Type:   "json_schema",
				Name:   "ruby_photo_transactions",
				Strict: true,
				Schema: ai.VisionIntentSchema(),
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	resp, err := doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/responses", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+v.apiKey)
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apiRequestError(resp.StatusCode, respBody)
	}

	var parsed responsesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}
	for _, out := range parsed.Output {
		for _, c := range out.Content {
			if c.Type == "output_text" && c.Text != "" {
				var txns visionTransactions
				if err := json.Unmarshal([]byte(c.Text), &txns); err != nil {
					return nil, fmt.Errorf("openai: could not parse extracted transactions: %w", err)
				}
				return txns.Transactions, nil
			}
		}
	}
	return nil, nil
}

func visionSystemPrompt(now time.Time) string {
	return fmt.Sprintf(`You are Ruby, a WhatsApp assistant for informal Nigerian traders. The trader sent a photo — a page from a paper ledger, a handwritten IOU, a receipt, or similar. Extract every distinct transaction visible in the photo into the transactions list, each in the exact same shape a single text message would produce.

Today's date is %s, a %s, in the trader's configured timezone — resolve any relative or written date the same way a text message would, returning it as due_date_iso in YYYY-MM-DD format. If a line has no date at all, return null for it — never invent one.

Unlike a single text message, a photo routinely shows more than one transaction (several rows of a ledger, several separate IOUs) — extract every one you can make out as its own entry in transactions, each with its own customer_name and amount_minor, never merged together and never limited to one.

amount_minor is the amount in kobo (1 naira = 100 kobo) — if a line reads "75k" or "N75,000", return 7500000, never 75000.

Set confidence to "low" for any individual transaction whose name, amount, or date you're genuinely unsure of — faded ink, unclear handwriting, a partly-obscured number — "high" only when you can read it clearly. Judge each transaction on its own: one line being unclear doesn't mean the rest are.

Every transaction from a photo is either CREATE_DEBT (goods given on credit) or RECORD_PAYMENT (money received against a debt) — whichever the line actually shows. If a line genuinely isn't a credit-sale transaction (a note, a total, something illegible), leave it out of transactions entirely rather than guessing. If the photo shows no transactions at all, return an empty transactions list.

language is per-transaction, the language any handwritten or printed text for that line is in (default to "en" if it's just names and numbers with no words to judge from).`,
		now.Format("2006-01-02"), now.Format("Monday"))
}
