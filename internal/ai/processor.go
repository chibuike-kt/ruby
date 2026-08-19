package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
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

// Confirmation button ids (spec §23, docs/BRIEF-interactive-messages.md).
// Fixed, English-only, deliberately not run through any translation
// table — see the brief's "What NOT to build": short generic labels
// like these already read fine across all five supported languages.
const (
	buttonConfirm = "confirm"
	buttonEdit    = "edit"
	buttonCancel  = "cancel"
)

func confirmationButtons() []Button {
	return []Button{
		{ID: buttonConfirm, Title: "Confirm"},
		{ID: buttonEdit, Title: "Edit"},
		{ID: buttonCancel, Title: "Cancel"},
	}
}

// InboundMessage is what Processor.Handle needs from a stored WhatsApp
// message — plain values, not a whatsapp.StoredMessage, so this package
// never imports internal/whatsapp (plan decision #10).
type InboundMessage struct {
	UserID            int64
	ProviderMessageID string
	Type              string // "text", "audio", or "interactive"
	Text              *string
	MediaID           *string
	InteractiveID     *string // set when Type == "interactive": the button_reply/list_reply id
}

// ToInboundMessage adapts whatsapp-shaped fields into an InboundMessage.
func ToInboundMessage(userID int64, providerMessageID, messageType string, contentReference *string) InboundMessage {
	msg := InboundMessage{UserID: userID, ProviderMessageID: providerMessageID, Type: messageType}
	switch messageType {
	case "text":
		msg.Text = contentReference
	case "audio":
		msg.MediaID = contentReference
	case "interactive":
		msg.InteractiveID = contentReference
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
func (p *Processor) Handle(ctx context.Context, msg InboundMessage) (Reply, error) {
	acct, err := account.GetByID(ctx, p.cfg.Pool, msg.UserID)
	if err != nil {
		return Reply{}, err
	}

	reply, lang, procErr := p.process(ctx, msg, acct)
	if procErr != nil {
		p.logf("ai processing failed", "error", procErr)
		reply = textReply(fixedText(genericErrorText, lang))
	}

	if p.cfg.Sender != nil && reply.Text != "" {
		if sendErr := p.send(ctx, acct.PhoneNumber, reply); sendErr != nil {
			p.logf("ai reply send failed", "error", sendErr)
		}
	}

	return reply, procErr
}

// send picks SendList/SendButtons/SendText based on what reply actually
// carries — buttons/list are an enhancement, so a Reply with neither
// still sends correctly as plain text.
func (p *Processor) send(ctx context.Context, to string, reply Reply) error {
	switch {
	case reply.List != nil:
		return p.cfg.Sender.SendList(ctx, to, reply.Text, reply.List.ButtonLabel, reply.List.Sections)
	case len(reply.Buttons) > 0:
		return p.cfg.Sender.SendButtons(ctx, to, reply.Text, reply.Buttons)
	default:
		return p.cfg.Sender.SendText(ctx, to, reply.Text)
	}
}

func (p *Processor) process(ctx context.Context, msg InboundMessage, acct account.Account) (Reply, Language, error) {
	// An account with no name yet is either a brand-new contact or one
	// that dropped off mid-onboarding — either way, name capture takes
	// priority over everything else (docs/BRIEF-response-quality.md #1).
	// Once a name is set, this is never reached again for that account.
	if acct.Name == "" {
		return p.handleNameCapture(ctx, msg)
	}

	pending, hasPending, err := GetPendingAction(ctx, p.cfg.Redis, msg.UserID)
	if err != nil {
		return Reply{}, LangEnglish, err
	}

	// Interactive replies carry a deterministic id, never free text —
	// dispatched directly, never through greeting detection or the AI
	// extractor (docs/BRIEF-interactive-messages.md).
	if msg.Type == "interactive" {
		return p.handleInteractive(ctx, msg, pending, hasPending)
	}

	if hasPending && pending.Kind == PendingDisambiguateCustomer {
		return p.handleDisambiguationReply(ctx, msg, pending)
	}

	text, detectedLang, err := p.resolveText(ctx, msg)
	if err != nil {
		if errors.Is(err, errUnsupportedMessageType) {
			return textReply(fixedText(unsupportedMessageTypeText, LangEnglish)), LangEnglish, nil
		}
		return Reply{}, LangEnglish, err
	}
	if strings.TrimSpace(text) == "" {
		return Reply{}, LangEnglish, errors.New("ai: empty message")
	}

	// A bare greeting must never reach the financial-intent extractor
	// (docs/BRIEF-interactive-messages.md) — checked before any AI call
	// at all, not as a possible classification outcome of one. By this
	// point acct.Name is guaranteed non-empty (handleNameCapture already
	// intercepted the nameless case above), so every greeting here is a
	// returning trader — docs/BRIEF-response-quality.md #2: it should
	// feel like talking to an assistant that knows you, not a fresh
	// pitch on every "hi."
	if lang, ok := greetingLanguage(text); ok {
		return returningGreetingReply(lang, acct.Name), lang, nil
	}

	raw, err := p.cfg.Extractor.Extract(ctx, text)
	if err != nil {
		return Reply{}, LangEnglish, err
	}
	// The transcription response's own detected language is preferred
	// over re-detecting from the transcript text, per current
	// gpt-transcribe docs (docs/BRIEF-ai-intent.md).
	if isSupportedLanguage(detectedLang) {
		raw.Language = detectedLang
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
		return textReply(fixedText(reminderUnsupportedText, lang)), lang, nil
	case IntentConfirmAction:
		return textReply(fixedText(nothingToConfirmText, lang)), lang, nil
	case IntentHelp:
		return textReply(fixedText(helpText, lang)), lang, nil
	}

	reply, err := p.validateAndExecute(ctx, msg, raw)
	return reply, lang, err
}

// handleInteractive deterministically dispatches an inbound
// button_reply/list_reply. Never runs the AI extractor: the id is
// always one Ruby itself generated (a customer_id, "confirm"/"edit"/
// "cancel", or a "menu:..." id), so there's nothing to interpret.
func (p *Processor) handleInteractive(ctx context.Context, msg InboundMessage, pending PendingAction, hasPending bool) (Reply, Language, error) {
	id := ""
	if msg.InteractiveID != nil {
		id = strings.TrimSpace(*msg.InteractiveID)
	}

	if hasPending {
		switch pending.Kind {
		case PendingDisambiguateCustomer:
			return p.handleDisambiguationButtonReply(ctx, msg, pending, id)
		case PendingConfirm:
			return p.handleConfirmationButtonReply(ctx, msg, pending, id)
		}
	}

	switch id {
	case menuCreateDebt:
		return textReply(createDebtPromptText), LangEnglish, nil
	case menuBalance:
		reply, err := p.executeListOutstandingDebts(ctx, msg, RawIntent{Language: LangEnglish})
		return reply, LangEnglish, err
	default: // menuHelp and anything unrecognized
		return textReply(fixedText(helpText, LangEnglish)), LangEnglish, nil
	}
}

func (p *Processor) handleDisambiguationButtonReply(ctx context.Context, msg InboundMessage, pending PendingAction, id string) (Reply, Language, error) {
	lang := pending.Intent.Language

	customerID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return p.reaskDisambiguation(ctx, pending, lang)
	}
	match, ok := matchCandidateByID(customerID, pending.Candidates)
	if !ok {
		return p.reaskDisambiguation(ctx, pending, lang)
	}

	if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
		p.logf("failed to clear pending action", "error", err)
	}

	resolvedID := match.CustomerID
	reply, err := p.validateAndExecuteWithHint(ctx, msg, pending.Intent, ContextHint{ResolvedCustomerID: &resolvedID})
	return reply, lang, err
}

