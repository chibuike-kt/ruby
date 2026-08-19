package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/ruby/internal/account"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/ledger"
	"github.com/chibuike-kt/ruby/internal/payment"
)

var supportedLanguages = []Language{LangEnglish, LangPidgin, LangYoruba, LangIgbo, LangHausa}

func isSupportedLanguage(l Language) bool {
	return slices.Contains(supportedLanguages, l)
}

var errUnsupportedMessageType = errors.New("ai: unsupported message type")
var errNoOutstandingDebt = errors.New("ai: customer has no outstanding debt")

// InboundMessage is what Processor.Handle needs from a stored WhatsApp
// message — plain values, not a whatsapp.StoredMessage, so this package
// never imports internal/whatsapp (plan decision #10).
type InboundMessage struct {
	UserID            int64
	ProviderMessageID string
	Type              string // "text" or "audio"
	Text              *string
	MediaID           *string
}

// ToInboundMessage adapts whatsapp-shaped fields into an InboundMessage.
func ToInboundMessage(userID int64, providerMessageID, messageType string, contentReference *string) InboundMessage {
	msg := InboundMessage{UserID: userID, ProviderMessageID: providerMessageID, Type: messageType}
	switch messageType {
	case "text":
		msg.Text = contentReference
	case "audio":
		msg.MediaID = contentReference
	}
	return msg
}

// Config wires every collaborator Processor needs. Extractor/Transcriber/
// Transcoder/Phraser/Sender/Media are interfaces so tests can fake the
// AI provider and WhatsApp transport entirely.
type Config struct {
	Extractor   Extractor
	Transcriber Transcriber
	Transcoder  Transcoder
	Phraser     Phraser
	Sender      Sender
	Media       MediaDownloader

	Pool      *pgxpool.Pool
	Redis     *redis.Client
	Customers *customer.Service
	Debts     *debt.Service
	Payments  *payment.Service

	Logger *slog.Logger
}

// Processor is the conductor: understand (Extractor/Transcriber/
// Transcoder) -> validate (Validator) -> execute against the
// already-proven services -> phrase (Phraser) -> send (Sender). See
// docs/BRIEF-ai-intent.md and plan decisions #1-#11.
type Processor struct {
	cfg       Config
	validator *Validator
}

func NewProcessor(cfg Config) *Processor {
	return &Processor{cfg: cfg, validator: NewValidator(cfg.Customers)}
}

// Handle processes one inbound message end to end and sends the reply.
// Any error is turned into a fixed, friendly message before sending
// (spec §37) — the returned error is still surfaced to the caller for
// logging, but it's never what reaches the trader verbatim.
func (p *Processor) Handle(ctx context.Context, msg InboundMessage) (string, error) {
	acct, err := account.GetByID(ctx, p.cfg.Pool, msg.UserID)
	if err != nil {
		return "", err
	}

	reply, lang, procErr := p.process(ctx, msg)
	if procErr != nil {
		p.logf("ai processing failed", "error", procErr)
		reply = fixedText(genericErrorText, lang)
	}

	if p.cfg.Sender != nil && reply != "" {
		if sendErr := p.cfg.Sender.SendText(ctx, acct.PhoneNumber, reply); sendErr != nil {
			p.logf("ai reply send failed", "error", sendErr)
		}
	}

	return reply, procErr
}

