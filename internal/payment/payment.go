// Package payment records repayments against a debt (spec §13-15). All
// balance mutation happens inside Service.Record, which locks the debt
// row for the duration of the transaction (spec §33/§34) so concurrent
// payments against the same debt can never both succeed.
package payment

import "time"

type Payment struct {
	ID             int64
	DebtID         int64
	AmountMinor    int64
	Currency       string
	IdempotencyKey string
	CreatedAt      time.Time
}
