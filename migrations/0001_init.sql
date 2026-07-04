-- 0001_init.sql — initial cloud schema (PostgreSQL)
-- Tables: shops, agents, jobs, payments, refunds, problem_reports, audit_logs.
CREATE TABLE
    shops (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
        name TEXT NOT NULL,
        monthly_fee INTEGER NOT NULL DEFAULT 0 CHECK (monthly_fee >= 0), -- paise
        is_active BOOLEAN NOT NULL DEFAULT TRUE,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now ()
    );

CREATE TABLE
    agents (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
        shop_id UUID NOT NULL REFERENCES shops (id) ON DELETE CASCADE,
        protocol_version TEXT NOT NULL,
        printer_status TEXT NOT NULL DEFAULT 'unknown',
        last_heartbeat TIMESTAMPTZ,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now ()
    );

CREATE TABLE
    jobs (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
        shop_id UUID NOT NULL REFERENCES shops (id) ON DELETE CASCADE,
        idempotency_key TEXT NOT NULL UNIQUE,
        mode TEXT NOT NULL DEFAULT 'print_now',
        state TEXT NOT NULL,
        claim_code TEXT NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now (),
        expires_at TIMESTAMPTZ NOT NULL
    );

CREATE TABLE
    payments (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
        job_id UUID NOT NULL UNIQUE REFERENCES jobs (id) ON DELETE CASCADE,
        amount_paise INTEGER NOT NULL CHECK (amount_paise >= 0),
        gateway_ref TEXT,
        status TEXT NOT NULL,
        paid_at TIMESTAMPTZ,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now ()
    );

CREATE TABLE
    refunds (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
        payment_id UUID NOT NULL REFERENCES payments (id) ON DELETE CASCADE,
        reason TEXT NOT NULL,
        amount_paise INTEGER NOT NULL CHECK (amount_paise >= 0),
        refunded_at TIMESTAMPTZ NOT NULL DEFAULT now (),
        created_at TIMESTAMPTZ NOT NULL DEFAULT now ()
    );

CREATE TABLE
    problem_reports (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
        job_id UUID NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
        claim_code TEXT NOT NULL,
        note TEXT,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now ()
    );

-- audit_logs — records money-affecting events (refunds, owner confirmations)
-- for accountability. before/after hold JSON snapshots of the changed row.
CREATE TABLE
    audit_logs (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
        action TEXT NOT NULL, -- e.g. 'refund', 'owner_confirm'
        entity_type TEXT NOT NULL, -- e.g. 'job', 'payment'
        entity_id UUID NOT NULL,
        before JSONB,
        after JSONB,
        reason TEXT,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now ()
    );

-- Helpful lookups
CREATE INDEX idx_jobs_shop_id ON jobs (shop_id);

CREATE INDEX idx_agents_shop_id ON agents (shop_id);

CREATE INDEX idx_payments_job_id ON payments (job_id);

CREATE INDEX idx_audit_entity ON audit_logs (entity_type, entity_id);

CREATE INDEX idx_audit_created ON audit_logs (created_at DESC);