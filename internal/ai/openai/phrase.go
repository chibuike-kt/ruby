package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chibuike-kt/ruby/internal/ai"
)

// Phraser implements ai.Phraser — the second, narrowly-scoped call (plan
// decision #7). It receives only input (an ai.PhraseInput, marshaled to
// JSON as the user message) and returns plain phrasing text, no schema:
// there is nothing structural left to enforce once the outcome is
// already decided.
type Phraser struct {
	apiKey string
}

func NewPhraser(apiKey string) *Phraser {
	return &Phraser{apiKey: apiKey}
}

func (p *Phraser) Phrase(ctx context.Context, input ai.PhraseInput) (string, error) {
	userContent, err := json.Marshal(input)
	if err != nil {
		return "", err
	}

	reqBody := responsesRequest{
		Model: phrasingModel,
		Input: []responsesMessage{
			{Role: "system", Content: phrasingSystemPrompt(input.Language)},
			{Role: "user", Content: string(userContent)},
		},
	}

	text, err := postResponses(ctx, p.apiKey, reqBody)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func phrasingSystemPrompt(lang ai.Language) string {
	return fmt.Sprintf(`You are Ruby, a WhatsApp assistant for informal Nigerian traders. You will be given a JSON object describing an outcome that has already happened and been confirmed by the backend — you only phrase it naturally for the trader, you never decide anything, never add facts not present in the JSON, and never second-guess the outcome. Amounts are in kobo (divide by 100 for naira). Reply in language code %q (en=English, pcm=Nigerian Pidgin, yo=Yoruba, ig=Igbo, ha=Hausa), as a short, warm, natural WhatsApp message a real trader would send. Reply with only the message text — no preamble, no quotes, no explanation of what you're doing.`, lang)
}