func (p *Processor) process(ctx context.Context, msg InboundMessage) (string, Language, error) {
	pending, hasPending, err := GetPendingAction(ctx, p.cfg.Redis, msg.UserID)
	if err != nil {
		return "", LangEnglish, err
	}
	if hasPending && pending.Kind == PendingDisambiguateCustomer {
		return p.handleDisambiguationReply(ctx, msg, pending)
	}

	raw, err := p.extract(ctx, msg)
	if err != nil {
		if errors.Is(err, errUnsupportedMessageType) {
			return fixedText(unsupportedMessageTypeText, LangEnglish), LangEnglish, nil
		}
		return "", LangEnglish, err
	}
	lang := raw.Language

	if hasPending && pending.Kind == PendingConfirm {
		if raw.Intent == IntentConfirmAction {
			if clearErr := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); clearErr != nil {
				p.logf("failed to clear pending action", "error", clearErr)
			}
			reply, err := p.executeConfirmed(ctx, msg, pending.Intent)
			return reply, pending.Intent.Language, err
		}
		// The schema has no negative-confirmation signal (plan decision
		// #3): a reply that isn't CONFIRM_ACTION means the trader moved
		// on, not "no" — drop the stale pending state and process the
		// new message fresh.
		if clearErr := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); clearErr != nil {
			p.logf("failed to clear pending action", "error", clearErr)
		}
	}

	switch raw.Intent {
	case IntentCreateReminder, IntentCancelReminder:
		return fixedText(reminderUnsupportedText, lang), lang, nil
	case IntentConfirmAction:
		return fixedText(nothingToConfirmText, lang), lang, nil
	case IntentHelp:
		return fixedText(helpText, lang), lang, nil
	}

	reply, err := p.validateAndExecute(ctx, msg, raw)
	return reply, lang, err
}

func (p *Processor) extract(ctx context.Context, msg InboundMessage) (RawIntent, error) {
	switch msg.Type {
	case "text":
		if msg.Text == nil || strings.TrimSpace(*msg.Text) == "" {
			return RawIntent{}, errors.New("ai: empty text message")
		}
		return p.cfg.Extractor.Extract(ctx, *msg.Text)
	case "audio":
		return p.extractFromAudio(ctx, msg)
	default:
		return RawIntent{}, errUnsupportedMessageType
	}
}

func (p *Processor) extractFromAudio(ctx context.Context, msg InboundMessage) (RawIntent, error) {
	text, detectedLang, err := p.transcribeAudio(ctx, msg)
	if err != nil {
		return RawIntent{}, err
	}
	raw, err := p.cfg.Extractor.Extract(ctx, text)
	if err != nil {
		return RawIntent{}, err
	}
	// The transcription response's own detected language is preferred
	// over re-detecting from the transcript text, per current
	// gpt-transcribe docs (docs/BRIEF-ai-intent.md).
	if isSupportedLanguage(detectedLang) {
		raw.Language = detectedLang
	}
	return raw, nil
}

// transcribeAudio runs the full download -> transcode -> transcribe
// pipeline (spec §22) and is also reused by handleDisambiguationReply,
// which needs raw text but not a fresh intent extraction.
func (p *Processor) transcribeAudio(ctx context.Context, msg InboundMessage) (string, Language, error) {
	if msg.MediaID == nil {
		return "", "", errors.New("ai: audio message missing media id")
	}
	ogg, _, err := p.cfg.Media.DownloadMedia(ctx, *msg.MediaID)
	if err != nil {
		return "", "", err
	}
	transcoded, err := p.cfg.Transcoder.Transcode(ctx, ogg)
	if err != nil {
		return "", "", err
	}
	return p.cfg.Transcriber.Transcribe(ctx, transcoded, supportedLanguages)
}

func (p *Processor) rawText(ctx context.Context, msg InboundMessage) (string, error) {
	switch msg.Type {
	case "text":
		if msg.Text == nil {
			return "", nil
		}
		return *msg.Text, nil
	case "audio":
		text, _, err := p.transcribeAudio(ctx, msg)
		return text, err
	default:
		return "", errUnsupportedMessageType
	}
}

// handleDisambiguationReply matches the trader's reply locally
// (disambiguate.go) — no AI call, since this is a deterministic
// selection step, not a language-understanding one (plan decision #3).
func (p *Processor) handleDisambiguationReply(ctx context.Context, msg InboundMessage, pending PendingAction) (string, Language, error) {
	lang := pending.Intent.Language

	text, err := p.rawText(ctx, msg)
	if err != nil {
		return "", lang, err
	}

	match, ok := matchCandidate(text, pending.Candidates)
	if !ok {
		reply, err := p.phrase(ctx, PhraseInput{
			Event:    EventAmbiguousCustomer,
			Language: lang,
			Items:    candidateDisplay(pending.Candidates),
		})
		return reply, lang, err
	}

	if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
		p.logf("failed to clear pending action", "error", err)
	}

	resolvedID := match.CustomerID
	reply, err := p.validateAndExecuteWithHint(ctx, msg, pending.Intent, ContextHint{ResolvedCustomerID: &resolvedID})
	return reply, lang, err
}

