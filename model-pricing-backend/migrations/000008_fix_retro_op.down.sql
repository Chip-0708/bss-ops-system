-- 000008_fix_retro_op.down：将 RETRO_OP 完整还原至 000007 种子后的越权状态
-- （is_functional=true, data_scope='ALL', 26 个权限点 = E/P/A×(M1/M2/M4/M5/M6/M7/M8) + V×(M3/M9/M10/M11/M12)）。

-- 1. 先恢复角色属性
UPDATE role SET is_functional = true, data_scope = 'ALL', updated_at = now()
WHERE code = 'RETRO_OP';

-- 2. 插入修正前的原始权限点集合（26 个）
INSERT INTO role_permission(role_id, permission_point_id, created_at, updated_at, request_id, created_by, updated_by)
SELECT r.id, p.id, now(), now(), NULL, NULL, NULL
FROM role r
JOIN permission_point p ON (p.module_code, p.action_code) IN (
    ('M1','E'), ('M1','P'), ('M1','A'),
    ('M2','E'), ('M2','P'), ('M2','A'),
    ('M3','V'),
    ('M4','E'), ('M4','P'), ('M4','A'),
    ('M5','E'), ('M5','P'), ('M5','A'),
    ('M6','E'), ('M6','P'), ('M6','A'),
    ('M7','E'), ('M7','P'), ('M7','A'),
    ('M8','E'), ('M8','P'), ('M8','A'),
    ('M9','V'), ('M10','V'), ('M11','V'), ('M12','V')
)
WHERE r.code = 'RETRO_OP'
ON CONFLICT (role_id, permission_point_id) DO NOTHING;

-- 3. 删除修正中新加入的 6 个查看点
DELETE FROM role_permission rp
USING role r, permission_point p
WHERE rp.role_id = r.id
  AND rp.permission_point_id = p.id
  AND r.code = 'RETRO_OP'
  AND (p.module_code, p.action_code) IN (
    ('M1','V'), ('M2','V'), ('M4','V'),
    ('M5','V'), ('M6','V'), ('M7','V'), ('M8','V')
  );
