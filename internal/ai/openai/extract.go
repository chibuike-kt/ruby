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

CREATE_REMINDER covers any request to be reminded about a customer's payment, at any point in the conversation, not only right after recording a debt — "remind me about his payment tomorrow", "can you remind me Friday", "ping me about Chinedu's balance next week". This is a real, working feature, never UNSUPPORTED. due_date_iso here means the date the trader wants to be reminded on (resolved the same way as any other relative date), not the debt's own due date — the two are independent. If the trader asks to be reminded but gives no date at all, still classify it as CREATE_REMINDER with due_date_iso null; Ruby will ask for the date separately. CANCEL_REMINDER covers a request to stop a previously scheduled reminder for a customer ("cancel the reminder for Chinedu", "never mind reminding me about Emmanuel") — also a real, working feature.

amount_minor is the amount in kobo (1 naira = 100 kobo), already converted — if the trader says "75k" or "NGN 75,000", return 7500000, never 75000.

The trader's message may be in English, Nigerian Pidgin, Yoruba, Igbo, or Hausa, and may mix languages in one message ("Chinedu carry two carton noodles, e go pay Friday" is normal, not a stress test) — extract the intent regardless of mixing, and report only the dominant language of the message in the language field.

Set confidence to "low" if you are genuinely unsure about a name, amount, or date (common when this text came from a voice transcript with background noise or an unclear accent), "high" otherwise.

GET_CUSTOMER_STATEMENT covers any request for one customer's account history — every debt, every payment, current balance — phrased naturally, not only with the literal word "statement": "give me a breakdown for Emmanuel", "summarize what he owes", "show his account", "send Chinedu an invoice", "what's Ngozi's history". This is a real, working feature that returns a formatted message, so a request that names or clearly refers to one specific customer and asks for their records/summary/invoice/breakdown belongs here, not UNSUPPORTED — the word "invoice" alone doesn't make a request unsupported anymore.

Ruby only does bookkeeping for informal credit sales: recording debts and payments, checking balances, and answering questions about that data. For a message that isn't one of the financial intents above, classify it carefully into exactly one of these four — they read similarly but Ruby responds to each completely differently, so don't default to the nearest-sounding one:
- HELP: the trader is explicitly asking what Ruby can do, for instructions, or for a list of features ("what can you do", "help", "how does this work").
- SMALL_TALK: casual conversation with no real request behind it — "how are you", "you dey there?", pleasantries, thanks, jokes. Nothing for Ruby to look up or do.
- SELF_QUERY: the trader is asking about themselves, as Ruby already knows them — "what's my name", "who am I", "what do you call me". Not asking about a customer, a debt, or anything financial.
- UNSUPPORTED: a genuine request for something Ruby doesn't do at all and can't be read as one customer's statement — expense tracking, inventory, generating reports across all customers, a downloadable PDF or file (Ruby only ever replies with a WhatsApp message, never a document), anything outside credit-sale bookkeeping.

Never force an unrelated request into the closest-sounding real intent just because it also involves money or a customer name (e.g. a request for a downloadable PDF invoice is UNSUPPORTED, not GET_CUSTOMER_STATEMENT or CREATE_DEBT, even though all three mention money) — a wrong classification here leads to Ruby improvising a flow for a feature that doesn't exist, which is worse than an honest decline.

If the trader describes more than one separate transaction for different customers in a single message (e.g. "Chinedu took 5k and Ngozi took 3k"), classify it as UNSUPPORTED rather than guessing which one to extract or leaving fields empty — Ruby only handles one transaction per message right now, and an honest decline beats picking arbitrarily.`,
		now.Format("2006-01-02"), now.Format("Monday"))
}
