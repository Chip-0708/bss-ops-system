ALTER TABLE supplier_profile
  DROP CONSTRAINT ck_supplier_settlement_currency,
  DROP COLUMN settlement_currency;