func (p *Processor) validateAndExecute(ctx context.Context, msg InboundMessage, raw RawIntent) (string, error) {
	hint, err := p.contextHint(ctx, msg.UserID)
	if err != nil {
		return "", err
	}
	return p.validateAndExecuteWithHint(ctx, msg, raw, hint)
}

func (p *Processor) validateAndExecuteWithHint(ctx context.Context, msg InboundMessage, raw RawIntent, hint ContextHint) (string, error) {
	action, err := p.validator.Validate(ctx, msg.UserID, raw, hint)
	if err != nil {
		return p.handleValidationError(ctx, msg, raw, err)
	}
	return p.execute(ctx, msg, raw, action)
}

// executeConfirmed re-runs validation+execution for a pending intent a
// trader just confirmed, bypassing the confidence gate — a human just
// confirmed it, regardless of what the model's own confidence was.
func (p *Processor) executeConfirmed(ctx context.Context, msg InboundMessage, raw RawIntent) (string, error) {
	confirmed := raw
	confirmed.Confidence = ConfidenceHigh
	return p.validateAndExecute(ctx, msg, confirmed)
}

func (p *Processor) contextHint(ctx context.Context, userID int64) (ContextHint, error) {
	id, ok, err := customer.GetLastCustomerContext(ctx, p.cfg.Redis, userID)
	if err != nil {
		return ContextHint{}, err
	}
	if !ok {
		return ContextHint{}, nil
	}
	return ContextHint{LastCustomerID: &id}, nil
}

func (p *Processor) handleValidationError(ctx context.Context, msg InboundMessage, raw RawIntent, err error) (string, error) {
	if ambiguous, ok := errors.AsType[*customer.AmbiguousError](err); ok {
		return p.beginDisambiguation(ctx, msg, raw, ambiguous)
	}

	if notFound, ok := errors.AsType[*CustomerNotFoundError](err); ok {
		return p.phrase(ctx, PhraseInput{Event: EventCustomerNotFound, Language: raw.Language, CustomerName: notFound.Name})
	}

	switch {
	case errors.Is(err, ErrAmountRequired):
		return p.phrase(ctx, PhraseInput{Event: EventAmountRequired, Language: raw.Language, CustomerName: trimOrEmpty(raw.CustomerName)})
	case errors.Is(err, ErrCustomerRequired):
		return p.phrase(ctx, PhraseInput{Event: EventCustomerRequired, Language: raw.Language})
	default:
		return "", err
	}
}

func (p *Processor) beginDisambiguation(ctx context.Context, msg InboundMessage, raw RawIntent, ambiguous *customer.AmbiguousError) (string, error) {
	descriptions, err := p.outstandingDescriptions(ctx, msg.UserID)
	if err != nil {
		return "", err
	}
	hints := ambiguous.Hints(descriptions)

	candidates := make([]PendingCandidateOption, len(hints))
	items := make([]string, len(hints))
	for i, h := range hints {
		phone := ""
		if h.Customer.PhoneNumber != nil {
			phone = *h.Customer.PhoneNumber
		}
		candidates[i] = PendingCandidateOption{CustomerID: h.Customer.ID, Phone: phone, Hint: h.Hint}
		items[i] = fmt.Sprintf("%s (%s)", h.Customer.Name, h.Hint)
	}

	if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{
		Kind:       PendingDisambiguateCustomer,
		Intent:     raw,
		Candidates: candidates,
	}, DefaultPendingTTL); err != nil {
		return "", err
	}

	return p.phrase(ctx, PhraseInput{Event: EventAmbiguousCustomer, Language: raw.Language, Items: items})
}

