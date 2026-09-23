-- 000027_admin_seed.down：撤销 smoke_admin 的授权、账号与员工行。
-- 删除顺序：role_grant（外键指向 internal_staff）→ account → internal_staff。

DELETE FROM role_grant
 WHERE staff_id IN (SELECT id FROM internal_staff WHERE mobile = '13800000000');

DELETE FROM account
 WHERE portal_type = 'INTERNAL' AND login_id = 'smoke_admin';

DELETE FROM internal_staff
 WHERE mobile = '13800000000';
