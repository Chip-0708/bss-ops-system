-- 000027_admin_seed：补平台管理员账号 smoke_admin（INTERNAL 门户）。
--
-- 背景：000013 只固化了 buyer_a / buyer_b（PROCUREMENT）与 supplier_a / supplier_b，
-- internal 侧没有任何管理角色账号，导致全新环境部署后**无法使用后台管理功能**
-- （PLATFORM_ADMIN / MODEL_OPS / PRICING_OP 都没有登录入口）。
-- 这三个账号此前只存在于本地开发库里，是手工种下的，不在任何迁移内。
--
-- 密码：Test@1234（bcrypt cost 4），与 000013 / 000018 的种子口令一致。
-- 注意 bcrypt 每次加盐结果都不同，不能用「密文是否相同」判断是否同口令。
--
-- 幂等：account 上已有 INTERNAL/smoke_admin 就整体跳过，避免重复建人、重复授权。
-- 该账号三个角色（PLATFORM_ADMIN / MODEL_OPS / PRICING_OP）之间无 role_mutex 互斥。
-- 变量统一 v_ 前缀：避免与 role_grant.staff_id 等同名列冲突（PL/pgSQL 会报 ambiguous）。

DO $$
DECLARE
  v_org_id   bigint;
  v_staff_id bigint;
BEGIN
  -- 已种过（含本地手工种过的同名账号）则跳过，不制造第二行 staff
  IF EXISTS (SELECT 1 FROM account WHERE portal_type = 'INTERNAL' AND login_id = 'smoke_admin') THEN
    RAISE NOTICE '000027 跳过：INTERNAL/smoke_admin 已存在';
    RETURN;
  END IF;

  -- 组织：与 000013 兜底同一口径
  SELECT id INTO v_org_id FROM org_unit ORDER BY id LIMIT 1;
  IF v_org_id IS NULL THEN
    INSERT INTO org_unit(parent_id, name, unit_type, path, status, created_at, updated_at, request_id, created_by, updated_by)
    VALUES (NULL, '总部', 'COMPANY', '/', 'ACTIVE', now(), now(), NULL, 1, 1)
    RETURNING id INTO v_org_id;
    UPDATE org_unit SET path = '/' || v_org_id || '/' WHERE id = v_org_id;
  END IF;

  -- 员工
  INSERT INTO internal_staff(org_unit_id, name, mobile, status, created_at, updated_at, request_id, created_by, updated_by)
  VALUES (v_org_id, '平台管理员', '13800000000', 'ACTIVE', now(), now(), NULL, 1, 1)
  ON CONFLICT (mobile) DO NOTHING
  RETURNING id INTO v_staff_id;
  IF v_staff_id IS NULL THEN
    SELECT id INTO v_staff_id FROM internal_staff WHERE mobile = '13800000000';
  END IF;

  -- 账号
  INSERT INTO account(portal_type, owner_type, owner_id, login_id, password_hash, status, created_at, updated_at, request_id, created_by, updated_by)
  VALUES ('INTERNAL', 'STAFF', v_staff_id, 'smoke_admin',
          '$2a$04$uSa0sf/aTtc6iEhp3u3P8e9vemo2oVz8FxR4LyoEgI6PUrUb2wbpK',
          'ACTIVE', now(), now(), NULL, 1, 1);

  -- 角色授权（role_grant 无唯一约束，用 NOT EXISTS 保证幂等）
  INSERT INTO role_grant(staff_id, role_id, granted_by, granted_at, created_at, updated_at, request_id, created_by, updated_by)
  SELECT v_staff_id, r.id, 1, now(), now(), now(), NULL, 1, 1
    FROM role r
   WHERE r.code IN ('PLATFORM_ADMIN', 'MODEL_OPS', 'PRICING_OP')
     AND NOT EXISTS (
           SELECT 1 FROM role_grant g WHERE g.staff_id = v_staff_id AND g.role_id = r.id
         );
END $$;
