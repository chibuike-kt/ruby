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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/ruby/internal/account"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/db"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/ledger"
	"github.com/chibuike-kt/ruby/internal/payment"
	"github.com/chibuike-kt/ruby/internal/reminder"
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
	case "audio", "image":
		msg.MediaID = contentReference
	case "interactive":
		msg.InteractiveID = contentReference
	}
	return msg
}

// Config wires every collaborator Processor needs. Extractor/Transcriber/
// Transcoder/Phraser/Sender/Media/Speaker are interfaces so tests can
// fake the AI provider and WhatsApp transport entirely.
type Config struct {
	Extractor   Extractor
	Transcriber Transcriber
	Transcoder  Transcoder
	Phraser     Phraser
	Sender      Sender
	Media       MediaDownloader
	// Speaker and Vision are optional (nil in any Config wired without
	// Part 5 Tier 1's voice-reply/photo-input features, e.g. an older
	// test double) — matches the established Reminders == nil pattern
	// elsewhere in this file.
	Speaker Speaker
	Vision  VisionExtractor

	Pool      *pgxpool.Pool
	Redis     *redis.Client
	Customers *customer.Service
	Debts     *debt.Service
	Payments  *payment.Service
	Reminders *reminder.Service

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

	// Voice replies (docs/BRIEF-research-hardening-standard.md Part 5
	// Tier 1): at minimum for voice-note-originated messages — a trader
	// who spoke a voice note most likely wants to hear one back, not
	// switch to reading. Skipped whenever the reply carries
	// buttons/a list: those need to be seen and tapped regardless, so a
	// voice-only version alongside them would be redundant, never a
	// substitute. Best-effort and never blocks the text reply that
	// already sent above — same reasoning as automatic trader reminders.
	if p.cfg.Speaker != nil && p.cfg.Sender != nil && msg.Type == "audio" &&
		reply.Text != "" && len(reply.Buttons) == 0 && reply.List == nil {
		p.sendVoiceReply(ctx, acct.PhoneNumber, reply.Text)
	}

	return reply, procErr
}

func (p *Processor) sendVoiceReply(ctx context.Context, to, text string) {
	audio, mimeType, err := p.cfg.Speaker.Speak(ctx, text)
	if err != nil {
		p.logf("voice reply synthesis failed", "error", err)
		return
	}
	if err := p.cfg.Sender.SendAudio(ctx, to, audio, mimeType); err != nil {
		p.logf("voice reply send failed", "error", err)
	}
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

	// Photo input (docs/BRIEF-research-hardening-standard.md Part 5 Tier
	// 1): treated as a fresh, deliberate action, exactly like the
	// interactive dispatch just above — it never falls through into
	// whatever text-shaped pending flow happens to be open (a slot-fill
	// question, a same/new prompt, ...), none of which know what to do
	// with an image anyway.
	if msg.Type == "image" {
		return p.handleImage(ctx, msg, acct)
	}

	// Interactive slot-filling edge case #4 (docs/BRIEF-critical-fixes-
	// and-reminders.md): "never mind"/"cancel"/equivalents must cleanly
	// exit *any* pending state, checked once here rather than per-flow.
	// Deterministic, no AI call — same reasoning as greeting detection.
	if hasPending {
		if cancelText, err := p.rawText(ctx, msg); err == nil && isCancelPhrase(cancelText) {
			if clearErr := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); clearErr != nil {
				p.logf("failed to clear pending action", "error", clearErr)
			}
			lang := stickyOrPendingLanguage(pending)
			return p.continueQueue(ctx, msg, textReply(fixedText(cancelledText, lang)), lang, nil, pending.Queue)
		}
	}

	if hasPending && pending.Kind == PendingDisambiguateCustomer {
		return p.handleDisambiguationReply(ctx, msg, pending)
	}

	// docs/BRIEF-polish-and-hardening.md #3: "buttons are additive,
	// never a replacement for typing the answer" — identity confirmation
	// and reminder opt-in are yes/no questions, so a typed answer is
	// classified the same deterministic way as isCancelPhrase, no AI
	// call, then dispatched through the exact same handler a button tap
	// uses (see handleIdentityConfirmationButtonReply/
	// handleReminderOptInButtonReply) so both entry points share one
	// execution path.
	if hasPending && pending.Kind == PendingIdentityConfirm {
		return p.handleIdentityConfirmationTextReply(ctx, msg, pending)
	}
	if hasPending && pending.Kind == PendingReminderOptIn {
		return p.handleReminderOptInTextReply(ctx, msg, pending)
	}

	// decisions.md #9's "New person" branch (docs/BRIEF-fixes-and-
	// reminders.md #3): a phone number or alias is a classification
	// step (phone-shaped or not), not a language-understanding one — no
	// AI call, same reasoning as disambiguation's text-reply matching.
	if hasPending && pending.Kind == PendingAwaitingCustomerSignal {
		return p.handleCustomerSignalReply(ctx, msg, pending)
	}

	// docs/BRIEF-fixes-and-reminders.md #4's phone-on-file follow-up —
	// deterministic phone-shape check, no AI call, same reasoning as
	// the customer-signal step above.
	if hasPending && pending.Kind == PendingAwaitingReminderPhone {
		return p.handleReminderPhoneReply(ctx, msg, pending)
	}

	// Interactive slot-filling (docs/BRIEF-critical-fixes-and-
	// reminders.md): unlike the deterministic flows above, this one
	// does its own text resolution and (usually) its own extractor
	// call — a slot-fill reply can carry real, ambiguous natural
	// language (edge cases #2/#3/#5/#6), not just a phone number or a
	// button id.
	if hasPending && pending.Kind == PendingSlotFill {
		return p.handleSlotFillReply(ctx, msg, pending)
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
	// docs/BRIEF-critical-fixes-and-reminders.md #2b: once a language is
	// established for this conversation, a short/ambiguous reply (an
	// amount, a bare name, "yes") must not re-detect and flip it —
	// only a message with enough content to genuinely signal a change
	// does.
	raw.Language = p.resolveStickyLanguage(ctx, msg.UserID, text, raw.Language)

	if hasPending && pending.Kind == PendingConfirm {
		if raw.Intent == IntentConfirmAction {
			if clearErr := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); clearErr != nil {
				p.logf("failed to clear pending action", "error", clearErr)
			}
			reply, err := p.executeConfirmed(ctx, msg, pending.Intent)
			return p.continueQueue(ctx, msg, reply, pending.Intent.Language, err, pending.Queue)
		}
		// The schema has no negative-confirmation signal (plan decision
		// #3): a reply that isn't CONFIRM_ACTION means the trader moved
		// on, not "no" — drop the stale pending state and process the
		// new message fresh.
		if clearErr := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); clearErr != nil {
			p.logf("failed to clear pending action", "error", clearErr)
		}
	}

	return p.processOneRawIntent(ctx, msg, acct, raw)
}