// outstandingDescriptions maps customer_id -> one representative
// outstanding-debt description, for AmbiguousError.Hints (decisions.md
// #8: description is a real distinguishing signal). When a customer has
// more than one outstanding debt, the first one found is used — Hints
// only needs *a* description to detect divergence between candidates,
// not an exhaustive list.
func (p *Processor) outstandingDescriptions(ctx context.Context, userID int64) (map[int64]string, error) {
	debts, err := p.cfg.Debts.ListOutstanding(ctx, userID)
	if err != nil {
		return nil, err
	}
	byCustomer := map[int64]string{}
	for _, d := range debts {
		if _, ok := byCustomer[d.CustomerID]; !ok && d.Description != "" {
			byCustomer[d.CustomerID] = d.Description
		}
	}
	return byCustomer, nil
}

func candidateDisplay(candidates []PendingCandidateOption) []string {
	items := make([]string, len(candidates))
	for i, c := range candidates {
		items[i] = c.Hint
	}
	return items
}

func (p *Processor) execute(ctx context.Context, msg InboundMessage, raw RawIntent, action Action) (string, error) {
	switch a := action.(type) {
	case CreateDebtAction:
		return p.executeCreateDebt(ctx, msg, raw, a)
	case RecordPaymentAction:
		return p.executeRecordPayment(ctx, msg, raw, a)
	case GetCustomerBalanceAction:
		return p.executeGetCustomerBalance(ctx, msg, raw, a)
	case ListCustomersAction:
		return p.executeListCustomers(ctx, msg, raw)
	case ListOutstandingDebtsAction:
		return p.executeListOutstandingDebts(ctx, msg, raw)
	case GetTotalOutstandingAction:
		return p.executeGetTotalOutstanding(ctx, msg, raw)
	case GetPaymentSummaryAction:
		return p.executeGetPaymentSummary(ctx, msg, raw)
	case HelpAction:
		return fixedText(helpText, raw.Language), nil
	case UnsupportedAction:
		return fixedText(reminderUnsupportedText, raw.Language), nil
	default:
		return "", fmt.Errorf("ai: unknown action type %T", action)
	}
}

func (p *Processor) executeCreateDebt(ctx context.Context, msg InboundMessage, raw RawIntent, a CreateDebtAction) (string, error) {
	if raw.Confidence == ConfidenceLow {
		return p.beginConfirmation(ctx, msg, raw)
	}

	customerID, customerName, err := p.resolveOrCreateCustomer(ctx, msg.UserID, a.Customer)
	if err != nil {
		return "", err
	}

	d, err := p.cfg.Debts.Create(ctx, msg.UserID, customerID, a.Amount, a.Description, a.DueDate)
	if err != nil {
		return "", err
	}
	p.rememberLastCustomer(ctx, msg.UserID, customerID)

	dueDateISO := ""
	if d.DueDate != nil {
		dueDateISO = d.DueDate.Format(time.DateOnly)
	}
	return p.phrase(ctx, PhraseInput{
		Event:        EventDebtCreated,
		Language:     raw.Language,
		CustomerName: customerName,
		AmountMinor:  d.Amount.MinorUnits(),
		DueDateISO:   dueDateISO,
		Description:  d.Description,
	})
}

func (p *Processor) resolveOrCreateCustomer(ctx context.Context, userID int64, ref CustomerRef) (int64, string, error) {
	if ref.ExistingID != nil {
		c, err := customer.GetByID(ctx, p.cfg.Pool, userID, *ref.ExistingID)
		if err != nil {
			return 0, "", err
		}
		return c.ID, c.Name, nil
	}
	if ref.NewName != nil {
		c, err := p.cfg.Customers.Create(ctx, userID, *ref.NewName, nil, nil)
		if err != nil {
			return 0, "", err
		}
		return c.ID, c.Name, nil
	}
	return 0, "", errors.New("ai: customer reference has neither an existing id nor a new name")
}

