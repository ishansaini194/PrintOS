-- 0003_hold_for_release.sql — hold-for-release v1.
-- Jobs now persist their print settings, page count and price so the agent push
-- can happen at payment time (not upload time), and the claim code typed at the
-- shop releases the held job.
--
-- Cloud-side job states: awaiting_payment → paid → held → printing → done | failed.
ALTER TABLE jobs ADD COLUMN type TEXT NOT NULL DEFAULT 'mono';

ALTER TABLE jobs ADD COLUMN copies INTEGER NOT NULL DEFAULT 1 CHECK (copies >= 1);

ALTER TABLE jobs ADD COLUMN pages INTEGER NOT NULL DEFAULT 1 CHECK (pages >= 1);

ALTER TABLE jobs ADD COLUMN amount_paise INTEGER NOT NULL DEFAULT 0 CHECK (amount_paise >= 0);

ALTER TABLE jobs ADD COLUMN pdf_sha256 TEXT NOT NULL DEFAULT '';

ALTER TABLE jobs ADD COLUMN duplex BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE jobs ADD COLUMN paper_size TEXT NOT NULL DEFAULT 'A4';

-- Release looks a job up by (shop, claim code).
CREATE INDEX idx_jobs_shop_claim ON jobs (shop_id, claim_code);
