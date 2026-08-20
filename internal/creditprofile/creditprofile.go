// Package creditprofile computes the aggregate, non-identifying
// creditworthiness summary docs/wema-integration.md's Credit Profile
// API returns to a consenting partner. It never returns customer names,
// phone numbers, or individual transaction detail — only rollups
// (spec §36's data-minimization principle, applied to a new external-
// partner context rather than internal logging).
package creditprofile

import (
	"context"
	"time"

	"github.com/chibuike-kt/ruby/internal/account"
	"github.com/chibuike-kt/ruby/internal/db"
	"github.com/chibuike-kt/ruby/internal/ledger"
)

// Profile is exactly docs/wema-integration.md's response shape.
type Profile struct {
	TraderName             string  `json:"trader_name"`
	AccountAgeDays         int     `json:"account_age_days"`
	TotalCreditIssuedMinor int64   `json:"total_credit_issued_minor"`
	TotalCollectedMinor    int64   `json:"total_collected_minor"`
	OnTimePaymentRate      float64 `json:"on_time_payment_rate"`
	ActiveCustomerCount    int     `json:"active_customer_count"`
	OutstandingMinor       int64   `json:"outstanding_minor"`
}

// Get computes userID's credit profile. Callers are responsible for
// checking users.credit_profile_sharing_enabled before calling this —
// Get itself doesn't gate on consent, since some callers (a trader
// previewing their own profile before opting in, an internal audit) may
// legitimately need it regardless of the partner-facing flag.
func Get(ctx context.Context, q db.Querier, userID int64) (Profile, error) {
	acct, err := account.GetByID(ctx, q, userID)
	if err != nil {
		return Profile{}, err
	}

	summaries, err := ledger.SummaryByUser(ctx, q, userID)
	if err != nil {
		return Profile{}, err
	}
	var issued, collected, outstanding int64
	if len(summaries) > 0 {
		issued = summaries[0].TotalCreditIssuedMinor
		collected = summaries[0].TotalCollectedMinor
		outstanding = summaries[0].TotalOutstandingMinor
	}

	activeCustomers, err := activeCustomerCount(ctx, q, userID)
	if err != nil {
		return Profile{}, err
	}

	onTimeRate, err := onTimePaymentRate(ctx, q, userID)
	if err != nil {
		return Profile{}, err
	}

	traderName := acct.Name
	if acct.BusinessName != nil && *acct.BusinessName != "" {
		traderName = *acct.BusinessName
	}

	return Profile{
		TraderName:             traderName,
		AccountAgeDays:         int(time.Since(acct.CreatedAt).Hours() / 24),
		TotalCreditIssuedMinor: issued,
		TotalCollectedMinor:    collected,
		OnTimePaymentRate:      onTimeRate,
		ActiveCustomerCount:    activeCustomers,
		OutstandingMinor:       outstanding,
	}, nil
}

// activeCustomerCount is the number of distinct customers the trader
// currently extends credit to — anyone with a not-yet-SETTLED debt.
func activeCustomerCount(ctx context.Context, q db.Querier, userID int64) (int, error) {
	var count int
	err := q.QueryRow(ctx, `
		SELECT COUNT(DISTINCT customer_id) FROM debts
		WHERE user_id = $1 AND status != 'SETTLED'
	`, userID).Scan(&count)
	return count, err
}

// onTimePaymentRate is the share of payments made on or before their
// debt's due date, among payments against a debt that actually has one
// — a debt with no due date has no "on time" concept to score against.
// A trader with no such payments yet gets a rate of 1.0: there is
// nothing to be late on, and a fresh trader shouldn't score worse than
// an established one just for lacking history.
func onTimePaymentRate(ctx context.Context, q db.Querier, userID int64) (float64, error) {
	var onTime, total int
	err := q.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE p.created_at::date <= d.due_date),
			COUNT(*)
		FROM payments p
		JOIN debts d ON d.id = p.debt_id
		WHERE d.user_id = $1 AND d.due_date IS NOT NULL
	`, userID).Scan(&onTime, &total)
	if err != nil {
		return 0, err
	}
	if total == 0 {
		return 1.0, nil
	}
	return float64(onTime) / float64(total), nil
}
