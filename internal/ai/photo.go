package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chibuike-kt/ruby/internal/account"
)

// noTransactionsInPhotoText is the honest decline when vision genuinely
// couldn't make out any transaction — never a guess.
var noTransactionsInPhotoText = map[Language]string{
	LangEnglish: "I couldn't make out any transactions in that photo — could you tell me what happened instead?",
	LangPidgin:  "I no fit see any transaction for dat photo — you fit tell me wetin happen instead?",
	LangYoruba:  "Mi ò rí ìdúnadúrà kankan nínú fọ́tò yẹn — ṣé o lè sọ ohun tí ó ṣẹlẹ̀ fún mi dípò?",
	LangIgbo:    "Ahụghị m azụmahịa ọ bụla na foto ahụ — ị nwere ike ịgwa m ihe meere kama ya?",
	LangHausa:   "Ban iya ganin wata mu'amala a hoton ba — za ka iya gaya mini abin da ya faru maimakon?",
}

// photoQueueDroppedText is the fallback for a pending kind
// pendingCanQueue doesn't (yet) recognize — every kind photo processing
// can actually land in is covered as of docs/BRIEF-research-hardening-
// standard.md Part 5's live-testing fix #1, so this should never
// trigger in practice. Kept as the honest fail-safe if a future pending
// kind is added and continueQueue isn't wired into it: tell the trader
// plainly rather than silently dropping their transactions.
var photoQueueDroppedText = map[Language]string{
	LangEnglish: "I'll need you to send me the other %d transactions from that photo again once we sort this one out.",
	LangPidgin:  "I go need make you send the other %d transactions from dat photo again once we don sort dis one.",
	LangYoruba:  "Màá nílò kí o tún fi àwọn ìdúnadúrà %d yòókù tí ó wà nínú fọ́tò yẹn ránṣẹ́ nígbà tí a bá parí èyí.",
	LangIgbo:    "Aga m achọ ka ị zigharị azụmahịa %d ndị ọzọ si na foto ahụ ozugbo anyị dozichara nke a.",
	LangHausa:   "Zan bukaci ka sake tura sauran ma'amaloli %d daga wancan hoton da zarar mun warware wannan.",
}

// handleImage runs docs/BRIEF-research-hardening-standard.md Part 5 Tier
// 1's photo input: download -> vision extraction -> the exact same
// per-transaction pipeline text messages already use, one transaction at
// a time (see processExtractedIntents). p.cfg.Vision == nil (an older
// test double, or the feature simply not wired) falls back to the same
// friendly decline any other unsupported message type gets — never a
// crash.
func (p *Processor) handleImage(ctx context.Context, msg InboundMessage, acct account.Account) (Reply, Language, error) {
	if p.cfg.Vision == nil || msg.MediaID == nil {
		return textReply(fixedText(unsupportedMessageTypeText, LangEnglish)), LangEnglish, nil
	}

	data, mimeType, err := p.cfg.Media.DownloadMedia(ctx, *msg.MediaID)
	if err != nil {
		p.errf("photo pipeline failed", "stage", "download", "error", err)
		return Reply{}, LangEnglish, err
	}

	intents, err := p.cfg.Vision.ExtractFromImage(ctx, data, mimeType)
	if err != nil {
		p.errf("photo pipeline failed", "stage", "vision", "error", err)
		return Reply{}, LangEnglish, err
	}
	if len(intents) == 0 {
		return textReply(fixedText(noTransactionsInPhotoText, LangEnglish)), LangEnglish, nil
	}

	return p.processExtractedIntents(ctx, msg, acct, intents)
}

// pendingCanQueue reports whether kind's own resolution points know to
// call continueQueue. Every pending kind a photo transaction can land in
// is covered — repeated names are exactly the common case a real ledger
// photo hits (docs/BRIEF-research-hardening-standard.md Part 5 Tier 1
// live-testing finding #1): disambiguation and identity-confirmation are
// not rarer edge cases to leave unresumed, they're the normal shape of
// "which Chinedu" questions a multi-transaction photo triggers
// constantly.
func pendingCanQueue(kind PendingKind) bool {
	switch kind {
	case PendingConfirm, PendingSlotFill, PendingReminderOptIn, PendingAwaitingReminderPhone,
		PendingIdentityConfirm, PendingAwaitingCustomerSignal, PendingDisambiguateCustomer:
		return true
	default:
		return false
	}
}