// processOneRawIntent runs raw through the exact same slot-fill/
// confirmation/execution pipeline every trader intent — however it
// arrived (typed, spoken, or read from a photo) — ultimately goes
// through. Shared by the ordinary text/audio path above and photo
// input's per-transaction loop (processExtractedIntents), so there is no
// separate, photo-specific decision logic to drift out of sync with the
// one text messages already use.
func (p *Processor) processOneRawIntent(ctx context.Context, msg InboundMessage, acct account.Account, raw RawIntent) (Reply, Language, error) {
	lang := raw.Language

	switch raw.Intent {
	case IntentConfirmAction:
		return textReply(fixedText(nothingToConfirmText, lang)), lang, nil
	case IntentHelp:
		return helpReply(lang), lang, nil
	case IntentSmallTalk:
		// docs/BRIEF-critical-fixes-and-reminders.md #3a: brief and
		// warm, never the capability-list fallback.
		return textReply(fixedText(smallTalkText, lang)), lang, nil
	case IntentSelfQuery:
		// #3b: answered straight from users.name, never a decline.
		return textReply(fmt.Sprintf(fixedText(selfQueryNameText, lang), acct.Name)), lang, nil
	case IntentDataSafety:
		// docs/BRIEF-research-hardening-standard.md Part 4: fixed text,
		// never AI-phrased — a real answer to "is my data safe," not a
		// landing-page FAQ improvised fresh every time.
		return textReply(fixedText(dataSafetyText, lang)), lang, nil
	}

	// Interactive slot-filling: CREATE_DEBT/RECORD_PAYMENT missing a
	// required field (customer identity or amount) never fails and
	// never guesses — it asks, and only reaches the backend once
	// every required field is present (docs/BRIEF-critical-fixes-and-
	// reminders.md). hint has to be checked here (not just inside
	// Validate) so a pronoun-resolvable customer never triggers an
	// unnecessary "who is this for?" — validateAndExecute below
	// re-fetches its own copy, one extra Redis read for the common
	// case, in exchange for not threading hint through every call.
	if requiredSlotFields(raw.Intent) != nil {
		hint, err := p.contextHint(ctx, msg.UserID)
		if err != nil {
			return Reply{}, lang, err
		}
		if missing := missingSlotFields(raw, hint, false); len(missing) > 0 {
			reply, err := p.beginSlotFill(ctx, msg.UserID, raw, missing)
			return reply, lang, err
		}
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
		case PendingIdentityConfirm:
			return p.handleIdentityConfirmationButtonReply(ctx, msg, pending, id)
		case PendingReminderOptIn:
			return p.handleReminderOptInButtonReply(ctx, msg, pending, id)
		case PendingSlotFill:
			return p.handleSlotFillButtonReply(ctx, msg, pending, id)
		}
	}

	switch id {
	case menuCreateDebt:
		return textReply(createDebtPromptText), LangEnglish, nil
	case menuBalance:
		reply, err := p.executeListOutstandingDebts(ctx, msg, RawIntent{Language: LangEnglish})
		return reply, LangEnglish, err
	default: // menuHelp and anything unrecognized
		return helpReply(LangEnglish), LangEnglish, nil
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
		return p.continueQueue(ctx, msg, reply, lang, err, pending.Queue)
	case buttonEdit:
		// "Edit can just prompt the trader to resend the message
		// correctly rather than building inline editing" — real scope,
		// explicitly skipped (docs/BRIEF-interactive-messages.md). A
		// photo queue riding along doesn't carry over here either — Edit
		// is itself already an explicit "start over" affordance, and
		// there's no clean way to "resend" a photo transaction inline.
		if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
			p.logf("failed to clear pending action", "error", err)
		}
		return textReply(fixedText(editPromptText, lang)), lang, nil
	case buttonCancel:
		if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
			p.logf("failed to clear pending action", "error", err)
		}
		return p.continueQueue(ctx, msg, textReply(fixedText(cancelledText, lang)), lang, nil, pending.Queue)
	default:
		// Unrecognized id while a confirmation is pending — re-ask
		// rather than silently dropping it.
		reply, err := p.beginConfirmation(ctx, msg, pending.Intent)
		return reply, lang, err
	}
}

