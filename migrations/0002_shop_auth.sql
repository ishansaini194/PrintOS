-- 0002_shop_auth.sql — shop provisioning via a one-time setup code.
-- A shop is created with a setup_code; the agent exchanges it once for a token.
-- The cloud stores only the token's sha256 hash, never the raw token.
ALTER TABLE shops ADD COLUMN setup_code TEXT UNIQUE;

ALTER TABLE shops ADD COLUMN setup_code_used BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE shops ADD COLUMN token_hash TEXT;