func (p *Processor) executeRecordPayment(ctx context.Context, msg InboundMessage, raw RawIntent, a RecordPaymentAction) (string, error) {
	if raw.Confidence == ConfidenceLow {
		return p.beginConfirmation(ctx, msg, raw)
	}

	c, err := customer.GetByID(ctx, p.cfg.Pool, msg.UserID, a.CustomerID)
	if err != nil {
		return "", err
	}

	// Multiple outstanding debts for one customer: apply to the oldest
	// (plan decision #6) — the spec never defines multi-debt
	// disambiguation and the demo scenario never exercises it.
	target, err := p.oldestOutstandingDebt(ctx, msg.UserID, a.CustomerID)
	if err != nil {
		if errors.Is(err, errNoOutstandingDebt) {
			return p.phrase(ctx, PhraseInput{Event: EventNoOutstandingDebt, Language: raw.Language, CustomerName: c.Name})
		}
		return "", err
	}

	// whatsapp-level dedup (decisions.md #2) already guarantees this
	// exact message hasn't been handled before Processor.Handle ever
	// runs, so its provider_message_id is a safe, natural idempotency
	// key for the payment itself (spec §15).
	idempotencyKey := "whatsapp:" + msg.ProviderMessageID
	result, err := p.cfg.Payments.Record(ctx, msg.UserID, target.ID, a.Amount, idempotencyKey)
	if err != nil {
		if overpay, ok := errors.AsType[*payment.OverpaymentError](err); ok {
			return p.beginOverpaymentConfirmation(ctx, msg, raw, overpay)
		}
		return "", err
	}
	p.rememberLastCustomer(ctx, msg.UserID, a.CustomerID)

	paidMinor, err := payment.SumByDebt(ctx, p.cfg.Pool, target.ID)
	if err != nil {
		return "", err
	}

	return p.phrase(ctx, PhraseInput{
		Event:            EventPaymentRecorded,
		Language:         raw.Language,
		CustomerName:     c.Name,
		AmountMinor:      result.Payment.AmountMinor,
		OutstandingMinor: target.Amount.MinorUnits() - paidMinor,
	})
}

// beginOverpaymentConfirmation implements decisions.md #6: the default
// answer is "record the actual outstanding amount," never what the
// trader said, and only ever on an explicit confirmation.
func (p *Processor) beginOverpaymentConfirmation(ctx context.Context, msg InboundMessage, raw RawIntent, overpay *payment.OverpaymentError) (string, error) {
	adjusted := raw
	outstanding := overpay.Outstanding.MinorUnits()
	adjusted.AmountMinor = &outstanding
	adjusted.Confidence = ConfidenceHigh

	if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{Kind: PendingConfirm, Intent: adjusted}, DefaultPendingTTL); err != nil {
		return "", err
	}

	return p.phrase(ctx, PhraseInput{
		Event:            EventOverpaymentPrompt,
		Language:         raw.Language,
		CustomerName:     trimOrEmpty(raw.CustomerName),
		OutstandingMinor: overpay.Outstanding.MinorUnits(),
		AttemptedMinor:   overpay.Attempted.MinorUnits(),
	})
}

func (p *Processor) oldestOutstandingDebt(ctx context.Context, userID, customerID int64) (debt.Debt, error) {
	debts, err := p.cfg.Debts.ListOutstanding(ctx, userID)
	if err != nil {
		return debt.Debt{}, err
	}
	var oldest debt.Debt
	found := false
	for _, d := range debts {
		if d.CustomerID != customerID {
			continue
		}
		if !found || d.CreatedAt.Before(oldest.CreatedAt) {
			oldest = d
			found = true
		}
	}
	if !found {
		return debt.Debt{}, errNoOutstandingDebt
	}
	return oldest, nil
}

func (p *Processor) beginConfirmation(ctx context.Context, msg InboundMessage, raw RawIntent) (string, error) {
	if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{Kind: PendingConfirm, Intent: raw}, DefaultPendingTTL); err != nil {
		return "", err
	}
	amount := int64(0)
	if raw.AmountMinor != nil {
		amount = *raw.AmountMinor
	}
	return p.phrase(ctx, PhraseInput{
		Event:        EventConfirmationNeeded,
		Language:     raw.Language,
		CustomerName: trimOrEmpty(raw.CustomerName),
		AmountMinor:  amount,
		DueDateISO:   trimOrEmpty(raw.DueDateISO),
		Description:  trimOrEmpty(raw.Description),
	})
}