// beginIdentityConfirmation is decisions.md #9's entry point
// (docs/BRIEF-fixes-and-reminders.md #3): CREATE_DEBT/RECORD_PAYMENT
// named a customer whose name matched exactly one existing customer,
// with nothing else confirming it's them. Parks the intent pending a
// same/new answer, exactly like beginConfirmation/beginDisambiguation —
// no new infrastructure, just a new trigger condition.
func (p *Processor) beginIdentityConfirmation(ctx context.Context, msg InboundMessage, raw RawIntent, identity *IdentityConfirmationError) (Reply, error) {
	candidate := PendingCandidateOption{CustomerID: identity.Candidate.ID, Name: identity.Candidate.Name}
	if identity.Candidate.PhoneNumber != nil {
		candidate.Phone = *identity.Candidate.PhoneNumber
	}
	if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{
		Kind:       PendingIdentityConfirm,
		Intent:     raw,
		Candidates: []PendingCandidateOption{candidate},
	}, DefaultPendingTTL); err != nil {
		return Reply{}, err
	}
	return identityConfirmationReply(raw.Language, identity.Candidate.Name), nil
}

func (p *Processor) handleIdentityConfirmationButtonReply(ctx context.Context, msg InboundMessage, pending PendingAction, id string) (Reply, Language, error) {
	lang := pending.Intent.Language
	if len(pending.Candidates) == 0 {
		return Reply{}, lang, errors.New("ai: identity confirmation pending with no candidate")
	}
	candidate := pending.Candidates[0]

	switch id {
	case buttonIdentitySame:
		if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
			p.logf("failed to clear pending action", "error", err)
		}
		resolvedID := candidate.CustomerID
		reply, err := p.validateAndExecuteWithHint(ctx, msg, pending.Intent, ContextHint{ResolvedCustomerID: &resolvedID})
		return reply, lang, err
	case buttonIdentityNew:
		reply, err := p.beginCustomerSignalRequest(ctx, msg, pending, candidate.Name)
		return reply, lang, err
	default:
		// Unrecognized id while an identity confirmation is pending —
		// re-ask rather than silently dropping it, same pattern as
		// handleConfirmationButtonReply's default case.
		return identityConfirmationReply(lang, candidate.Name), lang, nil
	}
}

// handleIdentityConfirmationTextReply is the free-text counterpart to
// handleIdentityConfirmationButtonReply (docs/BRIEF-polish-and-
// hardening.md #3) — an unclassifiable reply re-asks with the same
// text+buttons the original prompt sent, rather than silently dropping
// it or misreading it as a fresh, unrelated message.
func (p *Processor) handleIdentityConfirmationTextReply(ctx context.Context, msg InboundMessage, pending PendingAction) (Reply, Language, error) {
	lang := pending.Intent.Language
	if len(pending.Candidates) == 0 {
		return Reply{}, lang, errors.New("ai: identity confirmation pending with no candidate")
	}
	candidate := pending.Candidates[0]

	text, err := p.rawText(ctx, msg)
	if err != nil {
		return Reply{}, lang, err
	}
	switch yesNoPhrase(text) {
	case answerYes:
		return p.handleIdentityConfirmationButtonReply(ctx, msg, pending, buttonIdentitySame)
	case answerNo:
		return p.handleIdentityConfirmationButtonReply(ctx, msg, pending, buttonIdentityNew)
	default:
		return identityConfirmationReply(lang, candidate.Name), lang, nil
	}
}

// beginCustomerSignalRequest is decisions.md #8's creation guard
// (docs/BRIEF-fixes-and-reminders.md #3's "New person" branch): the
// trader just said this is *not* the existing candidate, so creating a
// same-named record needs a phone number or alias first. The pending
// intent carries forward unchanged — it's replayed once the signal is
// captured (handleCustomerSignalReply).
func (p *Processor) beginCustomerSignalRequest(ctx context.Context, msg InboundMessage, pending PendingAction, existingName string) (Reply, error) {
	lang := pending.Intent.Language
	if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{
		Kind:   PendingAwaitingCustomerSignal,
		Intent: pending.Intent,
	}, DefaultPendingTTL); err != nil {
		return Reply{}, err
	}
	return customerSignalRequestReply(lang, existingName), nil
}

