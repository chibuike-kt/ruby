package httpserver

import (
	"time"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/ledger"
	"github.com/chibuike-kt/ruby/internal/payment"
)

// Response DTOs live here, separate from Slice 1's domain structs, so
// none of those structs need JSON tags or exported-field shape changes
// — this package wraps them, it doesn't modify them.

type customerResponse struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	PhoneNumber *string   `json:"phone_number,omitempty"`
	Alias       *string   `json:"alias,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toCustomerResponse(c customer.Customer) customerResponse {
	return customerResponse{
		ID:          c.ID,
		Name:        c.Name,
		PhoneNumber: c.PhoneNumber,
		Alias:       c.Alias,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

func toCustomerResponses(customers []customer.Customer) []customerResponse {
	out := make([]customerResponse, len(customers))
	for i, c := range customers {
		out[i] = toCustomerResponse(c)
	}
	return out
}

type debtResponse struct {
	ID          int64      `json:"id"`
	CustomerID  int64      `json:"customer_id"`
	AmountMinor int64      `json:"amount_minor"`
	Currency    string     `json:"currency"`
	Description string     `json:"description,omitempty"`
	DueDate     *time.Time `json:"due_date,omitempty"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toDebtResponse(d debt.Debt) debtResponse {
	return debtResponse{
		ID:          d.ID,
		CustomerID:  d.CustomerID,
		AmountMinor: d.Amount.MinorUnits(),
		Currency:    string(d.Amount.Currency()),
		Description: d.Description,
		DueDate:     d.DueDate,
		Status:      string(d.Status),
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}

func toDebtResponses(debts []debt.Debt) []debtResponse {
	out := make([]debtResponse, len(debts))
	for i, d := range debts {
		out[i] = toDebtResponse(d)
	}
	return out
}

type paymentResponse struct {
	ID             int64     `json:"id"`
	DebtID         int64     `json:"debt_id"`
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	IdempotencyKey string    `json:"idempotency_key"`
	DebtStatus     string    `json:"debt_status"`
	CreatedAt      time.Time `json:"created_at"`
}

func toPaymentResponse(p payment.Payment, d debt.Debt) paymentResponse {
	return paymentResponse{
		ID:             p.ID,
		DebtID:         p.DebtID,
		AmountMinor:    p.AmountMinor,
		Currency:       p.Currency,
		IdempotencyKey: p.IdempotencyKey,
		DebtStatus:     string(d.Status),
		CreatedAt:      p.CreatedAt,
	}
}

type ledgerEntryResponse struct {
	ID          int64     `json:"id"`
	DebtID      *int64    `json:"debt_id,omitempty"`
	Type        string    `json:"type"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	Reference   *string   `json:"reference,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func toLedgerEntryResponse(e ledger.Entry) ledgerEntryResponse {
	return ledgerEntryResponse{
		ID:          e.ID,
		DebtID:      e.DebtID,
		Type:        string(e.Type),
		AmountMinor: e.AmountMinor,
		Currency:    e.Currency,
		Reference:   e.Reference,
		CreatedAt:   e.CreatedAt,
	}
}

func toLedgerEntryResponses(entries []ledger.Entry) []ledgerEntryResponse {
	out := make([]ledgerEntryResponse, len(entries))
	for i, e := range entries {
		out[i] = toLedgerEntryResponse(e)
	}
	return out
}

type summaryTotals struct {
	Currency               string `json:"currency"`
	TotalCreditIssuedMinor int64  `json:"total_credit_issued_minor"`
	TotalCollectedMinor    int64  `json:"total_collected_minor"`
	TotalOutstandingMinor  int64  `json:"total_outstanding_minor"`
}

func toSummaryTotals(t ledger.Summary) summaryTotals {
	return summaryTotals{
		Currency:               t.Currency,
		TotalCreditIssuedMinor: t.TotalCreditIssuedMinor,
		TotalCollectedMinor:    t.TotalCollectedMinor,
		TotalOutstandingMinor:  t.TotalOutstandingMinor,
	}
}
