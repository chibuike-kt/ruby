package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chibuike-kt/ruby/internal/ai"
)

// Extractor implements ai.Extractor: one Structured Outputs call, strict
// mode, covering intent + entities + language detection together (spec
// §9, docs/BRIEF-ai-intent.md).
type Extractor struct {
	apiKey   string
	model    string
	location *time.Location
}

// NewExtractor's timezone parameter resolves relative due dates
// ("Friday") against a concrete "today" before the AI ever sees the
// message (spec §21) — an unrecognized zone falls back to UTC rather
// than failing to construct.
func NewExtractor(apiKey, model, timezone string) *Extractor {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	return &Extractor{apiKey: apiKey, model: model, location: loc}
}

func (e *Extractor) Extract(ctx context.Context, text string) (ai.RawIntent, error) {
	now := time.Now().In(e.location)

	reqBody := responsesRequest{
		Model: e.model,
		Input: []responsesMessage{
			{Role: "system", Content: extractionSystemPrompt(now)},
			{Role: "user", Content: text},
		},
		Text: &responsesText{
			Format: responsesFormat{
				Type:   "json_schema",
				Name:   "ruby_intent",
				Strict: true,
				Schema: ai.IntentSchema(),
			},
		},
	}

	raw, err := postResponses(ctx, e.apiKey, reqBody)
	if err != nil {
		return ai.RawIntent{}, err
	}

	var intent ai.RawIntent
	if err := json.Unmarshal([]byte(raw), &intent); err != nil {
		return ai.RawIntent{}, fmt.Errorf("openai: could not parse extracted intent: %w", err)
	}
	return intent, nil
}

func extractionSystemPrompt(now time.Time) string {
	return fmt.Sprintf(`You are Ruby, a WhatsApp assistant for informal Nigerian traders. Extract the trader's message into a structured intent.

Today's date is %s, a %s, in the trader's configured timezone — resolve any relative date ("Friday", "next week", "tomorrow") against this date, and return it as due_date_iso in YYYY-MM-DD format. If the trader gave no due date at all, return null — never invent one.

amount_minor is the amount in kobo (1 naira = 100 kobo), already converted — if the trader says "75k" or "NGN 75,000", return 7500000, never 75000.

The trader's message may be in English, Nigerian Pidgin, Yoruba, Igbo, or Hausa, and may mix languages in one message ("Chinedu carry two carton noodles, e go pay Friday" is normal, not a stress test) — extract the intent regardless of mixing, and report only the dominant language of the message in the language field.

Set confidence to "low" if you are genuinely unsure about a name, amount, or date (common when this text came from a voice transcript with background noise or an unclear accent), "high" otherwise. Only choose an intent when the message clearly matches one of the allowed values — for a message you cannot classify at all, use HELP.`,
		now.Format("2006-01-02"), now.Format("Monday"))
}