func (p *Processor) reaskDisambiguation(ctx context.Context, pending PendingAction, lang Language) (Reply, Language, error) {
	text, err := p.phrase(ctx, PhraseInput{Event: EventAmbiguousCustomer, Language: lang, Items: candidateDisplay(pending.Candidates)})
	return disambiguationReplyFor(text, pending.Candidates), lang, err
}

func (p *Processor) handleConfirmationButtonReply(ctx context.Context, msg InboundMessage, pending PendingAction, id string) (Reply, Language, error) {
	lang := pending.Intent.Language

	switch id {
	case buttonConfirm:
		if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
			p.logf("failed to clear pending action", "error", err)
		}
		reply, err := p.executeConfirmed(ctx, msg, pending.Intent)
		return reply, lang, err
	case buttonEdit:
		// "Edit can just prompt the trader to resend the message
		// correctly rather than building inline editing" — real scope,
		// explicitly skipped (docs/BRIEF-interactive-messages.md).
		if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
			p.logf("failed to clear pending action", "error", err)
		}
		return textReply(fixedText(editPromptText, lang)), lang, nil
	case buttonCancel:
		if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
			p.logf("failed to clear pending action", "error", err)
		}
		return textReply(fixedText(cancelledText, lang)), lang, nil
	default:
		// Unrecognized id while a confirmation is pending — re-ask
		// rather than silently dropping it.
		reply, err := p.beginConfirmation(ctx, msg, pending.Intent)
		return reply, lang, err
	}
}

