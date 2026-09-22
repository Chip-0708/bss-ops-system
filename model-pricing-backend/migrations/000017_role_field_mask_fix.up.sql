-- 000017_role_field_mask_fix：修复阶段 2 空转的字段剔除。
--
-- 问题（2026-09-14 阻断 2 复核）：
--   000007_seed 给 SALES/SUPPLIER/CUSTOMER 配的 field_mask 是 {"hide":["cost","margin","baseline"]}，
--   但项目所有 DTO 的真实字段名是 unit_cost / cost_min / cost_weighted / supplier_cost /
--   floor_price / calc_snapshot / unit_cost_basis 等。
--   pkg/fieldmask/fieldmask.go 是**精确 key 匹配**（不是子串）：
--     if _, drop := mask[k]; drop { continue }
--   因此这三个角色的字段剔除从未真正生效（一直空转）。
--
-- 裁决（同日 阻断 3）：
--   SALES    保留 floor_price（销售 E13 校验下限，设计 §1177），剔 calc_snapshot 整块；
--   SUPPLIER / CUSTOMER  剔 floor_price + calc_snapshot。
-- 其余字段（unit_cost / unit_cost_basis / cost_min / cost_max / cost_weighted / supplier_cost）
-- 三个角色都剔。
--
-- 重要：login_session.role_snapshot 是登录时快照，本迁移生效后**旧 token 不会自动带新 mask**，
-- E2E 验证前必须重新登录刷新快照（不算手工改数）。
UPDATE role SET field_mask = '{"hide":["unit_cost","unit_cost_basis","cost_min","cost_max","cost_weighted","supplier_cost","calc_snapshot"]}'::jsonb
 WHERE code = 'SALES';

UPDATE role SET field_mask = '{"hide":["unit_cost","unit_cost_basis","cost_min","cost_max","cost_weighted","supplier_cost","floor_price","calc_snapshot"]}'::jsonb
 WHERE code IN ('SUPPLIER','CUSTOMER');
