// Package ledger records the append-only financial event history (spec
// §16, §39). Entries are never updated or deleted at the application
// layer — decisions.md #4 also restricts the app's DB role to
// INSERT/SELECT on ledger_entries, but that grant is a follow-up
// migration; this package's missing mutators are the layer that ships
// with Slice 1.
package ledger

import "time"

type EntryType string

const (
	EntryDebtCreated     EntryType = "DEBT_CREATED"
	EntryPaymentRecorded EntryType = "PAYMENT_RECORDED"
	EntryDebtSettled     EntryType = "DEBT_SETTLED"
)

// Entry mirrors the ledger_entries row. AmountMinor is signed: positive
// for debt creation, negative for a payment, matching the running-total
// reading in spec §16's example (+₦120,000 then a series of -payments).
type Entry struct {
	ID          int64
	UserID      int64
	DebtID      *int64
	Type        EntryType
	AmountMinor int64
	Currency    string
	Reference   *string
	CreatedAt   time.Time
}