// resolveText returns the plain text behind a text or audio message —
// for audio, this runs the full download -> transcode -> transcribe
// pipeline (spec §22). It does not judge whether the text is
// meaningful; callers decide that — process rejects empty text before
// ever calling the extractor, while a disambiguation reply (rawText)
// tolerates it, since an empty/unmatched reply just means "ask again."
func (p *Processor) resolveText(ctx context.Context, msg InboundMessage) (string, Language, error) {
	switch msg.Type {
	case "text":
		if msg.Text == nil {
			return "", "", nil
		}
		return *msg.Text, "", nil
	case "audio":
		return p.transcribeAudio(ctx, msg)
	default:
		return "", "", errUnsupportedMessageType
	}
}

// transcribeAudio runs the full download -> transcode -> transcribe
// pipeline (spec §22) and is also reused by rawText, which needs raw
// text but not a fresh intent extraction.
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
	text, _, err := p.resolveText(ctx, msg)
	return text, err
}

// handleDisambiguationReply matches the trader's typed reply locally
// (disambiguate.go) — no AI call, since this is a deterministic
// selection step, not a language-understanding one (plan decision #3).
// Kept working exactly as before buttons existed
// (docs/BRIEF-interactive-messages.md: "Keep the existing text-reply
// matching path working too").
func (p *Processor) handleDisambiguationReply(ctx context.Context, msg InboundMessage, pending PendingAction) (Reply, Language, error) {
	lang := pending.Intent.Language

	text, err := p.rawText(ctx, msg)
	if err != nil {
		return Reply{}, lang, err
	}

	match, ok := matchCandidate(text, pending.Candidates)
	if !ok {
		return p.reaskDisambiguation(ctx, pending, lang)
	}

	if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
		p.logf("failed to clear pending action", "error", err)
	}

	resolvedID := match.CustomerID
	reply, err := p.validateAndExecuteWithHint(ctx, msg, pending.Intent, ContextHint{ResolvedCustomerID: &resolvedID})
	return reply, lang, err
}

func (p *Processor) validateAndExecute(ctx context.Context, msg InboundMessage, raw RawIntent) (Reply, error) {
	hint, err := p.contextHint(ctx, msg.UserID)
	if err != nil {
		return Reply{}, err
	}
	return p.validateAndExecuteWithHint(ctx, msg, raw, hint)
}

func (p *Processor) validateAndExecuteWithHint(ctx context.Context, msg InboundMessage, raw RawIntent, hint ContextHint) (Reply, error) {
	action, err := p.validator.Validate(ctx, msg.UserID, raw, hint)
	if err != nil {
		return p.handleValidationError(ctx, msg, raw, err)
	}
	return p.execute(ctx, msg, raw, action)
}

