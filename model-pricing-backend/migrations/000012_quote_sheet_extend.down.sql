ALTER TABLE quote_sheet DROP COLUMN IF EXISTS reject_reason;
ALTER TABLE quote_sheet DROP COLUMN IF EXISTS approved_by;
ALTER TABLE quote_sheet DROP COLUMN IF EXISTS approved_at;
ALTER TABLE quote_sheet DROP COLUMN IF EXISTS activated_at;
ALTER TABLE quote_sheet DROP COLUMN IF EXISTS remove_confirmed;
ALTER TABLE quote_sheet DROP COLUMN IF EXISTS grace_until;
