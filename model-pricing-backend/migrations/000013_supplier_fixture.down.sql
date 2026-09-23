-- 000013_supplier_fixture.down：按 login_id / uscc / mobile 删除（不用 id）。

DELETE FROM account WHERE login_id IN ('supplier_a','supplier_b','buyer_a','buyer_b');
DELETE FROM role_grant WHERE staff_id IN (
  SELECT id FROM internal_staff WHERE mobile IN ('13800000011','13800000012')
);
DELETE FROM supplier_profile WHERE subject_id IN (
  SELECT id FROM legal_subject WHERE uscc IN ('91310000MA1FL0Q23X','91310000MA1FL0Q24Y')
);
DELETE FROM subject_operator WHERE mobile IN ('13800000001','13800000002');
DELETE FROM internal_staff WHERE mobile IN ('13800000011','13800000012');
DELETE FROM legal_subject WHERE uscc IN ('91310000MA1FL0Q23X','91310000MA1FL0Q24Y');