// executeConfirmed re-runs validation+execution for a pending intent a
// trader just confirmed, bypassing the confidence gate — a human just
// confirmed it, regardless of what the model's own confidence was.
func (p *Processor) executeConfirmed(ctx context.Context, msg InboundMessage, raw RawIntent) (Reply, error) {
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

func (p *Processor) handleValidationError(ctx context.Context, msg InboundMessage, raw RawIntent, err error) (Reply, error) {
	if ambiguous, ok := errors.AsType[*customer.AmbiguousError](err); ok {
		return p.beginDisambiguation(ctx, msg, raw, ambiguous)
	}

	if notFound, ok := errors.AsType[*CustomerNotFoundError](err); ok {
		text, err := p.phrase(ctx, PhraseInput{Event: EventCustomerNotFound, Language: raw.Language, CustomerName: notFound.Name})
		return textReply(text), err
	}

	switch {
	case errors.Is(err, ErrAmountRequired):
		text, err := p.phrase(ctx, PhraseInput{Event: EventAmountRequired, Language: raw.Language, CustomerName: trimOrEmpty(raw.CustomerName)})
		return textReply(text), err
	case errors.Is(err, ErrCustomerRequired):
		text, err := p.phrase(ctx, PhraseInput{Event: EventCustomerRequired, Language: raw.Language})
		return textReply(text), err
	default:
		return Reply{}, err
	}
}

func (p *Processor) beginDisambiguation(ctx context.Context, msg InboundMessage, raw RawIntent, ambiguous *customer.AmbiguousError) (Reply, error) {
	descriptions, err := p.outstandingDescriptions(ctx, msg.UserID)
	if err != nil {
		return Reply{}, err
	}
	hints := ambiguous.Hints(descriptions)

	candidates := make([]PendingCandidateOption, len(hints))
	for i, h := range hints {
		phone := ""
		if h.Customer.PhoneNumber != nil {
			phone = *h.Customer.PhoneNumber
		}
		candidates[i] = PendingCandidateOption{CustomerID: h.Customer.ID, Name: h.Customer.Name, Phone: phone, Hint: h.Hint}
	}

	if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{
		Kind:       PendingDisambiguateCustomer,
		Intent:     raw,
		Candidates: candidates,
	}, DefaultPendingTTL); err != nil {
		return Reply{}, err
	}

	text, err := p.phrase(ctx, PhraseInput{Event: EventAmbiguousCustomer, Language: raw.Language, Items: candidateDisplay(candidates)})
	if err != nil {
		return Reply{}, err
	}
	return disambiguationReplyFor(text, candidates), nil
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

func (p *Processor) execute(ctx context.Context, msg InboundMessage, raw RawIntent, action Action) (Reply, error) {
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
		return textReply(fixedText(helpText, raw.Language)), nil
	case UnsupportedAction:
		return textReply(fixedText(reminderUnsupportedText, raw.Language)), nil
	default:
		return Reply{}, fmt.Errorf("ai: unknown action type %T", action)
	}
}

