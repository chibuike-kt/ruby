package reminder

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/ruby/internal/money"
)

// TemplateSender sends a Meta-approved WhatsApp template message.
// Satisfied by *whatsapp.Service — this package never imports
// internal/whatsapp (plan decision #10's same one-directional-import
// reasoning: whatsapp already imports ai, and ai will import reminder,
// so reminder importing whatsapp back would cycle).
type TemplateSender interface {
	SendTemplate(ctx context.Context, to, templateName, languageCode string, bodyParams []string) (providerMessageID string, err error)
}

// Service schedules and dispatches debt-creation reminder opt-ins
// (docs/BRIEF-fixes-and-reminders.md #4).
type Service struct {
	pool         *pgxpool.Pool
	sender       TemplateSender
	templateName string
}

// NewService wires a Service. templateName names the Meta-approved
// template every reminder is sent through — see Config.ReminderTemplateName.
func NewService(pool *pgxpool.Pool, sender TemplateSender, templateName string) *Service {
	return &Service{pool: pool, sender: sender, templateName: templateName}
}

// OptIn schedules the two reminders a trader's "yes" answer requires.
func (s *Service) OptIn(ctx context.Context, debtID, customerID int64, dueDate time.Time) ([]Reminder, error) {
	return Schedule(ctx, s.pool, debtID, customerID, dueDate, s.templateName)
}

// dispatchBatchSize bounds one Dispatch call so a large backlog (e.g.
// after downtime) is processed in bounded chunks rather than one huge
// query — callers loop (see cmd/api/main.go's periodic dispatcher) until
// a call returns fewer than this many.
const dispatchBatchSize = 100

// reminderLanguageCode is fixed at English for now — spec §22's own
// hackathon-scope carve-out ("reliability takes priority over
// supporting every language") applies here too: a Meta-approved
// template is registered per language, and building/maintaining five
// approved variants is out of scope for this slice.
const reminderLanguageCode = "en"

// Dispatch sends every reminder due at or before now, via the
// templated-message API shape (docs/BRIEF-fixes-and-reminders.md #4) —
// never freeform text, since the customer has never messaged Ruby and
// this is a business-initiated conversation from message one.
//
// If no template has been approved by Meta yet in this environment,
// every send here fails with a real, expected error from the Cloud
// API — that's the honest failure mode the brief asks for, not
// something this method papers over: it's recorded via MarkFailed
// (attempts, failure_reason) exactly like any other send failure, not
// silently dropped or faked as success.
func (s *Service) Dispatch(ctx context.Context, now time.Time) (sent, failed int, err error) {
	candidates, err := DueForDispatch(ctx, s.pool, now, dispatchBatchSize)
	if err != nil {
		return 0, 0, err
	}

	for _, c := range candidates {
		if c.CustomerPhone == "" {
			if markErr := MarkFailed(ctx, s.pool, c.Reminder.ID, "customer has no phone number on file"); markErr != nil {
				return sent, failed, markErr
			}
			failed++
			continue
		}

		params := []string{c.CustomerName, money.FormatNaira(c.AmountMinor), c.DueDate.Format("2 Jan")}
		providerMessageID, sendErr := s.sender.SendTemplate(ctx, c.CustomerPhone, c.Reminder.Template, reminderLanguageCode, params)
		if sendErr != nil {
			if markErr := MarkFailed(ctx, s.pool, c.Reminder.ID, sendErr.Error()); markErr != nil {
				return sent, failed, markErr
			}
			failed++
			continue
		}

		if markErr := MarkSent(ctx, s.pool, c.Reminder.ID, providerMessageID, time.Now()); markErr != nil {
			return sent, failed, markErr
		}
		sent++
	}

	return sent, failed, nil
}