// handleCustomerSignalReply captures the phone/alias decisions.md #8
// requires, creates the new (same-named) customer, and replays the
// original CREATE_DEBT/RECORD_PAYMENT intent against it — deterministic
// classification (phone-shaped text vs. alias), never an AI call, same
// reasoning as handleDisambiguationReply.
func (p *Processor) handleCustomerSignalReply(ctx context.Context, msg InboundMessage, pending PendingAction) (Reply, Language, error) {
	lang := pending.Intent.Language

	text, err := p.rawText(ctx, msg)
	if err != nil {
		return Reply{}, lang, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return textReply(customerSignalReaskText), lang, nil
	}

	name := trimOrEmpty(pending.Intent.CustomerName)
	if name == "" {
		return Reply{}, lang, errors.New("ai: awaiting customer signal with no pending customer name")
	}

	var phone, alias *string
	if looksLikePhone(text) {
		phone = &text
	} else {
		alias = &text
	}

	c, err := p.cfg.Customers.Create(ctx, msg.UserID, name, phone, alias)
	if err != nil {
		return Reply{}, lang, err
	}
	if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
		p.logf("failed to clear pending action", "error", err)
	}

	resolvedID := c.ID
	reply, err := p.validateAndExecuteWithHint(ctx, msg, pending.Intent, ContextHint{ResolvedCustomerID: &resolvedID})
	return reply, lang, err
}

// beginReminderOptIn parks the just-created debt pending a yes/no
// answer to the reminder question (docs/BRIEF-fixes-and-reminders.md
// #4). Only DebtID is stored — the customer and due date are re-fetched
// from the debt itself when the answer comes back, rather than
// duplicated into Redis, so this can never go stale relative to the
// debt record.
func (p *Processor) beginReminderOptIn(ctx context.Context, userID, debtID int64, lang Language) error {
	return SetPendingAction(ctx, p.cfg.Redis, userID, PendingAction{
		Kind:   PendingReminderOptIn,
		Intent: RawIntent{Language: lang},
		DebtID: &debtID,
	}, DefaultPendingTTL)
}

func (p *Processor) handleReminderOptInButtonReply(ctx context.Context, msg InboundMessage, pending PendingAction, id string) (Reply, Language, error) {
	lang := pending.Intent.Language
	if pending.DebtID == nil {
		return Reply{}, lang, errors.New("ai: reminder opt-in pending with no debt id")
	}

	switch id {
	case buttonReminderYes:
		return p.handleReminderOptInYes(ctx, msg, *pending.DebtID, lang, pending.Queue)
	case buttonReminderNo:
		if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
			p.logf("failed to clear pending action", "error", err)
		}
		return p.continueQueue(ctx, msg, textReply(fixedText(reminderDeclinedText, lang)), lang, nil, pending.Queue)
	default:
		// Unrecognized id — re-ask rather than silently dropping it,
		// same pattern as handleConfirmationButtonReply's default case.
		_, c, err := p.debtAndCustomer(ctx, msg.UserID, *pending.DebtID)
		if err != nil {
			return Reply{}, lang, err
		}
		return Reply{Text: reminderOptInSuffix(lang, c.Name), Buttons: reminderOptInButtons()}, lang, nil
	}
}

// handleReminderOptInTextReply is the free-text counterpart to
// handleReminderOptInButtonReply (docs/BRIEF-polish-and-hardening.md
// #3) — an unclassifiable reply re-asks with the same text+buttons the
// original opt-in question sent.
func (p *Processor) handleReminderOptInTextReply(ctx context.Context, msg InboundMessage, pending PendingAction) (Reply, Language, error) {
	lang := pending.Intent.Language
	if pending.DebtID == nil {
		return Reply{}, lang, errors.New("ai: reminder opt-in pending with no debt id")
	}

	text, err := p.rawText(ctx, msg)
	if err != nil {
		return Reply{}, lang, err
	}
	switch yesNoPhrase(text) {
	case answerYes:
		return p.handleReminderOptInButtonReply(ctx, msg, pending, buttonReminderYes)
	case answerNo:
		return p.handleReminderOptInButtonReply(ctx, msg, pending, buttonReminderNo)
	default:
		_, c, err := p.debtAndCustomer(ctx, msg.UserID, *pending.DebtID)
		if err != nil {
			return Reply{}, lang, err
		}
		return Reply{Text: reminderOptInSuffix(lang, c.Name), Buttons: reminderOptInButtons()}, lang, nil
	}
}

// handleReminderOptInYes is decisions.md's phone-on-file branch
// (docs/BRIEF-fixes-and-reminders.md #4): schedule immediately if the
// customer already has a phone number, otherwise ask for one first.
func (p *Processor) handleReminderOptInYes(ctx context.Context, msg InboundMessage, debtID int64, lang Language, queue []RawIntent) (Reply, Language, error) {
	d, c, err := p.debtAndCustomer(ctx, msg.UserID, debtID)
	if err != nil {
		return Reply{}, lang, err
	}

	if c.PhoneNumber == nil || strings.TrimSpace(*c.PhoneNumber) == "" {
		if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{
			Kind:   PendingAwaitingReminderPhone,
			Intent: RawIntent{Language: lang},
			DebtID: &debtID,
			Queue:  queue,
		}, DefaultPendingTTL); err != nil {
			return Reply{}, lang, err
		}
		return textReply(fixedText(reminderPhoneRequestText, lang)), lang, nil
	}

	return p.scheduleReminder(ctx, msg, d, c, lang, queue)
}