func (p *Processor) executeCreateDebt(ctx context.Context, msg InboundMessage, raw RawIntent, a CreateDebtAction) (Reply, error) {
	if raw.Confidence == ConfidenceLow {
		return p.beginConfirmation(ctx, msg, raw)
	}

	customerID, customerName, err := p.resolveOrCreateCustomer(ctx, msg.UserID, a.Customer)
	if err != nil {
		return Reply{}, err
	}

	d, err := p.cfg.Debts.Create(ctx, msg.UserID, customerID, a.Amount, a.Description, a.DueDate)
	if err != nil {
		return Reply{}, err
	}
	p.rememberLastCustomer(ctx, msg.UserID, customerID)

	dueDateISO := ""
	if d.DueDate != nil {
		dueDateISO = d.DueDate.Format(time.DateOnly)
	}
	// A freshly created debt has no payments against it yet, so
	// outstanding always equals the full amount — never leave this
	// unset (a bug previously did, which the model then read as a
	// genuine ₦0 outstanding balance; see isGrounded/allowedNumbers).
	amountMinor := d.Amount.MinorUnits()
	text, err := p.phrase(ctx, PhraseInput{
		Event:            EventDebtCreated,
		Language:         raw.Language,
		CustomerName:     customerName,
		AmountMinor:      &amountMinor,
		OutstandingMinor: &amountMinor,
		DueDateISO:       dueDateISO,
		Description:      d.Description,
	})
	return textReply(text), err
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

func (p *Processor) executeRecordPayment(ctx context.Context, msg InboundMessage, raw RawIntent, a RecordPaymentAction) (Reply, error) {
	if raw.Confidence == ConfidenceLow {
		return p.beginConfirmation(ctx, msg, raw)
	}

	c, err := customer.GetByID(ctx, p.cfg.Pool, msg.UserID, a.CustomerID)
	if err != nil {
		return Reply{}, err
	}

	// Multiple outstanding debts for one customer: apply to the oldest
	// (plan decision #6) — the spec never defines multi-debt
	// disambiguation and the demo scenario never exercises it.
	target, err := p.oldestOutstandingDebt(ctx, msg.UserID, a.CustomerID)
	if err != nil {
		if errors.Is(err, errNoOutstandingDebt) {
			text, err := p.phrase(ctx, PhraseInput{Event: EventNoOutstandingDebt, Language: raw.Language, CustomerName: c.Name})
			return textReply(text), err
		}
		return Reply{}, err
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
		return Reply{}, err
	}
	p.rememberLastCustomer(ctx, msg.UserID, a.CustomerID)

	paidMinor, err := payment.SumByDebt(ctx, p.cfg.Pool, target.ID)
	if err != nil {
		return Reply{}, err
	}

	outstandingMinor := target.Amount.MinorUnits() - paidMinor
	text, err := p.phrase(ctx, PhraseInput{
		Event:            EventPaymentRecorded,
		Language:         raw.Language,
		CustomerName:     c.Name,
		AmountMinor:      &result.Payment.AmountMinor,
		OutstandingMinor: &outstandingMinor,
	})
	return textReply(text), err
}

// beginOverpaymentConfirmation implements decisions.md #6: the default
// answer is "record the actual outstanding amount," never what the
// trader said, and only ever on an explicit confirmation.
func (p *Processor) beginOverpaymentConfirmation(ctx context.Context, msg InboundMessage, raw RawIntent, overpay *payment.OverpaymentError) (Reply, error) {
	adjusted := raw
	outstanding := overpay.Outstanding.MinorUnits()
	adjusted.AmountMinor = &outstanding
	adjusted.Confidence = ConfidenceHigh

	if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{Kind: PendingConfirm, Intent: adjusted}, DefaultPendingTTL); err != nil {
		return Reply{}, err
	}

	attempted := overpay.Attempted.MinorUnits()
	text, err := p.phrase(ctx, PhraseInput{
		Event:            EventOverpaymentPrompt,
		Language:         raw.Language,
		CustomerName:     trimOrEmpty(raw.CustomerName),
		OutstandingMinor: &outstanding,
		AttemptedMinor:   &attempted,
	})
	if err != nil {
		return Reply{}, err
	}
	return Reply{Text: text, Buttons: confirmationButtons()}, nil
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

func (p *Processor) beginConfirmation(ctx context.Context, msg InboundMessage, raw RawIntent) (Reply, error) {
	if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{Kind: PendingConfirm, Intent: raw}, DefaultPendingTTL); err != nil {
		return Reply{}, err
	}
	// raw.AmountMinor is already *int64 — passed through as-is rather
	// than defaulted to 0 when nil, so a genuinely-missing amount never
	// masquerades as a real ₦0 in front of the phrasing model.
	text, err := p.phrase(ctx, PhraseInput{
		Event:        EventConfirmationNeeded,
		Language:     raw.Language,
		CustomerName: trimOrEmpty(raw.CustomerName),
		AmountMinor:  raw.AmountMinor,
		DueDateISO:   trimOrEmpty(raw.DueDateISO),
		Description:  trimOrEmpty(raw.Description),
	})
	if err != nil {
		return Reply{}, err
	}
	return Reply{Text: text, Buttons: confirmationButtons()}, nil
}

func (p *Processor) executeGetCustomerBalance(ctx context.Context, msg InboundMessage, raw RawIntent, a GetCustomerBalanceAction) (Reply, error) {
	c, err := customer.GetByID(ctx, p.cfg.Pool, msg.UserID, a.CustomerID)
	if err != nil {
		return Reply{}, err
	}
	debts, err := p.cfg.Debts.ListOutstanding(ctx, msg.UserID)
	if err != nil {
		return Reply{}, err
	}
	var outstanding int64
	for _, d := range debts {
		if d.CustomerID != a.CustomerID {
			continue
		}
		paid, err := payment.SumByDebt(ctx, p.cfg.Pool, d.ID)
		if err != nil {
			return Reply{}, err
		}
		outstanding += d.Amount.MinorUnits() - paid
	}
	text, err := p.phrase(ctx, PhraseInput{Event: EventCustomerBalance, Language: raw.Language, CustomerName: c.Name, OutstandingMinor: &outstanding})
	return textReply(text), err
}

func (p *Processor) executeListCustomers(ctx context.Context, msg InboundMessage, raw RawIntent) (Reply, error) {
	customers, err := customer.ListByUser(ctx, p.cfg.Pool, msg.UserID)
	if err != nil {
		return Reply{}, err
	}
	items := make([]string, len(customers))
	for i, c := range customers {
		items[i] = c.Name
	}
	text, err := p.phrase(ctx, PhraseInput{Event: EventCustomerList, Language: raw.Language, Items: items})
	return textReply(text), err
}

