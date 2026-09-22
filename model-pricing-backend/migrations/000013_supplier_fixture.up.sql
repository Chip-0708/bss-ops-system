-- 000013_supplier_fixture：开发种子（供应商 ×2 + 采购员工 ×2 + 账号 ×4）。
-- 允许 ON CONFLICT DO NOTHING；用 DO $$ 变量承接子查询取 id，不硬编码。
-- 密码统一 Test@1234（bcrypt cost 4）。

DO $$
DECLARE
  org_id bigint;
  sub_a bigint;
  sub_b bigint;
  op_a bigint;
  op_b bigint;
  staff_a bigint;
  staff_b bigint;
BEGIN
  -- 组织（取第一个 org_unit）
  SELECT id INTO org_id FROM org_unit ORDER BY id LIMIT 1;
  IF org_id IS NULL THEN
    INSERT INTO org_unit(name, unit_type, path, status, updated_at, request_id, created_by, updated_by)
    VALUES ('开发组织','COMPANY','/dev/','ACTIVE',now(),NULL,1,1)
    RETURNING id INTO org_id;
  END IF;

  -- legal_subject ×2（公司主体，USCC 唯一）
  INSERT INTO legal_subject(subject_type, legal_name, uscc, mobile, verification_status, updated_at, request_id, created_by, updated_by)
  VALUES ('COMPANY','供应商甲','91310000MA1FL0Q23X','13800000001','VERIFIED',now(),NULL,1,1)
  ON CONFLICT (uscc) WHERE subject_type = 'COMPANY' DO NOTHING
  RETURNING id INTO sub_a;
  IF sub_a IS NULL THEN
    SELECT id INTO sub_a FROM legal_subject WHERE uscc='91310000MA1FL0Q23X' AND subject_type='COMPANY';
  END IF;

  INSERT INTO legal_subject(subject_type, legal_name, uscc, mobile, verification_status, updated_at, request_id, created_by, updated_by)
  VALUES ('COMPANY','供应商乙','91310000MA1FL0Q24Y','13800000002','VERIFIED',now(),NULL,1,1)
  ON CONFLICT (uscc) WHERE subject_type = 'COMPANY' DO NOTHING
  RETURNING id INTO sub_b;
  IF sub_b IS NULL THEN
    SELECT id INTO sub_b FROM legal_subject WHERE uscc='91310000MA1FL0Q24Y' AND subject_type='COMPANY';
  END IF;

  -- subject_operator ×2
  INSERT INTO subject_operator(subject_id, name, mobile, is_primary, status, updated_at, request_id, created_by, updated_by)
  VALUES (sub_a,'甲操作员','13800000001',true,'ACTIVE',now(),NULL,1,1)
  ON CONFLICT (mobile) DO NOTHING
  RETURNING id INTO op_a;
  IF op_a IS NULL THEN
    SELECT id INTO op_a FROM subject_operator WHERE mobile='13800000001';
  END IF;

  INSERT INTO subject_operator(subject_id, name, mobile, is_primary, status, updated_at, request_id, created_by, updated_by)
  VALUES (sub_b,'乙操作员','13800000002',true,'ACTIVE',now(),NULL,1,1)
  ON CONFLICT (mobile) DO NOTHING
  RETURNING id INTO op_b;
  IF op_b IS NULL THEN
    SELECT id INTO op_b FROM subject_operator WHERE mobile='13800000002';
  END IF;

  -- internal_staff ×2（采购员）
  INSERT INTO internal_staff(org_unit_id, name, mobile, status, updated_at, request_id, created_by, updated_by)
  VALUES (org_id,'采购员A','13800000011','ACTIVE',now(),NULL,1,1)
  ON CONFLICT (mobile) DO NOTHING
  RETURNING id INTO staff_a;
  IF staff_a IS NULL THEN
    SELECT id INTO staff_a FROM internal_staff WHERE mobile='13800000011';
  END IF;

  INSERT INTO internal_staff(org_unit_id, name, mobile, status, updated_at, request_id, created_by, updated_by)
  VALUES (org_id,'采购员B','13800000012','ACTIVE',now(),NULL,1,1)
  ON CONFLICT (mobile) DO NOTHING
  RETURNING id INTO staff_b;
  IF staff_b IS NULL THEN
    SELECT id INTO staff_b FROM internal_staff WHERE mobile='13800000012';
  END IF;

  -- supplier_profile ×2（归属采购 = 刚建的两个员工）
  INSERT INTO supplier_profile(subject_id, channel_type, owner_procurement_operator_id, settle_status, qual_status, status, updated_at, request_id, created_by, updated_by)
  VALUES (sub_a,'官方直连',staff_a,'NORMAL','VALID','ACTIVE',now(),NULL,1,1)
  ON CONFLICT (subject_id) DO NOTHING;

  INSERT INTO supplier_profile(subject_id, channel_type, owner_procurement_operator_id, settle_status, qual_status, status, updated_at, request_id, created_by, updated_by)
  VALUES (sub_b,'官方直连',staff_b,'NORMAL','VALID','ACTIVE',now(),NULL,1,1)
  ON CONFLICT (subject_id) DO NOTHING;

  -- role_grant ×2（PROCUREMENT）
  INSERT INTO role_grant(staff_id, role_id, granted_by, updated_at, request_id, created_by, updated_by)
  SELECT staff_a, id, 1, now(), NULL, 1, 1 FROM role WHERE code='PROCUREMENT'
  ON CONFLICT DO NOTHING;
  INSERT INTO role_grant(staff_id, role_id, granted_by, updated_at, request_id, created_by, updated_by)
  SELECT staff_b, id, 1, now(), NULL, 1, 1 FROM role WHERE code='PROCUREMENT'
  ON CONFLICT DO NOTHING;

  -- account ×4（portal 隔离）
  INSERT INTO account(portal_type, owner_type, owner_id, login_id, password_hash, status, updated_at, request_id, created_by, updated_by)
  VALUES ('SUPPLIER','OPERATOR',op_a,'supplier_a','$2a$04$uSa0sf/aTtc6iEhp3u3P8e9vemo2oVz8FxR4LyoEgI6PUrUb2wbpK','ACTIVE',now(),NULL,1,1)
  ON CONFLICT (portal_type, login_id) DO NOTHING;

  INSERT INTO account(portal_type, owner_type, owner_id, login_id, password_hash, status, updated_at, request_id, created_by, updated_by)
  VALUES ('SUPPLIER','OPERATOR',op_b,'supplier_b','$2a$04$uSa0sf/aTtc6iEhp3u3P8e9vemo2oVz8FxR4LyoEgI6PUrUb2wbpK','ACTIVE',now(),NULL,1,1)
  ON CONFLICT (portal_type, login_id) DO NOTHING;

  INSERT INTO account(portal_type, owner_type, owner_id, login_id, password_hash, status, updated_at, request_id, created_by, updated_by)
  VALUES ('INTERNAL','STAFF',staff_a,'buyer_a','$2a$04$uSa0sf/aTtc6iEhp3u3P8e9vemo2oVz8FxR4LyoEgI6PUrUb2wbpK','ACTIVE',now(),NULL,1,1)
  ON CONFLICT (portal_type, login_id) DO NOTHING;

  INSERT INTO account(portal_type, owner_type, owner_id, login_id, password_hash, status, updated_at, request_id, created_by, updated_by)
  VALUES ('INTERNAL','STAFF',staff_b,'buyer_b','$2a$04$uSa0sf/aTtc6iEhp3u3P8e9vemo2oVz8FxR4LyoEgI6PUrUb2wbpK','ACTIVE',now(),NULL,1,1)
  ON CONFLICT (portal_type, login_id) DO NOTHING;
END $$;
