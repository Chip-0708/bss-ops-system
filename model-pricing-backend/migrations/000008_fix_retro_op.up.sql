-- 000008_fix_retro_op：修正 RETRO_OP 角色越权（拍板版）。
--
-- 依据需求规格说明书 §3 角色表："成本补录（特权）…同采购域 + 补录特权"。
-- 1) RETRO_OP 不应是职能角色：is_functional=false, data_scope='SELF'。
--    宽域由其同时持有的职能角色（如 PROCUREMENT）提供，不由特权角色附带。
-- 2) 权限点收敛至 17 个 = PROCUREMENT 基准集合（V 系列 + M3 + M4 + M5:E）
--    + 补录特权 M5:P（设计文档 §3.4 操作点 P，即特权/补录，无需另设特权点）。
--    M4:A / M4:P 来自 PROCUREMENT 继承，Stage 2 权限中间件落地后再评估是否进一步收敛至 15。

-- 1. 角色属性修正
UPDATE role SET is_functional = false, data_scope = 'SELF', updated_at = now()
WHERE code = 'RETRO_OP';

-- 2. 删除目标集合之外的越权权限点
DELETE FROM role_permission rp
USING role r, permission_point p
WHERE rp.role_id = r.id
  AND rp.permission_point_id = p.id
  AND r.code = 'RETRO_OP'
  AND (p.module_code, p.action_code) NOT IN (
    ('M1','V'), ('M2','V'), ('M3','V'),
    ('M4','V'), ('M4','E'), ('M4','A'), ('M4','P'),
    ('M5','V'), ('M5','E'), ('M5','P'),
    ('M6','V'), ('M7','V'), ('M8','V'),
    ('M9','V'), ('M10','V'), ('M11','V'), ('M12','V')
  );

-- 3. 补齐目标集合内缺失的权限点（id 从 permission_point 查询，不硬编码）
INSERT INTO role_permission(role_id, permission_point_id, created_at, updated_at, request_id, created_by, updated_by)
SELECT r.id, p.id, now(), now(), NULL, NULL, NULL
FROM role r
JOIN permission_point p ON (p.module_code, p.action_code) IN (
    ('M1','V'), ('M2','V'), ('M3','V'),
    ('M4','V'), ('M4','E'), ('M4','A'), ('M4','P'),
    ('M5','V'), ('M5','E'), ('M5','P'),
    ('M6','V'), ('M7','V'), ('M8','V'),
    ('M9','V'), ('M10','V'), ('M11','V'), ('M12','V')
)
WHERE r.code = 'RETRO_OP'
ON CONFLICT (role_id, permission_point_id) DO NOTHING;
