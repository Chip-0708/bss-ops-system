DROP INDEX IF EXISTS idx_idem_deadline;
ALTER TABLE idempotency_key
  DROP COLUMN IF EXISTS processing_deadline;
