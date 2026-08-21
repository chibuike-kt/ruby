package reminder

import (
	"context"
	"strconv"
	"time"

	"github.com/chibuike-kt/ruby/internal/db"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/ledger"
	"github.com/chibuike-kt/ruby/internal/money"
)

// digestInterval is a week — docs/BRIEF-research-hardening-standard.md
// Part 5 Tier 1's own framing ("a weekly summary"). dueSoonWindow
// matches it: "anything due soon" means due within the same week this
// digest covers.
const digestInterval = 7 * 24 * time.Hour
const dueSoonWindow = 7 * 24 * time.Hour

// digestBatchSize bounds one DispatchDigests call the same way
// dispatchBatchSize bounds Dispatch — a large trader base is processed
// in bounded chunks, not one huge query.
const digestBatchSize = 100

type digestRecipient struct {
	UserID      int64
	Name        string
	PhoneNumber string
}

// usersDueForDigest finds every onboarded trader (a name means past
// name-capture, see internal/ai's own acct.Name == "" check) whose last
// digest, if any, was sent at least a week ago.
func usersDueForDigest(ctx context.Context, q db.Querier, now time.Time, limit int) ([]digestRecipient, error) {
	rows, err := q.Query(ctx, `
		SELECT id, name, phone_number FROM users
		WHERE name != '' AND (last_digest_sent_at IS NULL OR last_digest_sent_at <= $1)
		ORDER BY id
		LIMIT $2
	`, now.Add(-digestInterval), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []digestRecipient
	for rows.Next() {
		var r digestRecipient
		if err := rows.Scan(&r.UserID, &r.Name, &r.PhoneNumber); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// markDigestSent records now as userID's last digest send — the only
// state a weekly checkpoint needs, unlike a scheduled reminder's full
// SCHEDULED/PROCESSING/SENT/FAILED history.
func markDigestSent(ctx context.Context, q db.Querier, userID int64, now time.Time) error {
	_, err := q.Exec(ctx, `UPDATE users SET last_digest_sent_at = $2 WHERE id = $1`, userID, now)
	return err
}

// digestStats is the four things docs/BRIEF-research-hardening-
// standard.md Part 5 Tier 1 names: "transactions recorded, amount
// collected, amount outstanding, anything due soon."
type digestStats struct {
	TransactionsRecorded int
	CollectedMinor       int64
	OutstandingMinor     int64
	DueSoonCount         int
}

// digestStatsFor computes userID's week: transactions recorded and
// amount collected are windowed to the last digestInterval (this
// week's activity), amount outstanding is the all-time running balance
// (spec §17's own ledger invariant), and due-soon counts every
// still-outstanding debt due within the same window ahead of now.
func digestStatsFor(ctx context.Context, q db.Querier, userID int64, now time.Time) (digestStats, error) {
	since := now.Add(-digestInterval)

	// PAYMENT_RECORDED entries are stored negative (ledger.SummaryByUser's
	// own sign convention, spec §17) — negated here the same way, so
	// "amount collected" reads as a positive figure.
	var stats digestStats
	err := q.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE type = $2 AND created_at >= $3),
			COALESCE(-sum(amount_minor) FILTER (WHERE type = $4 AND created_at >= $3), 0)
		FROM ledger_entries WHERE user_id = $1
	`, userID, string(ledger.EntryDebtCreated), since, string(ledger.EntryPaymentRecorded),
	).Scan(&stats.TransactionsRecorded, &stats.CollectedMinor)
	if err != nil {
		return digestStats{}, err
	}

	summaries, err := ledger.SummaryByUser(ctx, q, userID)
	if err != nil {
		return digestStats{}, err
	}
	if len(summaries) > 0 {
		stats.OutstandingMinor = summaries[0].TotalOutstandingMinor
	}

	outstanding, err := debt.ListOutstandingByUser(ctx, q, userID)
	if err != nil {
		return digestStats{}, err
	}
	dueBy := now.Add(dueSoonWindow)
	for _, d := range outstanding {
		if d.DueDate != nil && !d.DueDate.After(dueBy) {
			stats.DueSoonCount++
		}
	}

	return stats, nil
}

// DispatchDigests sends the weekly summary to every trader due for one
// (docs/BRIEF-research-hardening-standard.md Part 5 Tier 1), reusing
// this Service's own TemplateSender — a digest is business-initiated
// and unprompted exactly like a reminder, so it needs the same
// Meta-approved template mechanism, not freeform text. A trader with
// nothing to report this week (no transactions, nothing outstanding)
// still gets one: an all-zero week is itself useful information
// ("nothing happened, and Ruby is still watching"), never suppressed as
// noise.
func (s *Service) DispatchDigests(ctx context.Context, now time.Time) (sent, failed int, err error) {
	recipients, err := usersDueForDigest(ctx, s.pool, now, digestBatchSize)
	if err != nil {
		return 0, 0, err
	}

	for _, r := range recipients {
		if r.PhoneNumber == "" {
			failed++
			continue
		}
		stats, statErr := digestStatsFor(ctx, s.pool, r.UserID, now)
		if statErr != nil {
			return sent, failed, statErr
		}

		params := []string{
			r.Name,
			strconv.Itoa(stats.TransactionsRecorded),
			money.FormatNaira(stats.CollectedMinor),
			money.FormatNaira(stats.OutstandingMinor),
			strconv.Itoa(stats.DueSoonCount),
		}
		if _, sendErr := s.sender.SendTemplate(ctx, r.PhoneNumber, s.digestTemplateName, reminderLanguageCode, params); sendErr != nil {
			failed++
			continue
		}

		if err := markDigestSent(ctx, s.pool, r.UserID, now); err != nil {
			return sent, failed, err
		}
		sent++
	}

	return sent, failed, nil
}
