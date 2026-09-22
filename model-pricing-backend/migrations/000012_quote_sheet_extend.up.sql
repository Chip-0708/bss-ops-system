-- 000012_quote_sheet_extend：报价单扩展列（05-quotes.md §11.1，逐字执行 6 条 ALTER）。

ALTER TABLE quote_sheet ADD COLUMN reject_reason   text NULL;
ALTER TABLE quote_sheet ADD COLUMN approved_by     bigint NULL;
ALTER TABLE quote_sheet ADD COLUMN approved_at     timestamptz NULL;
ALTER TABLE quote_sheet ADD COLUMN activated_at    timestamptz NULL;
ALTER TABLE quote_sheet ADD COLUMN remove_confirmed boolean NOT NULL DEFAULT false;
ALTER TABLE quote_sheet ADD COLUMN grace_until     timestamptz NULL;
