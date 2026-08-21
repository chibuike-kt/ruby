-- Proactive weekly digest (docs/BRIEF-research-hardening-standard.md
-- Part 5 Tier 1): a single per-trader timestamp is enough to know
-- whether a week has passed since the last one — no new reminders-style
-- state machine, since a digest is a coarse weekly checkpoint, not a
-- scheduled send with its own retry/failure history.
ALTER TABLE users ADD COLUMN last_digest_sent_at TIMESTAMPTZ;
