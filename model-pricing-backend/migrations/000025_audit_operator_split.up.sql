-- 000025_audit_operator_split.up.sql
-- 10c §7-1：audit_log.operator_id 命名空间撞号治理（拆成两列）。
--
-- 背景：internal_staff 与 subject_operator 都有 id=1，operator_id 撞号。
-- 裁决 1：选「拆成两列」（选项 A），operator_id 保留（冗余字段，向后兼容）。
-- 裁决 2：新列 + 回填逻辑（按 operator_role 分派）。

-- 1. 加两列（都允许 NULL）
ALTER TABLE audit_log
  ADD COLUMN internal_operator_id bigint NULL,
  ADD COLUMN subject_operator_id bigint NULL;

-- 2. 回填历史数据（按 operator_role 分派）
-- 内部角色（STAFF / PLATFORM_ADMIN / MODEL_OPS / PRICING_OP / PROCUREMENT / SALES / FINANCE / OPS_ADMIN / RETRO_OP / AUDIT_READONLY）
UPDATE audit_log SET internal_operator_id = operator_id
WHERE operator_role IN ('STAFF', 'PLATFORM_ADMIN', 'MODEL_OPS', 'PRICING_OP', 'PROCUREMENT', 'SALES', 'FINANCE', 'OPS_ADMIN', 'RETRO_OP', 'AUDIT_READONLY')
  AND operator_id IS NOT NULL;

-- 外部角色（CUSTOMER / SUPPLIER / INTERNAL）
UPDATE audit_log SET subject_operator_id = operator_id
WHERE operator_role IN ('CUSTOMER', 'SUPPLIER', 'INTERNAL')
  AND operator_id IS NOT NULL;

-- operator_id=0 / SYSTEM → 两列都 NULL（能表达"系统"）

-- 3. operator_id 保留（标注废弃）
COMMENT ON COLUMN audit_log.operator_id IS '已废弃：内部角色填 internal_operator_id；外部角色填 subject_operator_id；SYSTEM 用 operator_id=0';
COMMENT ON COLUMN audit_log.internal_operator_id IS '内部员工 ID（internal_staff.id），仅内部角色填充';
COMMENT ON COLUMN audit_log.subject_operator_id IS '外部主体 ID（customer_profile.id / supplier_profile.id），仅外部角色填充';
