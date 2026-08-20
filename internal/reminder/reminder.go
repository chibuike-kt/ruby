// Package reminder implements the debt-creation reminder opt-in
// (docs/BRIEF-fixes-and-reminders.md #4) — a narrower slice than the
// full spec §18/19 reminder system: scheduling two reminders (the day
// before a debt's due date, and on the due date itself) to the
// customer, once the trader opts in right after creating the debt.
//
// WhatsApp's Business Platform requires a Meta-approved message
// template for any business-initiated conversation outside an active
// 24-hour customer service window — the customer has never messaged
// Ruby, so every reminder is business-initiated from message one. This
// package is built against that templated-message shape from the
// start (see TemplateSender), never freeform text, even though the
// actual template isn't approved yet in every environment. If Dispatch
// starts failing because of that, it's a real, expected failure mode —
// see Service.Dispatch's doc comment.
package reminder

import "time"

type RecipientType string

const (
	RecipientTrader   RecipientType = "TRADER"
	RecipientCustomer RecipientType = "CUSTOMER"
)

type Status string

const (
	StatusScheduled  Status = "SCHEDULED"
	StatusProcessing Status = "PROCESSING"
	StatusSent       Status = "SENT"
	StatusFailed     Status = "FAILED"
	StatusCancelled  Status = "CANCELLED"
)

// Reminder mirrors the reminders table, which predates this slice (the
// original schema already anticipated spec §18/19's full reminder
// system).
type Reminder struct {
	ID                int64
	DebtID            int64
	RecipientType     RecipientType
	RecipientID       *int64
	ScheduledAt       time.Time
	Status            Status
	Attempts          int
	Template          string
	ProviderMessageID *string
	SentAt            *time.Time
	FailureReason     *string
	CreatedAt         time.Time
}
