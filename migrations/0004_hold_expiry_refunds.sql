-- 0004_hold_expiry_refunds.sql — idempotent hold expiry stub refunds.

-- The sweeper looks for paid/held jobs past expires_at.
CREATE INDEX idx_jobs_expiry_sweep ON jobs (expires_at, state);

-- A hold-expiry refund should be recorded at most once for a payment.
CREATE UNIQUE INDEX idx_refunds_payment_reason ON refunds (payment_id, reason);
