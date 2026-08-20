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
)

// requiredSlotFields is the source of truth for what's required per
// intent — due date and description are deliberately absent: they're
// optional, and 1a is explicit that Ruby must never ask for them.
// Order matters: identity before amount (edge case #1).
func requiredSlotFields(intent IntentType) []SlotField {
	switch intent {
	case IntentCreateDebt, IntentRecordPayment:
		return []SlotField{SlotCustomer, SlotAmount}
	default:
		return nil
	}
}

// missingSlotFields reports which required fields raw is still missing,
// in order. hint is consulted so a pronoun-resolvable customer (spec §8
// signal 4, docs/BRIEF-critical-fixes-and-reminders.md #2c) is never
// treated as "missing" just because raw.CustomerName is empty — slot-
// filling and pronoun resolution must never fight each other.
func missingSlotFields(raw RawIntent, hint ContextHint) []SlotField {
	var missing []SlotField
	for _, f := range requiredSlotFields(raw.Intent) {
		switch f {
		case SlotCustomer:
			if trimOrEmpty(raw.CustomerName) == "" && hint.ResolvedCustomerID == nil && hint.LastCustomerID == nil {
				missing = append(missing, SlotCustomer)
			}
		case SlotAmount:
			if raw.AmountMinor == nil || *raw.AmountMinor <= 0 {
				missing = append(missing, SlotAmount)
			}
		}
	}
	return missing
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

func (p *Processor) slotFillQuestion(raw RawIntent, field SlotField) Reply {
	switch field {
	case SlotAmount:
		if name := trimOrEmpty(raw.CustomerName); name != "" {
			return textReply(fmt.Sprintf(fixedText(slotFillAmountWithNameText, raw.Language), name))
		}
		return textReply(fixedText(slotFillAmountText, raw.Language))
	default: // SlotCustomer
		return textReply(fixedText(slotFillCustomerText, raw.Language))
	}
}

func (p *Processor) slotFillReask(raw RawIntent, field SlotField) Reply {
	switch field {
	case SlotAmount:
		return textReply(fixedText(slotFillAmountReaskText, raw.Language))
	default: // SlotCustomer
		return textReply(fixedText(slotFillCustomerReaskText, raw.Language))
	}
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
	missing := missingSlotFields(pending.Intent, hint)
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
	var secondary *RawIntent

	if currentField == SlotCustomer && looksLikeName(text) && !looksLikeAmount(text) {
		name := text
		filledName = &name
	} else {
		// Either the amount slot (always needs the extractor's own
		// parsing) or a customer-slot reply that didn't look like a
		// clean name — understand it holistically.
		raw, err := p.cfg.Extractor.Extract(ctx, text)
		if err != nil {
			return Reply{}, lang, err
		}
		raw.Language = p.resolveStickyLanguage(ctx, msg.UserID, text, raw.Language)
		secondary = &raw
		// Check both fields, not just currentField: edge case #6 is a
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
	}

	filled := filledName != nil || filledAmount != nil

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
		return p.slotFillReask(pending.Intent, currentField), lang, nil
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

	stillMissing := missingSlotFields(updated, hint)
	if len(stillMissing) > 0 {
		// Edge case #1: filled one field, still need another — ask for
		// the next one, never both at once.
		if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{Kind: PendingSlotFill, Intent: updated}, DefaultPendingTTL); err != nil {
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
	return reply, lang, nil
}