func (p *Processor) executeGetCustomerBalance(ctx context.Context, msg InboundMessage, raw RawIntent, a GetCustomerBalanceAction) (string, error) {
	c, err := customer.GetByID(ctx, p.cfg.Pool, msg.UserID, a.CustomerID)
	if err != nil {
		return "", err
	}
	debts, err := p.cfg.Debts.ListOutstanding(ctx, msg.UserID)
	if err != nil {
		return "", err
	}
	var outstanding int64
	for _, d := range debts {
		if d.CustomerID != a.CustomerID {
			continue
		}
		paid, err := payment.SumByDebt(ctx, p.cfg.Pool, d.ID)
		if err != nil {
			return "", err
		}
		outstanding += d.Amount.MinorUnits() - paid
	}
	return p.phrase(ctx, PhraseInput{Event: EventCustomerBalance, Language: raw.Language, CustomerName: c.Name, OutstandingMinor: outstanding})
}

func (p *Processor) executeListCustomers(ctx context.Context, msg InboundMessage, raw RawIntent) (string, error) {
	customers, err := customer.ListByUser(ctx, p.cfg.Pool, msg.UserID)
	if err != nil {
		return "", err
	}
	items := make([]string, len(customers))
	for i, c := range customers {
		items[i] = c.Name
	}
	return p.phrase(ctx, PhraseInput{Event: EventCustomerList, Language: raw.Language, Items: items})
}

func (p *Processor) executeListOutstandingDebts(ctx context.Context, msg InboundMessage, raw RawIntent) (string, error) {
	debts, err := p.cfg.Debts.ListOutstanding(ctx, msg.UserID)
	if err != nil {
		return "", err
	}
	items := make([]string, len(debts))
	for i, d := range debts {
		c, err := customer.GetByID(ctx, p.cfg.Pool, msg.UserID, d.CustomerID)
		if err != nil {
			return "", err
		}
		items[i] = fmt.Sprintf("%s: %d", c.Name, d.Amount.MinorUnits())
	}
	return p.phrase(ctx, PhraseInput{Event: EventOutstandingList, Language: raw.Language, Items: items})
}

func (p *Processor) executeGetTotalOutstanding(ctx context.Context, msg InboundMessage, raw RawIntent) (string, error) {
	summaries, err := ledger.SummaryByUser(ctx, p.cfg.Pool, msg.UserID)
	if err != nil {
		return "", err
	}
	var total int64
	if len(summaries) > 0 {
		total = summaries[0].TotalOutstandingMinor
	}
	return p.phrase(ctx, PhraseInput{Event: EventTotalOutstanding, Language: raw.Language, OutstandingMinor: total})
}

func (p *Processor) executeGetPaymentSummary(ctx context.Context, msg InboundMessage, raw RawIntent) (string, error) {
	summaries, err := ledger.SummaryByUser(ctx, p.cfg.Pool, msg.UserID)
	if err != nil {
		return "", err
	}
	var s ledger.Summary
	if len(summaries) > 0 {
		s = summaries[0]
	}
	return p.phrase(ctx, PhraseInput{
		Event:            EventPaymentSummary,
		Language:         raw.Language,
		AmountMinor:      s.TotalCreditIssuedMinor,
		OutstandingMinor: s.TotalOutstandingMinor,
		CollectedMinor:   s.TotalCollectedMinor,
	})
}

func (p *Processor) rememberLastCustomer(ctx context.Context, userID, customerID int64) {
	if p.cfg.Redis == nil {
		return
	}
	if err := customer.SetLastCustomerContext(ctx, p.cfg.Redis, userID, customerID, customer.DefaultLastCustomerContextTTL); err != nil {
		p.logf("failed to set conversational context", "error", err)
	}
}

// phrase calls the Phraser, or falls back to a fixed generic-error
// string if none is configured (tests that don't care about phrasing
// output can leave Phraser nil).
func (p *Processor) phrase(ctx context.Context, input PhraseInput) (string, error) {
	if p.cfg.Phraser == nil {
		return fixedText(genericErrorText, input.Language), nil
	}
	return p.cfg.Phraser.Phrase(ctx, input)
}

func (p *Processor) logf(msg string, args ...any) {
	if p.cfg.Logger == nil {
		return
	}
	p.cfg.Logger.Warn(msg, args...)
}
