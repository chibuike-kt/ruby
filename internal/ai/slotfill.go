package ai

import (
	"context"
	"fmt"
	"strings"
)

// SlotField is one required-but-missing piece of a CREATE_DEBT/
// RECORD_PAYMENT intent (docs/BRIEF-critical-fixes-and-reminders.md's
// interactive slot-filling section).
type SlotField string

const (
	SlotCustomer SlotField = "customer"
	SlotAmount   SlotField = "amount"
	// SlotDate is docs/BRIEF-disambiguation-reminders-statements.md
	// Tier 2b's addition: CREATE_REMINDER needs a date to schedule
	// against, independent of whether the underlying debt has a due
	// date on record — "if the reminder request itself doesn't include
	// a date either, ask for one at that point rather than declining
	// outright."
	SlotDate SlotField = "date"
)

// requiredSlotFields is the source of truth for what's required per
// intent. For CREATE_DEBT/RECORD_PAYMENT, due date and description are
// deliberately absent: they're optional, and 1a is explicit that Ruby
// must never ask for them. Order matters: identity before amount (edge
// case #1) — the same "ask the identity question first" ordering
// applies to CREATE_REMINDER's customer-then-date.
func requiredSlotFields(intent IntentType) []SlotField {
	switch intent {
	case IntentCreateDebt, IntentRecordPayment:
		return []SlotField{SlotCustomer, SlotAmount}
	case IntentCreateReminder:
		return []SlotField{SlotCustomer, SlotDate}
	case IntentCancelReminder:
		return []SlotField{SlotCustomer}
	default:
		return nil
	}
}

// missingSlotFields reports which required fields raw is still missing,
// in order. hint.ResolvedCustomerID (a one-shot signal from a
// disambiguation/identity-confirmation the trader just answered) always
// counts as resolved. hint.LastCustomerID (spec §8 signal 4, docs/
// BRIEF-critical-fixes-and-reminders.md #2c's pronoun resolution) is
// trusted to silently resolve identity for a fresh, single-message
// evaluation (withinSlotFill false) — but only when identity is the
// *only* thing raw is missing: "he paid me 30k" (amount already given)
// correctly resolves "he" from the last-referenced customer without
// asking, while "someone took goods" (amount also missing) must not.
//
// Once a multi-turn slot-fill exchange has actually begun
// (withinSlotFill true — every call from within handleSlotFillReply/
// handleSlotFillButtonReply), the hint is never trusted for identity at
// all, regardless of what else has since been filled: docs/BRIEF-final-
// demo-fixes.md #1's exact bug was the hint silently resolving identity
// on the *second* pass, once the reply that answered "how much?"
// happened to fill the amount slot via a secondary extraction — "3k",
// naming no one, still isn't an identity answer, and once Ruby has
// committed to asking for identity explicitly it must get an explicit
// answer, never fall back to a customer mentioned several messages
// earlier.
func missingSlotFields(raw RawIntent, hint ContextHint, withinSlotFill bool) []SlotField {
	var missing []SlotField
	for _, f := range requiredSlotFields(raw.Intent) {
		switch f {
		case SlotCustomer:
			if trimOrEmpty(raw.CustomerName) != "" || hint.ResolvedCustomerID != nil {
				continue
			}
			if hint.LastCustomerID == nil || withinSlotFill || identityMustBeExplicit(raw) {
				missing = append(missing, SlotCustomer)
			}
		case SlotAmount:
			if raw.AmountMinor == nil || *raw.AmountMinor <= 0 {
				missing = append(missing, SlotAmount)
			}
		case SlotDate:
			// parseDueDate (validate.go) is the same parser Validate
			// itself uses — a malformed date is treated as no date at
			// all, consistently, rather than slot-filling accepting
			// something execution would then silently drop.
			if parseDueDate(raw.DueDateISO) == nil {
				missing = append(missing, SlotDate)
			}
		}
	}
	return missing
}

