-- 000019_procurement_m5_view：给 PROCUREMENT 补 M5:V。
--
-- 背景（6b-4 E2E 撞出来的设计缺口）：
--   设计 §8.7 原文「成本与比价类接口不做归属过滤（采购可见 ALL）」；
--   但 000007_seed 只给 PROCUREMENT 挂了 M5:E，没挂 M5:V —— 采购连成本基线列表都调不了
--   （6b-4 实测：buyer_a 调 GET /cost/baselines 返回 10003）。
--   写侧安全已由 pkg/perm.CanEditCostParam / CanLockPrimary 按角色 code 把关（§11-1），
--   补 M5:V 只开放「查看」，不扩大任何写权，与原有越权治理正交。
INSERT INTO role_permission (role_id, permission_point_id, created_at, updated_at)
SELECT r.id, pp.id, now(), now()
  FROM role r, permission_point pp
 WHERE r.code = 'PROCUREMENT'
   AND pp.module_code = 'M5' AND pp.action_code = 'V'
   AND NOT EXISTS (
       SELECT 1 FROM role_permission rp
        WHERE rp.role_id = r.id AND rp.permission_point_id = pp.id
   );
