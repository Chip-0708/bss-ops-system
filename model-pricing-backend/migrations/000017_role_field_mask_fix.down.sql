-- 000017 down：还原 000007_seed 的旧值（精确匹配旧语义，不做最小化）。
UPDATE role SET field_mask = '{"hide":["cost","margin","baseline"]}'::jsonb
 WHERE code IN ('SALES','SUPPLIER','CUSTOMER');
