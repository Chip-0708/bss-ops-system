DROP INDEX IF EXISTS idx_cq_customer_type;
ALTER TABLE customer_quote DROP CONSTRAINT IF EXISTS ck_customer_quote_type;
ALTER TABLE customer_quote DROP COLUMN IF EXISTS quote_type;
