package ledger

import (
	"context"
	"sort"

	"github.com/chibuike-kt/ruby/internal/db"
)

func Insert(ctx context.Context, q db.Querier, e Entry) (Entry, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO ledger_entries (user_id, debt_id, type, amount_minor, currency, reference)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, debt_id, type, amount_minor, currency, reference, created_at
	`, e.UserID, e.DebtID, string(e.Type), e.AmountMinor, e.Currency, e.Reference)
	return scan(row)
}

func ListByDebt(ctx context.Context, q db.Querier, debtID int64) ([]Entry, error) {
	return list(ctx, q, `
		SELECT id, user_id, debt_id, type, amount_minor, currency, reference, created_at
		FROM ledger_entries WHERE debt_id = $1
		ORDER BY id
	`, debtID)
}

func ListByUser(ctx context.Context, q db.Querier, userID int64) ([]Entry, error) {
	return list(ctx, q, `
		SELECT id, user_id, debt_id, type, amount_minor, currency, reference, created_at
		FROM ledger_entries WHERE user_id = $1
		ORDER BY id
	`, userID)
}

// Summary is a per-currency rollup of a user's ledger (spec §17):
// outstanding is always issued minus collected, never stored or computed
// independently, so the three numbers can never drift apart.
type Summary struct {
	Currency               string
	TotalCreditIssuedMinor int64
	TotalCollectedMinor    int64
	TotalOutstandingMinor  int64
}

// SummaryByUser groups every ledger entry for userID by currency. Every
// debt creation and payment is already a signed ledger entry, so
// outstanding = issued - collected holds without a per-debt loop.
func SummaryByUser(ctx context.Context, q db.Querier, userID int64) ([]Summary, error) {
	entries, err := ListByUser(ctx, q, userID)
	if err != nil {
		return nil, err
	}

	byCurrency := map[string]*Summary{}
	var order []string
	for _, e := range entries {
		t := byCurrency[e.Currency]
		if t == nil {
			t = &Summary{Currency: e.Currency}
			byCurrency[e.Currency] = t
			order = append(order, e.Currency)
		}
		switch e.Type {
		case EntryDebtCreated:
			t.TotalCreditIssuedMinor += e.AmountMinor
		case EntryPaymentRecorded:
			t.TotalCollectedMinor += -e.AmountMinor
		}
	}

	sort.Strings(order)
	out := make([]Summary, 0, len(order))
	for _, currency := range order {
		t := byCurrency[currency]
		t.TotalOutstandingMinor = t.TotalCreditIssuedMinor - t.TotalCollectedMinor
		out = append(out, *t)
	}
	return out, nil
}

func list(ctx context.Context, q db.Querier, sql string, args ...any) ([]Entry, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type row interface {
	Scan(dest ...any) error
}

func scan(r row) (Entry, error) {
	var e Entry
	var entryType string
	err := r.Scan(&e.ID, &e.UserID, &e.DebtID, &entryType, &e.AmountMinor, &e.Currency, &e.Reference, &e.CreatedAt)
	e.Type = EntryType(entryType)
	return e, err
}
