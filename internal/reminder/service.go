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

// Service schedules and dispatches the full reminder system
// (docs/BRIEF-critical-fixes-and-reminders.md): automatic trader
// reminders whenever a debt gets a due date, and opt-in customer
// reminders (docs/BRIEF-fixes-and-reminders.md #4).
type Service struct {
	pool                 *pgxpool.Pool
	sender               TemplateSender
	customerTemplateName string
	traderTemplateName   string
}

// NewService wires a Service. customerTemplateName and
// traderTemplateName are the two Meta-approved templates every
// reminder is sent through — separate templates because the two
// message bodies are genuinely different (spec §18's own two examples:
// the customer's names the trader's business, the trader's names the
// customer and reads more like an internal notice).
func NewService(pool *pgxpool.Pool, sender TemplateSender, customerTemplateName, traderTemplateName string) *Service {
	return &Service{pool: pool, sender: sender, customerTemplateName: customerTemplateName, traderTemplateName: traderTemplateName}
}

// OptIn schedules the two customer reminders a trader's "yes" answer
// requires (docs/BRIEF-fixes-and-reminders.md #4).
func (s *Service) OptIn(ctx context.Context, debtID, customerID int64, dueDate time.Time) ([]Reminder, error) {
	return ScheduleCustomer(ctx, s.pool, debtID, customerID, dueDate, s.customerTemplateName)
}

// ScheduleTraderReminders schedules the two automatic trader reminders
// every debt with a due date gets — no opt-in, no consent question:
// this is the trader's own data reflected back to them
// (docs/BRIEF-critical-fixes-and-reminders.md's full reminder system).
// It's also reused directly for docs/BRIEF-disambiguation-reminders-
// statements.md Tier 2a/2b's standalone CREATE_REMINDER intent
// ("remind me about his payment tomorrow"), passing the date the
// trader actually asked for rather than the debt's own due date — the
// same day-before/day-of scheduling shape, just invoked at a different
// point in the conversation and off a different date source.
func (s *Service) ScheduleTraderReminders(ctx context.Context, debtID, userID int64, dueDate time.Time) ([]Reminder, error) {
	return ScheduleTrader(ctx, s.pool, debtID, userID, dueDate, s.traderTemplateName)
}

// CancelForDebt cancels every scheduled reminder attached to debtID —
// docs/BRIEF-disambiguation-reminders-statements.md Tier 2c's
// CANCEL_REMINDER intent.
func (s *Service) CancelForDebt(ctx context.Context, debtID int64) (int, error) {
	return CancelForDebt(ctx, s.pool, debtID, "cancelled by trader")
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

// relativeDayWord picks "today" or "tomorrow" for the trader
// template's own wording (spec §18: "Chinedu's ₦75,000 payment is due
// today") — the two scheduled times (day-before, due-date) are the only
// two values this can ever be, computed from which one this row is
// rather than stored as its own column.
func relativeDayWord(scheduledAt, dueDate time.Time) string {
	if scheduledAt.Year() == dueDate.Year() && scheduledAt.YearDay() == dueDate.YearDay() {
		return "today"
	}
	return "tomorrow"
}

// Dispatch sends every reminder due at or before now, via the
// templated-message API shape — never freeform text, for either
// recipient (docs/BRIEF-critical-fixes-and-reminders.md: "The WhatsApp
// template constraint applies to both trader and customer reminders").
// Status moves through spec §20's state machine: SCHEDULED ->
// PROCESSING (claimed, so a second concurrent dispatcher can't also
// send it) -> SENT or FAILED, with failure reason and attempt count
// recorded on failure. A reminder for a debt that's already SETTLED is
// CANCELLED instead of sent — reminding someone about a paid-off debt
// would be a real, visible mistake, not just an unsent message.
//
// If no template has been approved by Meta yet in this environment,
// every send here fails with a real, expected error from the Cloud
// API — that's the honest failure mode the brief asks for, not
// something this method papers over.
func (s *Service) Dispatch(ctx context.Context, now time.Time) (sent, failed int, err error) {
	candidates, err := DueForDispatch(ctx, s.pool, now, dispatchBatchSize)
	if err != nil {
		return 0, 0, err
	}

	for _, c := range candidates {
		claimed, claimErr := MarkProcessing(ctx, s.pool, c.Reminder.ID)
		if claimErr != nil {
			return sent, failed, claimErr
		}
		if !claimed {
			// Already claimed by a concurrent dispatch run — skip, not
			// a failure.
			continue
		}

		if c.DebtSettled {
			if err := MarkCancelled(ctx, s.pool, c.Reminder.ID, "debt already settled before this reminder fired"); err != nil {
				return sent, failed, err
			}
			continue
		}

		if c.RecipientPhone == "" {
			if err := MarkFailed(ctx, s.pool, c.Reminder.ID, "recipient has no phone number on file"); err != nil {
				return sent, failed, err
			}
			failed++
			continue
		}

		templateName, params := s.templateAndParams(c)
		providerMessageID, sendErr := s.sender.SendTemplate(ctx, c.RecipientPhone, templateName, reminderLanguageCode, params)
		if sendErr != nil {
			if err := MarkFailed(ctx, s.pool, c.Reminder.ID, sendErr.Error()); err != nil {
				return sent, failed, err
			}
			failed++
			continue
		}

		if err := MarkSent(ctx, s.pool, c.Reminder.ID, providerMessageID, time.Now()); err != nil {
			return sent, failed, err
		}
		sent++
	}

	return sent, failed, nil
}

// templateAndParams picks the right Meta-approved template and its
// body parameters for c's recipient type — the two templates have
// genuinely different bodies (spec §18's two examples), so they're
// never interchangeable.
func (s *Service) templateAndParams(c DispatchCandidate) (string, []string) {
	amount := money.FormatNaira(c.AmountMinor)
	switch c.Reminder.RecipientType {
	case RecipientTrader:
		return s.traderTemplateName, []string{c.CustomerName, amount, relativeDayWord(c.Reminder.ScheduledAt, c.DueDate)}
	default: // RecipientCustomer
		return s.customerTemplateName, []string{c.CustomerName, amount, c.TraderName, c.DueDate.Format("2 Jan")}
	}
}
