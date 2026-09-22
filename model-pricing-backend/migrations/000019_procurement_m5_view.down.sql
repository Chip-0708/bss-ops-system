-- 000019_procurement_m5_view.down：撤销 PROCUREMENT 的 M5:V。
DELETE FROM role_permission rp
 USING role r, permission_point pp
 WHERE rp.role_id = r.id AND rp.permission_point_id = pp.id
   AND r.code = 'PROCUREMENT'
   AND pp.module_code = 'M5' AND pp.action_code = 'V';
