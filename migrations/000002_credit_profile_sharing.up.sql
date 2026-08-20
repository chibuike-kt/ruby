-- Wema Credit Profile API opt-in (docs/wema-integration.md). Off by
-- default: a trader's repayment history is never shared with a partner
-- until they explicitly opt in.
ALTER TABLE users ADD COLUMN credit_profile_sharing_enabled BOOLEAN NOT NULL DEFAULT false;