// identityMustBeExplicit reports whether raw is missing some *other*
// required field besides customer identity — see missingSlotFields'
// own doc comment for why that's the deciding factor.
func identityMustBeExplicit(raw RawIntent) bool {
	switch raw.Intent {
	case IntentCreateDebt, IntentRecordPayment:
		return raw.AmountMinor == nil || *raw.AmountMinor <= 0
	case IntentCreateReminder:
		return parseDueDate(raw.DueDateISO) == nil
	default:
		return false
	}
}

// looksLikeAmount is the amount slot's own "obviously doesn't match
// what was asked" check (edge case #3), generalizing onboarding.go's
// looksLikeName to a different field: an amount reply always has at
// least one digit ("5k", "₦5,000", "5000"); text with none clearly
// isn't attempting to answer an amount question.
func looksLikeAmount(text string) bool {
	for _, r := range text {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// isReadOnlyFinancialIntent is the set edge cases #2 and #5 both use
// for "a genuinely separate request, safe to also answer without
// touching anything" — deliberately excludes CREATE_DEBT/RECORD_PAYMENT:
// auto-executing a *second* mutation discovered mid-slot-fill is
// exactly the kind of guess spec §11 forbids and edge case #7 puts out
// of scope, and it would also need its own slot-filling, which this
// path doesn't (re-)enter.
func isReadOnlyFinancialIntent(intent IntentType) bool {
	switch intent {
	case IntentListCustomers, IntentListOutstandingDebts, IntentGetTotalOutstanding,
		IntentGetPaymentSummary, IntentGetCustomerBalance:
		return true
	default:
		return false
	}
}

// beginSlotFill parks intent pending its first missing field, exactly
// like beginConfirmation/beginDisambiguation — no new infrastructure,
// just a new trigger condition and PendingKind for the same Redis-
// backed pending-state system.
func (p *Processor) beginSlotFill(ctx context.Context, userID int64, raw RawIntent, missing []SlotField) (Reply, error) {
	if err := SetPendingAction(ctx, p.cfg.Redis, userID, PendingAction{
		Kind:   PendingSlotFill,
		Intent: raw,
	}, DefaultPendingTTL); err != nil {
		return Reply{}, err
	}
	return p.slotFillQuestion(raw, missing[0]), nil
}

// slotFillCancelButton is docs/BRIEF-polish-and-hardening.md #3's single
// addition to the slot-filling flow: the question itself stays free
// text (the answer space is genuinely open), but a Cancel button rides
// alongside it so backing out is one tap instead of typing "cancel" or a
// language-specific equivalent. Reuses buttonCancel's id so a tap lands
// on exactly the same "cancelled" acknowledgment the free-text path
// already gives (see handleSlotFillButtonReply) — buttons are additive,
// never a replacement for the existing free-text cancel phrases.
func slotFillCancelButton() []Button {
	return []Button{{ID: buttonCancel, Title: "Cancel"}}
}

func (p *Processor) slotFillQuestion(raw RawIntent, field SlotField) Reply {
	switch field {
	case SlotAmount:
		if name := trimOrEmpty(raw.CustomerName); name != "" {
			return Reply{Text: fmt.Sprintf(fixedText(slotFillAmountWithNameText, raw.Language), name), Buttons: slotFillCancelButton()}
		}
		return Reply{Text: fixedText(slotFillAmountText, raw.Language), Buttons: slotFillCancelButton()}
	case SlotDate:
		if name := trimOrEmpty(raw.CustomerName); name != "" {
			return Reply{Text: fmt.Sprintf(fixedText(slotFillDateWithNameText, raw.Language), name), Buttons: slotFillCancelButton()}
		}
		return Reply{Text: fixedText(slotFillDateText, raw.Language), Buttons: slotFillCancelButton()}
	default: // SlotCustomer
		return Reply{Text: fixedText(slotFillCustomerText, raw.Language), Buttons: slotFillCancelButton()}
	}
}

// slotFillReask picks the reask text for field, alternating between two
// distinct phrasings by reaskCount so a third, fifth, ... consecutive
// failed attempt never repeats the immediately-previous message
// verbatim (docs/BRIEF-research-hardening-standard.md Part 3:
// "repetitive identical fallback messages" is a named failure mode).
// reaskCount is how many times this exact field has already been
// re-asked — 0 on the first failure (uses the original reask text), 1+
// alternates into the narrowed "again" variant, then back, so no two
// consecutive re-asks for the same field are ever identical.
func (p *Processor) slotFillReask(raw RawIntent, field SlotField, reaskCount int) Reply {
	narrowed := reaskCount%2 == 1
	switch field {
	case SlotAmount:
		text := fixedText(slotFillAmountReaskText, raw.Language)
		if narrowed {
			text = fixedText(slotFillAmountReaskAgainText, raw.Language)
		}
		return Reply{Text: text, Buttons: slotFillCancelButton()}
	case SlotDate:
		text := fixedText(slotFillDateReaskText, raw.Language)
		if narrowed {
			text = fixedText(slotFillDateReaskAgainText, raw.Language)
		}
		return Reply{Text: text, Buttons: slotFillCancelButton()}
	default: // SlotCustomer
		text := fixedText(slotFillCustomerReaskText, raw.Language)
		if narrowed {
			text = fixedText(slotFillCustomerReaskAgainText, raw.Language)
		}
		return Reply{Text: text, Buttons: slotFillCancelButton()}
	}
}

// handleSlotFillButtonReply handles a tap on slotFillCancelButton — the
// only button ever attached during slot-filling. An unrecognized id
// (shouldn't happen: Ruby only ever sends this one button here) re-asks
// the current question rather than silently dropping it, the same
// pattern as every other pending kind's default case.
func (p *Processor) handleSlotFillButtonReply(ctx context.Context, msg InboundMessage, pending PendingAction, id string) (Reply, Language, error) {
	lang := pending.Intent.Language

	if id != buttonCancel {
		hint, err := p.contextHint(ctx, msg.UserID)
		if err != nil {
			return Reply{}, lang, err
		}
		missing := missingSlotFields(pending.Intent, hint, true)
		if len(missing) == 0 {
			reply, err := p.validateAndExecute(ctx, msg, pending.Intent)
			return p.continueQueue(ctx, msg, reply, lang, err, pending.Queue)
		}
		return p.slotFillQuestion(pending.Intent, missing[0]), lang, nil
	}

	if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
		p.logf("failed to clear pending action", "error", err)
	}
	return p.continueQueue(ctx, msg, textReply(fixedText(cancelledText, lang)), lang, nil, pending.Queue)
}

// handleSlotFillReply is the interactive slot-filling section's core
// logic: collect the missing field from this reply, without guessing
// and without ever calling the backend until the intent is complete.
// Only a clean, deterministic answer (a name-shaped customer reply)
// skips the AI call; an amount always needs the extractor's own
// amount-parsing ("5k" -> 500000), and anything that doesn't look like
// a clean answer needs the extractor's understanding to tell edge case
// #2 (fills the slot AND adds something new), #3 (doesn't answer at
// all), #5 (a genuinely different, complete request), and #6 (a
// correction) apart.
func (p *Processor) handleSlotFillReply(ctx context.Context, msg InboundMessage, pending PendingAction) (Reply, Language, error) {
	lang := pending.Intent.Language

	hint, err := p.contextHint(ctx, msg.UserID)
	if err != nil {
		return Reply{}, lang, err
	}
	missing := missingSlotFields(pending.Intent, hint, true)
	if len(missing) == 0 {
		// Shouldn't happen (a complete intent is never left pending) —
		// "should never happen" isn't "must crash": just execute it.
		if clearErr := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); clearErr != nil {
			p.logf("failed to clear pending action", "error", clearErr)
		}
		reply, err := p.validateAndExecute(ctx, msg, pending.Intent)
		return reply, lang, err
	}
	currentField := missing[0]

	text, err := p.rawText(ctx, msg)
	if err != nil {
		return Reply{}, lang, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return p.slotFillQuestion(pending.Intent, currentField), lang, nil
	}

	var filledName *string
	var filledAmount *int64
	var filledDate *string
	var secondary *RawIntent

	if currentField == SlotCustomer && looksLikeName(text) && !looksLikeAmount(text) {
		name := text
		filledName = &name
	} else {
		// The amount or date slot (both always need the extractor's own
		// parsing — "5k"/"tomorrow" aren't deterministically parseable
		// here) or a customer-slot reply that didn't look like a clean
		// name — understand it holistically.
		raw, err := p.cfg.Extractor.Extract(ctx, text)
		if err != nil {
			return Reply{}, lang, err
		}
		raw.Language = p.resolveStickyLanguage(ctx, msg.UserID, text, raw.Language)
		secondary = &raw
		// Check every field, not just currentField: edge case #6 is a
		// correction to whichever field was already given, which can
		// arrive while a *different* field is the one currently being
		// asked about ("Who is this for?" / "Chinedu" / "How much?" /
		// "actually it was Ngozi, not Chinedu" — that reply corrects
		// the customer while amount is what's pending).
		if n := trimOrEmpty(raw.CustomerName); n != "" {
			filledName = &n
		}
		if raw.AmountMinor != nil && *raw.AmountMinor > 0 {
			filledAmount = raw.AmountMinor
		}
		if d := trimOrEmpty(raw.DueDateISO); d != "" && parseDueDate(&d) != nil {
			filledDate = &d
		}
	}

	filled := filledName != nil || filledAmount != nil || filledDate != nil

	if !filled {
		if secondary != nil && isReadOnlyFinancialIntent(secondary.Intent) {
			// Edge case #5: a genuinely different, complete request —
			// handle it on its own terms. The original pending
			// question is left untouched: it re-asks naturally on the
			// trader's next reply, or lapses via the pending-state TTL.
			reply, err := p.validateAndExecute(ctx, msg, *secondary)
			return reply, secondary.Language, err
		}
		// Edge case #3: doesn't answer, and isn't a real request
		// either — re-ask, the same "obviously doesn't match what was
		// asked" pattern as name-capture, generalized to this field.
		// ReaskCount is tracked and persisted so a third, fifth, ...
		// consecutive failure alternates phrasing instead of repeating
		// the immediately-previous message (docs/BRIEF-research-
		// hardening-standard.md Part 3).
		nextReaskCount := pending.ReaskCount + 1
		if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{Kind: PendingSlotFill, Intent: pending.Intent, ReaskCount: nextReaskCount, Queue: pending.Queue}, DefaultPendingTTL); err != nil {
			return Reply{}, lang, err
		}
		return p.slotFillReask(pending.Intent, currentField, pending.ReaskCount), lang, nil
	}

	// Edge case #6 (a correction, "actually make that 7k") falls out
	// naturally here: filledAmount/filledName always *replaces* the
	// prior value on the pending intent, never appends alongside it.
	updated := pending.Intent
	if filledName != nil {
		updated.CustomerName = filledName
	}
	if filledAmount != nil {
		updated.AmountMinor = filledAmount
	}
	if filledDate != nil {
		updated.DueDateISO = filledDate
	}

	stillMissing := missingSlotFields(updated, hint, true)
	if len(stillMissing) > 0 {
		// Edge case #1: filled one field, still need another — ask for
		// the next one, never both at once.
		if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{Kind: PendingSlotFill, Intent: updated, Queue: pending.Queue}, DefaultPendingTTL); err != nil {
			return Reply{}, lang, err
		}
		return p.slotFillQuestion(updated, stillMissing[0]), lang, nil
	}

	// Complete — clear pending and finally reach the backend, exactly
	// once, only now that every required field is present.
	if err := ClearPendingAction(ctx, p.cfg.Redis, msg.UserID); err != nil {
		p.logf("failed to clear pending action", "error", err)
	}
	reply, err := p.validateAndExecute(ctx, msg, updated)
	if err != nil {
		return Reply{}, lang, err
	}

	if secondary != nil && isReadOnlyFinancialIntent(secondary.Intent) {
		// Edge case #2: the reply that completed the slot also carried
		// a separate request — answer it too, don't drop it.
		secondReply, err := p.validateAndExecute(ctx, msg, *secondary)
		if err == nil && secondReply.Text != "" {
			reply.Text += "\n\n" + secondReply.Text
		}
	}
	return p.continueQueue(ctx, msg, reply, lang, nil, pending.Queue)
}
