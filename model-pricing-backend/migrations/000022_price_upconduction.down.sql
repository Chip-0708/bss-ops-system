-- 000022_price_upconduction.down.sql
-- 回滚：还原 SALES.field_mask（回到 000017 的版本），再 drop 涨价传导表。

UPDATE role
SET field_mask = '{"hide":["unit_cost","unit_cost_basis","cost_min","cost_max","cost_weighted","supplier_cost","calc_snapshot"]}'::jsonb
WHERE code = 'SALES';

DROP INDEX IF EXISTS idx_price_upconduction_sku;
DROP INDEX IF EXISTS idx_price_upconduction_status;
DROP TABLE IF EXISTS price_upconduction;
-- 000022_price_upconduction.down.sql：回滚涨价传导决策队列表 + SALES mask 还原。

DROP TABLE IF EXISTS price_upconduction;

-- SALES mask 还原回 000017 的版本（不含 margin_before/margin_after）。
UPDATE role
SET field_mask = '{"hide":["unit_cost","unit_cost_basis","cost_min","cost_max","cost_weighted","supplier_cost","calc_snapshot"]}'::jsonb
WHERE code = 'SALES';
