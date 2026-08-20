package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/ruby/internal/customer"
)

// stickyLanguageTTL matches the conversational-context window the rest
// of Ruby already uses (docs/BRIEF-critical-fixes-and-reminders.md #2b)
// — refreshed on every message, so an active conversation never loses
// its established language mid-exchange, but a genuinely stale
// conversation doesn't hold onto a language forever either.
const stickyLanguageTTL = customer.DefaultLastCustomerContextTTL

func stickyLanguageKey(userID int64) string {
	return fmt.Sprintf("ruby:ctx:%d:language", userID)
}

// minWordsForLanguageSwitch is 2b's own "enough content to genuinely
// signal a change" bar — a bare name, an amount ("5k"), or a one-word
// "yes" must never flip an established conversation's language on
// their own, since a per-message language guess on that little text is
// unreliable by nature, not a real signal.
const minWordsForLanguageSwitch = 3

func looksLikeLanguageSignal(text string) bool {
	return len(strings.Fields(strings.TrimSpace(text))) >= minWordsForLanguageSwitch
}

// resolveStickyLanguage is docs/BRIEF-critical-fixes-and-reminders.md
// #2b's core logic. detected is whatever this message's own extraction/
// transcription reported; text is the trader's raw message, used only
// to judge whether it carries enough content to be a genuine language
// signal, never re-analyzed for anything else here.
//
// No established language yet, or this message has enough content to
// genuinely signal one: trust detected and (re-)establish it. A short/
// ambiguous message with a language already established: keep the
// established one regardless of what this message's own (unreliable,
// on so little text) detection guessed, and just refresh its TTL.
func (p *Processor) resolveStickyLanguage(ctx context.Context, userID int64, text string, detected Language) Language {
	sticky, ok, err := getStickyLanguage(ctx, p.cfg.Redis, userID)
	if err != nil {
		p.logf("failed to read sticky language, falling back to per-message detection", "error", err)
		return detected
	}

	if !ok || looksLikeLanguageSignal(text) {
		if setErr := setStickyLanguage(ctx, p.cfg.Redis, userID, detected); setErr != nil {
			p.logf("failed to set sticky language", "error", setErr)
		}
		return detected
	}

	if setErr := setStickyLanguage(ctx, p.cfg.Redis, userID, sticky); setErr != nil {
		p.logf("failed to refresh sticky language TTL", "error", setErr)
	}
	return sticky
}

func getStickyLanguage(ctx context.Context, rdb *redis.Client, userID int64) (Language, bool, error) {
	val, err := rdb.Get(ctx, stickyLanguageKey(userID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return Language(val), true, nil
}

func setStickyLanguage(ctx context.Context, rdb *redis.Client, userID int64, lang Language) error {
	return rdb.Set(ctx, stickyLanguageKey(userID), string(lang), stickyLanguageTTL).Err()
}
