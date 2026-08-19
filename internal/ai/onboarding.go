package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/chibuike-kt/ruby/internal/account"
)

// handleNameCapture is Processor.process's entry point whenever the
// account has no name yet (docs/BRIEF-response-quality.md #1). It
// covers three cases: the very first encounter (no pending state yet),
// a reply to the name question, and a quick-action button tapped while
// a name is still pending.
func (p *Processor) handleNameCapture(ctx context.Context, msg InboundMessage) (Reply, Language, error) {
	pending, hasPending, err := GetPendingAction(ctx, p.cfg.Redis, msg.UserID)
	if err != nil {
		return Reply{}, LangEnglish, err
	}

	if !hasPending || pending.Kind != PendingAwaitingName {
		return p.beginNameCapture(ctx, msg)
	}

	if msg.Type == "interactive" {
		// A button tap is not a name, but it *is* a clear, valid intent
		// (plan step 4) — it replaces whatever original message was
		// pending, since a deliberate tap is a stronger signal than
		// whatever the trader's raw first message was.
		return p.nameReaskOrFallback(ctx, msg.UserID, pending.ReaskCount, originalMessageFrom(msg))
	}

	text, _, err := p.resolveText(ctx, msg)
	if err != nil {
		return Reply{}, LangEnglish, err
	}
	name := strings.TrimSpace(text)
	if looksLikeName(name) {
		return p.acceptName(ctx, msg.UserID, name, pending.OriginalMessage)
	}
	return p.nameReaskOrFallback(ctx, msg.UserID, pending.ReaskCount, pending.OriginalMessage)
}

// beginNameCapture is the very first encounter with a nameless account:
// store the original message verbatim and reply with the combined
// intro+question (plan step 2). Language is detected the same
// deterministic way the greeting short-circuit does — if the message
// doesn't match a known greeting phrase, English, since the trader's
// very first message is overwhelmingly "Hi Ruby" (plan's own framing)
// and there's no cheaper reliable signal for anything else.
func (p *Processor) beginNameCapture(ctx context.Context, msg InboundMessage) (Reply, Language, error) {
	lang := LangEnglish
	if msg.Type == "text" && msg.Text != nil {
		if l, ok := greetingLanguage(*msg.Text); ok {
			lang = l
		}
	}

	if err := SetPendingAction(ctx, p.cfg.Redis, msg.UserID, PendingAction{
		Kind:            PendingAwaitingName,
		OriginalMessage: originalMessageFrom(msg),
	}, DefaultPendingTTL); err != nil {
		return Reply{}, lang, err
	}

	return firstContactReply(lang), lang, nil
}

// nameReaskOrFallback implements plan step 5's loop bound: ask once
// more, and if that also doesn't look like a name, stop trying — save a
// placeholder and proceed, rather than looping forever.
func (p *Processor) nameReaskOrFallback(ctx context.Context, userID int64, priorReaskCount int, original *PendingOriginalMessage) (Reply, Language, error) {
	if priorReaskCount == 0 {
		if err := SetPendingAction(ctx, p.cfg.Redis, userID, PendingAction{
			Kind:            PendingAwaitingName,
			OriginalMessage: original,
			ReaskCount:      1,
		}, DefaultPendingTTL); err != nil {
			return Reply{}, LangEnglish, err
		}
		return textReply(nameReaskText), LangEnglish, nil
	}
	return p.acceptName(ctx, userID, namePlaceholder, original)
}

// acceptName saves the name (real or placeholder), clears the pending
// state, and — if the original message had real content beyond a bare
// greeting — processes it now through the normal pipeline, prefixed
// with the acknowledgment (plan step 6).
func (p *Processor) acceptName(ctx context.Context, userID int64, name string, original *PendingOriginalMessage) (Reply, Language, error) {
	name = truncateName(name)
	if err := account.SetName(ctx, p.cfg.Pool, userID, name); err != nil {
		return Reply{}, LangEnglish, err
	}
	if err := ClearPendingAction(ctx, p.cfg.Redis, userID); err != nil {
		p.logf("failed to clear pending action", "error", err)
	}

	ack := fmt.Sprintf(nameAcceptedText, name)

	if original.isBareGreeting() {
		return textReply(ack), LangEnglish, nil
	}

	reply, lang, err := p.process(ctx, original.toInboundMessage(userID), account.Account{ID: userID, Name: name})
	if err != nil {
		return Reply{}, LangEnglish, err
	}
	reply.Text = ack + "\n\n" + reply.Text
	return reply, lang, err
}