// handleReminderPhoneReply captures the phone number decisions.md's
// guard requires before scheduling — deterministic (phone-shaped or
// not), never an AI call, same reasoning as handleCustomerSignalReply.
func (p *Processor) handleReminderPhoneReply(ctx context.Context, msg InboundMessage, pending PendingAction) (Reply, Language, error) {
	lang := pending.Intent.Language
	if pending.DebtID == nil {
		return Reply{}, lang, errors.New("ai: awaiting reminder phone with no debt id")
	}

	text, err := p.rawText(ctx, msg)
	if err != nil {
		return Reply{}, lang, err
	}
	text = strings.TrimSpace(text)
	valid := looksLikePhone(text)
	if !valid {
		// docs/BRIEF-research-hardening-standard.md Part 2: the
		// reask-then-success contradiction reported earlier was
		// investigated exhaustively and never reproduced — this logs
		// the shape of the input, the validation result, and which
		// reply got queued, right at the decision point, so a repeat is
		// diagnosable in one look instead of requiring another full
		// investigation from scratch. Never the raw digits themselves —
		// spec §36 forbids logging a customer's phone number — only
		// enough shape (length, digit count) to tell what kind of input
		// tripped the check.
		p.infof("reminder phone validation", "user_id", msg.UserID, "debt_id", *pending.DebtID, "input_len", len(text), "input_digits", digitCount(text), "looks_like_phone", false, "reply_queued", "reask")
		// docs/BRIEF-research-hardening-standard.md Part 3: alternate
		// phrasing on a second consecutive failure — never repeat the
		// exact same reask sentence back to back.
		nextReaskCount := pending.ReaskCount + 1
		if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{Kind: PendingAwaitingReminderPhone, Intent: pending.Intent, DebtID: pending.DebtID, ReaskCount: nextReaskCount, Queue: pending.Queue}, DefaultPendingTTL); err != nil {
			return Reply{}, lang, err
		}
		reaskText := reminderPhoneReaskText
		if pending.ReaskCount%2 == 1 {
			reaskText = reminderPhoneReaskAgainText
		}
		return textReply(reaskText), lang, nil
	}

	d, c, err := p.debtAndCustomer(ctx, msg.UserID, *pending.DebtID)
	if err != nil {
		return Reply{}, lang, err
	}
	if err := customer.SetPhone(ctx, p.cfg.Pool, c.ID, text); err != nil {
		return Reply{}, lang, err
	}
	c.PhoneNumber = &text

	p.infof("reminder phone validation", "user_id", msg.UserID, "debt_id", *pending.DebtID, "input_len", len(text), "input_digits", digitCount(text), "looks_like_phone", true, "reply_queued", "scheduled")
	return p.scheduleReminder(ctx, msg, d, c, lang, pending.Queue)
}

