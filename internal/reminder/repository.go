package reminder

import (
	"context"
	"time"

	"github.com/chibuike-kt/ruby/internal/db"
)

// ScheduleCustomer creates the two reminders the debt-creation opt-in
// requires (docs/BRIEF-fixes-and-reminders.md #4): one the day before
// dueDate, one on dueDate itself, sent to the customer.
func ScheduleCustomer(ctx context.Context, q db.Querier, debtID, customerID int64, dueDate time.Time, template string) ([]Reminder, error) {
	return schedule(ctx, q, debtID, RecipientCustomer, customerID, dueDate, template)
}

// ScheduleTrader creates the same day-before/due-date pair sent to the
// trader instead — automatic, no opt-in
// (docs/BRIEF-critical-fixes-and-reminders.md's full reminder system:
// "this is the trader's own data reflected back to them, no separate
// consent needed").
func ScheduleTrader(ctx context.Context, q db.Querier, debtID, userID int64, dueDate time.Time, template string) ([]Reminder, error) {
	return schedule(ctx, q, debtID, RecipientTrader, userID, dueDate, template)
}

func schedule(ctx context.Context, q db.Querier, debtID int64, recipientType RecipientType, recipientID int64, dueDate time.Time, template string) ([]Reminder, error) {
	times := []time.Time{dueDate.AddDate(0, 0, -1), dueDate}
	out := make([]Reminder, 0, len(times))
	for _, at := range times {
		r, err := insert(ctx, q, debtID, recipientType, recipientID, at, template)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func insert(ctx context.Context, q db.Querier, debtID int64, recipientType RecipientType, recipientID int64, scheduledAt time.Time, template string) (Reminder, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO reminders (debt_id, recipient_type, recipient_id, scheduled_at, status, template)
		VALUES ($1, $2, $3, $4, 'SCHEDULED', $5)
		RETURNING id, debt_id, recipient_type, recipient_id, scheduled_at, status, attempts, template, provider_message_id, sent_at, failure_reason, created_at
	`, debtID, string(recipientType), recipientID, scheduledAt, template)
	return scan(row)
}

// DispatchCandidate is one due reminder plus everything a template
// message body might need — joined in one query rather than a service
// call per row, since Dispatch may process many at once. CustomerName/
// TraderName are always both populated regardless of RecipientType: a
// debt always has both, and either template (see spec §18's two
// examples) may need to name the customer even when the trader is the
// one being messaged, or name the trader ("with Musa Trading") even
// when the customer is the one being messaged.
type DispatchCandidate struct {
	Reminder       Reminder
	RecipientPhone string
	CustomerName   string
	TraderName     string
	AmountMinor    int64
	DueDate        time.Time
	DebtSettled    bool
}

// DueForDispatch returns up to limit reminders (either recipient type)
// scheduled at or before now that haven't been sent yet.
func DueForDispatch(ctx context.Context, q db.Querier, now time.Time, limit int) ([]DispatchCandidate, error) {
	rows, err := q.Query(ctx, `
		SELECT r.id, r.debt_id, r.recipient_type, r.recipient_id, r.scheduled_at, r.status, r.attempts,
		       r.template, r.provider_message_id, r.sent_at, r.failure_reason, r.created_at,
		       c.name, c.phone_number, COALESCE(u.business_name, u.name), u.phone_number,
		       d.amount_minor, d.due_date, d.status
		FROM reminders r
		JOIN debts d ON d.id = r.debt_id
		JOIN customers c ON c.id = d.customer_id
		JOIN users u ON u.id = d.user_id
		WHERE r.status = 'SCHEDULED' AND r.scheduled_at <= $1
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
		var customerPhone, traderPhone *string
		var dueDate *time.Time
		var debtStatus string
		r, err := scanInto(rows, &c.CustomerName, &customerPhone, &c.TraderName, &traderPhone, &c.AmountMinor, &dueDate, &debtStatus)
		if err != nil {
			return nil, err
		}
		c.Reminder = r
		c.DebtSettled = debtStatus == "SETTLED"
		if dueDate != nil {
			c.DueDate = *dueDate
		}
		switch r.RecipientType {
		case RecipientTrader:
			if traderPhone != nil {
				c.RecipientPhone = *traderPhone
			}
		default: // RecipientCustomer
			if customerPhone != nil {
				c.RecipientPhone = *customerPhone
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkProcessing claims a reminder before attempting to send it — the
// SCHEDULED -> PROCESSING half of spec §20's state machine
// (docs/BRIEF-critical-fixes-and-reminders.md's full reminder system).
// Scoped to WHERE status = 'SCHEDULED' so two dispatchers racing on the
// same row can't both claim and send it.
func MarkProcessing(ctx context.Context, q db.Querier, id int64) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE reminders SET status = 'PROCESSING' WHERE id = $1 AND status = 'SCHEDULED'`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
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

// MarkCancelled records a reminder that was never sent because it no
// longer makes sense to — currently only "the debt was already settled
// before this reminder fired" (a defensive correctness check, not
// something the brief spelled out step by step, but spec §20 lists
// CANCELLED as a real state and reminding someone about a debt they've
// already paid off would be a real, visible mistake).
func MarkCancelled(ctx context.Context, q db.Querier, id int64, reason string) error {
	_, err := q.Exec(ctx, `
		UPDATE reminders SET status = 'CANCELLED', failure_reason = $1
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

func scanInto(r row, customerName *string, customerPhone **string, traderName *string, traderPhone **string, amountMinor *int64, dueDate **time.Time, debtStatus *string) (Reminder, error) {
	var rem Reminder
	var recipientType, status string
	err := r.Scan(&rem.ID, &rem.DebtID, &recipientType, &rem.RecipientID, &rem.ScheduledAt, &status,
		&rem.Attempts, &rem.Template, &rem.ProviderMessageID, &rem.SentAt, &rem.FailureReason, &rem.CreatedAt,
		customerName, customerPhone, traderName, traderPhone, amountMinor, dueDate, debtStatus)
	if err != nil {
		return Reminder{}, err
	}
	rem.RecipientType = RecipientType(recipientType)
	rem.Status = Status(status)
	return rem, nil
}
