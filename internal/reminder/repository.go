package reminder

import (
	"context"
	"time"

	"github.com/chibuike-kt/ruby/internal/db"
)

// Schedule creates the two reminders a debt-creation opt-in requires
// (docs/BRIEF-fixes-and-reminders.md #4): one the day before dueDate,
// one on dueDate itself, both to the customer — a trader wanting a
// reminder about their own book is a different, out-of-scope feature,
// so RecipientType is always CUSTOMER here. template is stored per row
// so Dispatch knows which Meta-approved template to send without
// re-deriving it later.
func Schedule(ctx context.Context, q db.Querier, debtID, customerID int64, dueDate time.Time, template string) ([]Reminder, error) {
	times := []time.Time{dueDate.AddDate(0, 0, -1), dueDate}
	out := make([]Reminder, 0, len(times))
	for _, at := range times {
		r, err := insert(ctx, q, debtID, customerID, at, template)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func insert(ctx context.Context, q db.Querier, debtID, customerID int64, scheduledAt time.Time, template string) (Reminder, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO reminders (debt_id, recipient_type, recipient_id, scheduled_at, status, template)
		VALUES ($1, 'CUSTOMER', $2, $3, 'SCHEDULED', $4)
		RETURNING id, debt_id, recipient_type, recipient_id, scheduled_at, status, attempts, template, provider_message_id, sent_at, failure_reason, created_at
	`, debtID, customerID, scheduledAt, template)
	return scan(row)
}

// DispatchCandidate is one due reminder plus the customer/debt context
// a template message needs — joined in one query rather than a
// service call per row, since Dispatch may process many at once.
type DispatchCandidate struct {
	Reminder      Reminder
	CustomerName  string
	CustomerPhone string
	AmountMinor   int64
	DueDate       time.Time
}

// DueForDispatch returns up to limit CUSTOMER reminders scheduled at or
// before now that haven't been sent yet — Dispatch's own query.
func DueForDispatch(ctx context.Context, q db.Querier, now time.Time, limit int) ([]DispatchCandidate, error) {
	rows, err := q.Query(ctx, `
		SELECT r.id, r.debt_id, r.recipient_type, r.recipient_id, r.scheduled_at, r.status, r.attempts,
		       r.template, r.provider_message_id, r.sent_at, r.failure_reason, r.created_at,
		       c.name, c.phone_number, d.amount_minor, d.due_date
		FROM reminders r
		JOIN debts d ON d.id = r.debt_id
		JOIN customers c ON c.id = r.recipient_id
		WHERE r.status = 'SCHEDULED' AND r.recipient_type = 'CUSTOMER' AND r.scheduled_at <= $1
		ORDER BY r.scheduled_at
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DispatchCandidate
	for rows.Next() {
		var c DispatchCandidate
		var phone *string
		var dueDate *time.Time
		r, err := scanInto(rows, &c.CustomerName, &phone, &c.AmountMinor, &dueDate)
		if err != nil {
			return nil, err
		}
		c.Reminder = r
		if phone != nil {
			c.CustomerPhone = *phone
		}
		if dueDate != nil {
			c.DueDate = *dueDate
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkSent records a successful send (Dispatch's own bookkeeping) — the
// provider's own message id, for the same audit-trail reasoning
// internal/whatsapp already applies to every other outbound send.
func MarkSent(ctx context.Context, q db.Querier, id int64, providerMessageID string, sentAt time.Time) error {
	_, err := q.Exec(ctx, `
		UPDATE reminders SET status = 'SENT', provider_message_id = $1, sent_at = $2, attempts = attempts + 1
		WHERE id = $3
	`, providerMessageID, sentAt, id)
	return err
}

// MarkFailed records a failed send attempt. reason is stored verbatim —
// this is an internal operational record, never shown to a trader or
// customer (spec §36/§37's leak-nothing-internal rule governs
// user-facing text, not this table).
func MarkFailed(ctx context.Context, q db.Querier, id int64, reason string) error {
	_, err := q.Exec(ctx, `
		UPDATE reminders SET status = 'FAILED', failure_reason = $1, attempts = attempts + 1
		WHERE id = $2
	`, reason, id)
	return err
}

type row interface {
	Scan(dest ...any) error
}

func scan(r row) (Reminder, error) {
	var rem Reminder
	var recipientType, status string
	err := r.Scan(&rem.ID, &rem.DebtID, &recipientType, &rem.RecipientID, &rem.ScheduledAt, &status,
		&rem.Attempts, &rem.Template, &rem.ProviderMessageID, &rem.SentAt, &rem.FailureReason, &rem.CreatedAt)
	if err != nil {
		return Reminder{}, err
	}
	rem.RecipientType = RecipientType(recipientType)
	rem.Status = Status(status)
	return rem, nil
}

func scanInto(r row, name *string, phone **string, amountMinor *int64, dueDate **time.Time) (Reminder, error) {
	var rem Reminder
	var recipientType, status string
	err := r.Scan(&rem.ID, &rem.DebtID, &recipientType, &rem.RecipientID, &rem.ScheduledAt, &status,
		&rem.Attempts, &rem.Template, &rem.ProviderMessageID, &rem.SentAt, &rem.FailureReason, &rem.CreatedAt,
		name, phone, amountMinor, dueDate)
	if err != nil {
		return Reminder{}, err
	}
	rem.RecipientType = RecipientType(recipientType)
	rem.Status = Status(status)
	return rem, nil
}