// digitCount is a privacy-safe diagnostic helper (see
// handleReminderPhoneReply) — a count, never the digits themselves.
func digitCount(text string) int {
	n := 0
	for _, r := range text {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// scheduleReminder is the shared tail of both the phone-already-on-file
// and just-captured-it paths: schedule via internal/reminder, clear the
// pending state, confirm.
func (p *Processor) scheduleReminder(ctx context.Context, msg InboundMessage, d debt.Debt, c customer.Customer, lang Language, queue []RawIntent) (Reply, Language, error) {
	if p.cfg.Reminders != nil && d.DueDate != nil {
		if _, err := p.cfg.Reminders.OptIn(ctx, d.ID, c.ID, *d.DueDate); err != nil {
			return Reply{}, lang, err
		}
	}
	if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
		p.logf("failed to clear pending action", "error", err)
	}
	return p.continueQueue(ctx, msg, textReply(fmt.Sprintf(fixedText(reminderScheduledText, lang), c.Name)), lang, nil, queue)
}

// debtAndCustomer re-fetches the debt and its customer for a reminder
// opt-in pending state — see beginReminderOptIn's doc comment on why
// this is re-fetched rather than carried in Redis.
func (p *Processor) debtAndCustomer(ctx context.Context, userID, debtID int64) (debt.Debt, customer.Customer, error) {
	d, err := p.cfg.Debts.Get(ctx, userID, debtID)
	if err != nil {
		return debt.Debt{}, customer.Customer{}, err
	}
	c, err := customer.GetByID(ctx, p.cfg.Pool, userID, d.CustomerID)
	if err != nil {
		return debt.Debt{}, customer.Customer{}, err
	}
	return d, c, nil
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
// text but not a fresh intent extraction. Handle already logs procErr
// generically for every processing failure, but that's a Warn-level
// catch-all that doesn't say which of these three steps actually broke
// (docs/BRIEF-fixes-and-reminders.md #2) — every voice note was
// returning the same generic failure message with no way to tell
// download/transcode/transcribe apart, so each step logs its own error
// at Error level, tagged by stage, before returning it up.
func (p *Processor) transcribeAudio(ctx context.Context, msg InboundMessage) (string, Language, error) {
	if msg.MediaID == nil {
		return "", "", errors.New("ai: audio message missing media id")
	}
	ogg, _, err := p.cfg.Media.DownloadMedia(ctx, *msg.MediaID)
	if err != nil {
		p.errf("voice note pipeline failed", "stage", "download", "error", err)
		return "", "", err
	}
	transcoded, err := p.cfg.Transcoder.Transcode(ctx, ogg)
	if err != nil {
		p.errf("voice note pipeline failed", "stage", "transcode", "error", err)
		return "", "", err
	}
	text, lang, err := p.cfg.Transcriber.Transcribe(ctx, transcoded)
	if err != nil {
		p.errf("voice note pipeline failed", "stage", "transcribe", "error", err)
		return "", "", err
	}
	return text, lang, nil
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

	if identity, ok := errors.AsType[*IdentityConfirmationError](err); ok {
		return p.beginIdentityConfirmation(ctx, msg, raw, identity)
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

// outstandingDescriptions maps customer_id -> the most recent
// outstanding-debt description, for AmbiguousError.Hints
// (docs/BRIEF-disambiguation-reminders-statements.md Tier 1c: "the most
// recent transaction's item description"). ListOutstanding orders
// oldest-first, so always overwriting as it iterates leaves the last
// (most recent) description per customer standing.
func (p *Processor) outstandingDescriptions(ctx context.Context, userID int64) (map[int64]string, error) {
	debts, err := p.cfg.Debts.ListOutstanding(ctx, userID)
	if err != nil {
		return nil, err
	}
	byCustomer := map[int64]string{}
	for _, d := range debts {
		if d.Description != "" {
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
	case GetCustomerStatementAction:
		return p.executeGetCustomerStatement(ctx, msg, raw, a)
	case ListCustomersAction:
		return p.executeListCustomers(ctx, msg, raw)
	case ListOutstandingDebtsAction:
		return p.executeListOutstandingDebts(ctx, msg, raw)
	case GetTotalOutstandingAction:
		return p.executeGetTotalOutstanding(ctx, msg, raw)
	case GetPaymentSummaryAction:
		return p.executeGetPaymentSummary(ctx, msg, raw)
	case CreateReminderAction:
		return p.executeCreateReminder(ctx, msg, raw, a)
	case CancelReminderAction:
		return p.executeCancelReminder(ctx, msg, raw, a)
	case HelpAction:
		return helpReply(raw.Language), nil
	case UnsupportedAction:
		// docs/BRIEF-critical-fixes-and-reminders.md #1c: an honest
		// decline plus the real capability list.
		return unsupportedReply(raw.Language), nil
	default:
		return Reply{}, fmt.Errorf("ai: unknown action type %T", action)
	}
}

func (p *Processor) executeCreateDebt(ctx context.Context, msg InboundMessage, raw RawIntent, a CreateDebtAction) (Reply, error) {
	if raw.Confidence == ConfidenceLow {
		return p.beginConfirmation(ctx, msg, raw)
	}

	// Customer resolution/creation and the debt (+ledger) write happen
	// in one transaction (docs/BRIEF-critical-fixes-and-reminders.md
	// #1b): without this, a customer auto-created for a name that
	// doesn't exist yet commits immediately, and any failure in the
	// debt write that follows — a bad amount, a DB error, anything —
	// orphans that customer row. Mirrors the atomicity
	// payment.Service.Record already has for its own multi-write
	// sequence.
	var d debt.Debt
	var customerID int64
	var customerName string
	err := db.WithTx(ctx, p.cfg.Pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		customerID, customerName, err = p.resolveOrCreateCustomer(ctx, tx, msg.UserID, a.Customer)
		if err != nil {
			return err
		}
		d, err = debt.CreateWithLedger(ctx, tx, debt.Debt{
			UserID:      msg.UserID,
			CustomerID:  customerID,
			Amount:      a.Amount,
			Description: a.Description,
			DueDate:     a.DueDate,
			Status:      debt.StatusOutstanding,
		})
		return err
	})
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
	if err != nil {
		return textReply(text), err
	}
	reply := textReply(text)

	// Automatic trader reminders (docs/BRIEF-critical-fixes-and-
	// reminders.md's full reminder system): scheduled unconditionally
	// whenever a debt has a due date — this is the trader's own data
	// reflected back to them, no opt-in question, unlike the customer
	// reminder flow just below. A scheduling failure here is logged,
	// never surfaced to the trader or allowed to affect the debt-
	// created confirmation that already succeeded.
	if p.cfg.Reminders != nil && d.DueDate != nil {
		if _, err := p.cfg.Reminders.ScheduleTraderReminders(ctx, d.ID, msg.UserID, *d.DueDate); err != nil {
			p.logf("failed to schedule automatic trader reminders", "error", err)
		}
	}

	// The reminder opt-in question only makes sense with a due date to
	// count down to (docs/BRIEF-fixes-and-reminders.md #4) — combined
	// into this same confirmation message, not a separate follow-up
	// turn (same reasoning as firstContactReply). Reminders == nil means
	// this Processor was wired without the feature (e.g. an older test
	// double) — offering something Ruby couldn't actually schedule if
	// the trader said yes would be worse than not offering it.
	if p.cfg.Reminders != nil && d.DueDate != nil {
		if beginErr := p.beginReminderOptIn(ctx, msg.UserID, d.ID, raw.Language); beginErr != nil {
			p.logf("failed to set reminder opt-in pending state", "error", beginErr)
		} else {
			reply.Text += "\n\n" + reminderOptInSuffix(raw.Language, customerName)
			reply.Buttons = reminderOptInButtons()
		}
	}
	return reply, nil
}

// resolveOrCreateCustomer takes a db.Querier rather than always using
// p.cfg.Pool so a caller already inside a transaction (executeCreateDebt,
// docs/BRIEF-critical-fixes-and-reminders.md #1b) can pass its own tx —
// both *pgxpool.Pool and pgx.Tx satisfy db.Querier.
func (p *Processor) resolveOrCreateCustomer(ctx context.Context, q db.Querier, userID int64, ref CustomerRef) (int64, string, error) {
	if ref.ExistingID != nil {
		c, err := customer.GetByID(ctx, q, userID, *ref.ExistingID)
		if err != nil {
			return 0, "", err
		}
		return c.ID, c.Name, nil
	}
	if ref.NewName != nil {
		c, err := customer.CreateChecked(ctx, q, userID, *ref.NewName, nil, nil)
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
	// docs/BRIEF-final-demo-fixes.md #2: which event this is must come
	// from raw.Intent, the one thing Processor itself already knows for
	// certain — never left for the Phraser to guess from a generic
	// shared event name. beginConfirmation is reached from
	// executeCreateDebt/executeRecordPayment/executeCreateReminder's own
	// low-confidence gates, so raw.Intent is always one of these three.
	event := EventDebtConfirmationNeeded
	switch raw.Intent {
	case IntentRecordPayment:
		event = EventPaymentConfirmationNeeded
	case IntentCreateReminder:
		event = EventReminderConfirmationNeeded
	}
	// raw.AmountMinor is already *int64 — passed through as-is rather
	// than defaulted to 0 when nil, so a genuinely-missing amount never
	// masquerades as a real ₦0 in front of the phrasing model.
	text, err := p.phrase(ctx, PhraseInput{
		Event:        event,
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

// executeGetCustomerStatement is built deterministically in Go, not
// phrased by the AI (docs/BRIEF-disambiguation-reminders-statements.md
// Tier 3) — the same reasoning as executeListOutstandingDebts: this is
// real financial data being read back to a trader, and a list of
// amounts/dates is exactly the highest-risk surface for a model to
// drop or alter a number.
func (p *Processor) executeGetCustomerStatement(ctx context.Context, msg InboundMessage, raw RawIntent, a GetCustomerStatementAction) (Reply, error) {
	c, err := customer.GetByID(ctx, p.cfg.Pool, msg.UserID, a.CustomerID)
	if err != nil {
		return Reply{}, err
	}
	debts, err := p.cfg.Debts.ListByCustomer(ctx, msg.UserID, a.CustomerID)
	if err != nil {
		return Reply{}, err
	}
	if len(debts) == 0 {
		return textReply(fmt.Sprintf(fixedText(noStatementHistoryText, raw.Language), c.Name)), nil
	}

	lines := make([]statementDebtLine, len(debts))
	var outstandingMinor int64
	for i, d := range debts {
		payments, err := payment.ListByDebt(ctx, p.cfg.Pool, d.ID)
		if err != nil {
			return Reply{}, err
		}
		paidLines := make([]statementPaymentLine, len(payments))
		var paidMinor int64
		for j, pmt := range payments {
			paidLines[j] = statementPaymentLine{amountMinor: pmt.AmountMinor, date: pmt.CreatedAt}
			paidMinor += pmt.AmountMinor
		}
		remaining := d.Amount.MinorUnits() - paidMinor
		if d.Status != debt.StatusSettled {
			outstandingMinor += remaining
		}
		lines[i] = statementDebtLine{
			description: d.Description,
			amountMinor: d.Amount.MinorUnits(),
			date:        d.CreatedAt,
			payments:    paidLines,
		}
	}

	return textReply(formatCustomerStatement(c.Name, lines, outstandingMinor, raw.Language)), nil
}

// executeCreateReminder is docs/BRIEF-disambiguation-reminders-
// statements.md Tier 2a/2b's standalone reminder — invokable at any
// point in a conversation about any existing debt, using the date the
// trader actually asked for rather than requiring the underlying debt
// to have a due date on record. Applies to the customer's oldest
// outstanding debt, the same convention RECORD_PAYMENT already uses
// when a customer has more than one (plan decision #6) — the spec never
// defines multi-debt disambiguation and this session's scope doesn't
// add it. A trader-facing self-reminder (ScheduleTraderReminders), not
// the customer opt-in flow: Tier 2a's own example is "remind me," not
// "remind him."
func (p *Processor) executeCreateReminder(ctx context.Context, msg InboundMessage, raw RawIntent, a CreateReminderAction) (Reply, error) {
	// docs/BRIEF-research-hardening-standard.md Part 3: CREATE_REMINDER
	// schedules a real message to a real customer — the same stakes as
	// CREATE_DEBT/RECORD_PAYMENT, so a low-confidence extraction gets the
	// same confirm-before-acting gate those two already have, rather than
	// scheduling on an uncertain guess.
	if raw.Confidence == ConfidenceLow {
		return p.beginConfirmation(ctx, msg, raw)
	}

	c, err := customer.GetByID(ctx, p.cfg.Pool, msg.UserID, a.CustomerID)
	if err != nil {
		return Reply{}, err
	}
	target, err := p.oldestOutstandingDebt(ctx, msg.UserID, a.CustomerID)
	if err != nil {
		if errors.Is(err, errNoOutstandingDebt) {
			text, err := p.phrase(ctx, PhraseInput{Event: EventNoOutstandingDebt, Language: raw.Language, CustomerName: c.Name})
			return textReply(text), err
		}
		return Reply{}, err
	}
	if p.cfg.Reminders == nil {
		return textReply(fixedText(reminderUnsupportedText, raw.Language)), nil
	}
	if _, err := p.cfg.Reminders.ScheduleTraderReminders(ctx, target.ID, msg.UserID, a.ReminderDate); err != nil {
		return Reply{}, err
	}
	return textReply(fmt.Sprintf(fixedText(standaloneReminderScheduledText, raw.Language), c.Name, a.ReminderDate.Format(dueDateDisplayFormat))), nil
}

// executeCancelReminder is Tier 2c's CANCEL_REMINDER — cancels every
// still-SCHEDULED reminder (either recipient type: the automatic
// trader reminders and any customer opt-in) across all of the
// referenced customer's debts, since the trader references the
// customer or debt, not a specific reminder id they'd have no way of
// knowing.
func (p *Processor) executeCancelReminder(ctx context.Context, msg InboundMessage, raw RawIntent, a CancelReminderAction) (Reply, error) {
	c, err := customer.GetByID(ctx, p.cfg.Pool, msg.UserID, a.CustomerID)
	if err != nil {
		return Reply{}, err
	}
	if p.cfg.Reminders == nil {
		return textReply(fixedText(reminderUnsupportedText, raw.Language)), nil
	}
	debts, err := p.cfg.Debts.ListOutstanding(ctx, msg.UserID)
	if err != nil {
		return Reply{}, err
	}
	var cancelled int
	for _, d := range debts {
		if d.CustomerID != a.CustomerID {
			continue
		}
		n, err := p.cfg.Reminders.CancelForDebt(ctx, d.ID)
		if err != nil {
			return Reply{}, err
		}
		cancelled += n
	}
	if cancelled == 0 {
		return textReply(fmt.Sprintf(fixedText(noReminderScheduledText, raw.Language), c.Name)), nil
	}
	return textReply(fmt.Sprintf(fixedText(reminderCancelledText, raw.Language), c.Name)), nil
}

// executeListCustomers is built deterministically in Go, not phrased by
// the AI (docs/BRIEF-critical-fixes-and-reminders.md #2a) — an empty
// customer list left the Phraser to improvise a "stub response with no
// content" (Items omitted from the JSON entirely via omitempty), the
// same class of bug LIST_OUTSTANDING_DEBTS's own deterministic
// formatting already guards against.
func (p *Processor) executeListCustomers(ctx context.Context, msg InboundMessage, raw RawIntent) (Reply, error) {
	customers, err := customer.ListByUser(ctx, p.cfg.Pool, msg.UserID)
	if err != nil {
		return Reply{}, err
	}
	if len(customers) == 0 {
		return textReply(fixedText(noCustomersText, raw.Language)), nil
	}
	names := make([]string, len(customers))
	for i, c := range customers {
		names[i] = c.Name
	}
	return textReply(formatCustomerList(names, raw.Language)), nil
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
		lines[i] = outstandingDebtLine{customerID: d.CustomerID, customerName: c.Name, outstandingMinor: outstandingMinor, dueDate: d.DueDate}
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

// errf is logf's Error-level counterpart, for failures worth surfacing
// above the generic per-message Warn in Handle — see transcribeAudio's
// stage-tagged logging (docs/BRIEF-fixes-and-reminders.md #2).
func (p *Processor) errf(msg string, args ...any) {
	if p.cfg.Logger == nil {
		return
	}
	p.cfg.Logger.Error(msg, args...)
}

// infof is logf's Info-level counterpart, for routine, expected
// decision points worth recording for diagnosability without implying
// anything went wrong — see handleReminderPhoneReply's structured
// validation logging (docs/BRIEF-research-hardening-standard.md Part 2).
func (p *Processor) infof(msg string, args ...any) {
	if p.cfg.Logger == nil {
		return
	}
	p.cfg.Logger.Info(msg, args...)
}