// pendingTTL mirrors whatever TTL kind's own begin* function already
// uses — a queue attaching to an existing pending state (see
// processExtractedIntents) re-sets it and must not silently downgrade a
// deliberately longer TTL (PendingReminderOptIn's, see beginReminderOptIn)
// back to the standard conversational window.
func pendingTTL(kind PendingKind) time.Duration {
	if kind == PendingReminderOptIn {
		return ReminderOptInPendingTTL
	}
	return DefaultPendingTTL
}

// processExtractedIntents drives one or more freshly-extracted intents
// through processOneRawIntent — the exact same pipeline a single text
// message already uses, never a separate bulk-confirm flow (docs/BRIEF-
// research-hardening-standard.md Part 5 Tier 1). A complete,
// high-confidence, unambiguous intent executes immediately and the loop
// moves on; the first intent that genuinely needs the trader's input
// stops the loop and parks exactly as it would for a single message, and
// — only for the pending kinds pendingCanQueue recognizes — carries the
// rest along as Queue so continueQueue can resume them once this one
// resolves.
func (p *Processor) processExtractedIntents(ctx context.Context, msg InboundMessage, acct account.Account, intents []RawIntent) (Reply, Language, error) {
	var parts []string
	lang := LangEnglish

	for i, raw := range intents {
		reply, rLang, err := p.processOneRawIntent(ctx, msg, acct, raw)
		if err != nil {
			return Reply{}, rLang, err
		}
		lang = rLang
		if reply.Text != "" {
			parts = append(parts, reply.Text)
		}

		pendingNow, ok, pErr := GetPendingAction(ctx, p.cfg.Redis, msg.UserID)
		if pErr != nil {
			return Reply{}, lang, pErr
		}
		if !ok {
			// Executed cleanly, nothing left waiting on the trader —
			// move straight on to the next transaction in the photo.
			continue
		}

		rest := intents[i+1:]
		reply.Text = strings.Join(parts, "\n\n")
		if len(rest) > 0 {
			if pendingCanQueue(pendingNow.Kind) {
				pendingNow.Queue = rest
				// pendingTTL, not the blanket DefaultPendingTTL: a queue
				// attaching to PendingReminderOptIn must not downgrade
				// its own deliberately longer TTL back to the standard
				// conversational window (docs/BRIEF-research-hardening-
				// standard.md Part 5 live-testing finding #4).
				if setErr := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, pendingNow, pendingTTL(pendingNow.Kind)); setErr != nil {
					return Reply{}, lang, setErr
				}
			} else {
				reply.Text += "\n\n" + fmt.Sprintf(fixedText(photoQueueDroppedText, lang), len(rest))
			}
		}
		return reply, lang, nil
	}

	return textReply(strings.Join(parts, "\n\n")), lang, nil
}

// continueQueue resumes a photo's remaining transactions once whatever
// single transaction was blocking them has resolved — success or
// cancel alike, since declining one transaction from a photo must never
// discard the others (docs/BRIEF-research-hardening-standard.md Part 5
// Tier 1). queue is nil for every ordinary, non-photo pending state, in
// which case this is a plain passthrough — every resolution point in
// this package can call it unconditionally rather than branching on
// whether a queue exists.
func (p *Processor) continueQueue(ctx context.Context, msg InboundMessage, reply Reply, lang Language, err error, queue []RawIntent) (Reply, Language, error) {
	if err != nil || len(queue) == 0 {
		return reply, lang, err
	}

	acct, acctErr := account.GetByID(ctx, p.cfg.Pool, msg.UserID)
	if acctErr != nil {
		p.logf("photo queue continuation failed", "error", acctErr)
		return reply, lang, nil
	}

	rest, restLang, restErr := p.processExtractedIntents(ctx, msg, acct, queue)
	if restErr != nil {
		p.logf("photo queue continuation failed", "error", restErr)
		return reply, lang, nil
	}

	if rest.Text != "" {
		if reply.Text != "" {
			reply.Text += "\n\n" + rest.Text
		} else {
			reply.Text = rest.Text
		}
	}
	if len(rest.Buttons) > 0 {
		reply.Buttons = rest.Buttons
	}
	if rest.List != nil {
		reply.List = rest.List
	}
	return reply, restLang, nil
}
