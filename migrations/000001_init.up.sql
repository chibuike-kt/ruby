-- Core financial entities. Amounts are stored as bigint minor units
-- (kobo for NGN) per spec §17 — never numeric/float for money.

CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    phone_number TEXT NOT NULL UNIQUE,
    business_name TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE customers (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    name TEXT NOT NULL,
    phone_number TEXT,
    alias TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Same-name customers under the same trader are expected (spec §7) —
-- no uniqueness constraint on (user_id, name). Phone number, when
-- present, should identify a customer more reliably than name.
CREATE INDEX idx_customers_user_id_name ON customers(user_id, name);
CREATE INDEX idx_customers_user_id_phone ON customers(user_id, phone_number);

CREATE TYPE debt_status AS ENUM ('OUTSTANDING', 'PARTIALLY_PAID', 'SETTLED');

CREATE TABLE debts (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    customer_id BIGINT NOT NULL REFERENCES customers(id),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL DEFAULT 'NGN',
    description TEXT,
    due_date DATE,
    status debt_status NOT NULL DEFAULT 'OUTSTANDING',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_debts_user_id_status ON debts(user_id, status);
CREATE INDEX idx_debts_customer_id ON debts(customer_id);

CREATE TABLE payments (
    id BIGSERIAL PRIMARY KEY,
    debt_id BIGINT NOT NULL REFERENCES debts(id),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL DEFAULT 'NGN',
    idempotency_key TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_payments_debt_id ON payments(debt_id);

-- Ledger is append-only by convention at the application layer; the
-- migration for restricting UPDATE/DELETE grants on this table lives
-- in a later migration once the app DB role is created.
CREATE TABLE ledger_entries (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    debt_id BIGINT REFERENCES debts(id),
    type TEXT NOT NULL,
    amount_minor BIGINT NOT NULL,
    currency TEXT NOT NULL DEFAULT 'NGN',
    reference TEXT,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ledger_entries_user_id ON ledger_entries(user_id);
CREATE INDEX idx_ledger_entries_debt_id ON ledger_entries(debt_id);

CREATE TYPE reminder_recipient_type AS ENUM ('TRADER', 'CUSTOMER');
CREATE TYPE reminder_status AS ENUM ('SCHEDULED', 'PROCESSING', 'SENT', 'FAILED', 'CANCELLED');

CREATE TABLE reminders (
    id BIGSERIAL PRIMARY KEY,
    debt_id BIGINT NOT NULL REFERENCES debts(id),
    recipient_type reminder_recipient_type NOT NULL,
    recipient_id BIGINT,
    scheduled_at TIMESTAMPTZ NOT NULL,
    status reminder_status NOT NULL DEFAULT 'SCHEDULED',
    attempts INT NOT NULL DEFAULT 0,
    template TEXT,
    provider_message_id TEXT,
    sent_at TIMESTAMPTZ,
    failure_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_reminders_debt_id ON reminders(debt_id);
CREATE INDEX idx_reminders_status_scheduled_at ON reminders(status, scheduled_at);

CREATE TABLE messages (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT REFERENCES users(id),
    provider_message_id TEXT NOT NULL,
    direction TEXT NOT NULL,
    message_type TEXT NOT NULL,
    content_reference TEXT,
    processing_status TEXT NOT NULL DEFAULT 'RECEIVED',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Enforces spec §15/§31: the same inbound WhatsApp event must never be
-- processed twice.
CREATE UNIQUE INDEX idx_messages_provider_message_id ON messages(provider_message_id);
