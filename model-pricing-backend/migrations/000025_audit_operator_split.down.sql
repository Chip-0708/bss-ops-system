-- 000025_audit_operator_split.down.sql
-- 回滚：删两列。

ALTER TABLE audit_log DROP COLUMN internal_operator_id;
ALTER TABLE audit_log DROP COLUMN subject_operator_id;
