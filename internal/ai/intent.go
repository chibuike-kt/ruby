package ai

// RawIntent is the untrusted, unvalidated struct straight off the model
// (spec §9's example). It is never passed to a service call — see
// Validator.Validate. Every field is a plain Go value, never a database
// id: there is no CustomerID field here, only CustomerName, so the model
// has no way to name another trader's row even if it tried.
type RawIntent struct {
	Intent       IntentType `json:"intent"`
	CustomerName *string    `json:"customer_name"`
	AmountMinor  *int64     `json:"amount_minor"`
	Description  *string    `json:"description"`
	DueDateISO   *string    `json:"due_date_iso"`
	Confidence   Confidence `json:"confidence"`
	Language     Language   `json:"language"`
}

var allIntents = []string{
	string(IntentCreateDebt), string(IntentRecordPayment), string(IntentListCustomers),
	string(IntentGetCustomerBalance), string(IntentListOutstandingDebts), string(IntentGetTotalOutstanding),
	string(IntentGetPaymentSummary), string(IntentCreateReminder), string(IntentCancelReminder),
	string(IntentConfirmAction), string(IntentHelp),
}

var allLanguages = []string{
	string(LangEnglish), string(LangPidgin), string(LangYoruba), string(LangIgbo), string(LangHausa),
}

var allConfidences = []string{string(ConfidenceHigh), string(ConfidenceLow)}

// IntentSchema is the JSON Schema for RawIntent in OpenAI Structured
// Outputs' strict-mode shape: every property listed in required,
// additionalProperties false, nullable fields expressed as a ["type",
// "null"] union — verified against current docs during planning, not
// assumed from older JSON-mode conventions.
func IntentSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"intent": map[string]any{
				"type": "string",
				"enum": allIntents,
			},
			"customer_name": map[string]any{
				"type": []string{"string", "null"},
			},
			"amount_minor": map[string]any{
				"type": []string{"integer", "null"},
			},
			"description": map[string]any{
				"type": []string{"string", "null"},
			},
			"due_date_iso": map[string]any{
				"type":        []string{"string", "null"},
				"description": "ISO 8601 date (YYYY-MM-DD), resolved against the trader's timezone. Null if the trader gave no due date.",
			},
			"confidence": map[string]any{
				"type": "string",
				"enum": allConfidences,
			},
			"language": map[string]any{
				"type": "string",
				"enum": allLanguages,
			},
		},
		"required":             []string{"intent", "customer_name", "amount_minor", "description", "due_date_iso", "confidence", "language"},
		"additionalProperties": false,
	}
}
