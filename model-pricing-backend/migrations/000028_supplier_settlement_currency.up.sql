-- Existing supplier records settle in CNY until finance explicitly changes them.
ALTER TABLE supplier_profile
  ADD COLUMN settlement_currency varchar(3) NOT NULL DEFAULT 'CNY',
  ADD CONSTRAINT ck_supplier_settlement_currency
    CHECK (settlement_currency IN ('CNY', 'USD'));