// executeListOutstandingDebts is built deterministically in Go, not
// phrased by the AI (docs/BRIEF-response-quality.md #4/#5): a list is
// exactly the highest-risk surface for the model to drop or alter a
// number, and the formatting spec here (bold name, amount+due date,
// blank line between entries, a total when there's more than one
// debtor) is already precise enough to just build directly — this also
// means the list can never conflict with the isGrounded backstop, since
// no model call happens for it at all. The empty state gets its own
// fixed, warm message rather than an awkward empty list or a Phraser
// call that might render nothing usefully.
func (p *Processor) executeListOutstandingDebts(ctx context.Context, msg InboundMessage, raw RawIntent) (Reply, error) {
	debts, err := p.cfg.Debts.ListOutstanding(ctx, msg.UserID)
	if err != nil {
		return Reply{}, err
	}
	if len(debts) == 0 {
		return textReply(fixedText(noOutstandingDebtsText, raw.Language)), nil
	}

	lines := make([]outstandingDebtLine, len(debts))
	var totalMinor int64
	for i, d := range debts {
		c, err := customer.GetByID(ctx, p.cfg.Pool, msg.UserID, d.CustomerID)
		if err != nil {
			return Reply{}, err
		}
		paid, err := payment.SumByDebt(ctx, p.cfg.Pool, d.ID)
		if err != nil {
			return Reply{}, err
		}
		outstandingMinor := d.Amount.MinorUnits() - paid
		lines[i] = outstandingDebtLine{customerName: c.Name, outstandingMinor: outstandingMinor, dueDate: d.DueDate}
		totalMinor += outstandingMinor
	}

	return textReply(formatOutstandingDebtsList(lines, totalMinor, raw.Language)), nil
}

func (p *Processor) executeGetTotalOutstanding(ctx context.Context, msg InboundMessage, raw RawIntent) (Reply, error) {
	summaries, err := ledger.SummaryByUser(ctx, p.cfg.Pool, msg.UserID)
	if err != nil {
		return Reply{}, err
	}
	var total int64
	if len(summaries) > 0 {
		total = summaries[0].TotalOutstandingMinor
	}
	text, err := p.phrase(ctx, PhraseInput{Event: EventTotalOutstanding, Language: raw.Language, OutstandingMinor: &total})
	return textReply(text), err
}

func (p *Processor) executeGetPaymentSummary(ctx context.Context, msg InboundMessage, raw RawIntent) (Reply, error) {
	summaries, err := ledger.SummaryByUser(ctx, p.cfg.Pool, msg.UserID)
	if err != nil {
		return Reply{}, err
	}
	var s ledger.Summary
	if len(summaries) > 0 {
		s = summaries[0]
	}
	text, err := p.phrase(ctx, PhraseInput{
		Event:            EventPaymentSummary,
		Language:         raw.Language,
		AmountMinor:      &s.TotalCreditIssuedMinor,
		OutstandingMinor: &s.TotalOutstandingMinor,
		CollectedMinor:   &s.TotalCollectedMinor,
	})
	return textReply(text), err
}

func (p *Processor) rememberLastCustomer(ctx context.Context, userID, customerID int64) {
	if p.cfg.Redis == nil {
		return
	}
	if err := customer.SetLastCustomerContext(ctx, p.cfg.Redis, userID, customerID, customer.DefaultLastCustomerContextTTL); err != nil {
		p.logf("failed to set conversational context", "error", err)
	}
}

// phrase calls the Phraser and enforces the boundary from the backend
// side: isGrounded checks that every number in the reply traces back to
// a field actually present in input (plan decision #7). "Terminal"
// never meant unchecked — a reply that fails this check is a boundary
// violation, not a phrasing choice, and never reaches the trader; a
// fixed fallback is used instead (never one that claims failure for an
// event that already succeeded — see fallbackText).
func (p *Processor) phrase(ctx context.Context, input PhraseInput) (string, error) {
	if p.cfg.Phraser == nil {
		return fallbackText(input.Event, input.Language), nil
	}
	text, err := p.cfg.Phraser.Phrase(ctx, input)
	if err != nil {
		return "", err
	}
	if !isGrounded(text, input) {
		p.logf("phrasing stated a number not present in its input — using a safe fallback", "event", input.Event)
		return fallbackText(input.Event, input.Language), nil
	}
	return text, nil
}

func (p *Processor) logf(msg string, args ...any) {
	if p.cfg.Logger == nil {
		return
	}
	p.cfg.Logger.Warn(msg, args...)
}
