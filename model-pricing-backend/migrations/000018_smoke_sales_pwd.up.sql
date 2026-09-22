-- 000018_smoke_sales_pwd：重置 stage 2 手工种的 dev 冒烟账号 smoke_sales 的开发口令。
--
-- 背景：smoke_sales 是 stage 2 做 E2E 时手工种下的 INTERNAL 账号，口令已失考
--（不是 Test@1234）。000017_role_field_mask_fix 修正 SALES 的 field_mask 后，
-- 阶段 6b-4 必须用 smoke_sales 重登刷新 login_session.role_snapshot，
-- 否则字段剔除断言没有真库覆盖。
--
-- 密码来源：与 000013_supplier_fixture 的种子口令一致（Test@1234，bcrypt cost 4）。
-- 注意 bcrypt 每次加盐结果都不同，不能用「hash 是否等于 000013 那一份」判断是否同口令——
-- 同一个明文在不同会话算出的密文本来就不同，所以这里直接把整行密文覆盖。
UPDATE account
   SET password_hash = '$2a$04$uSa0sf/aTtc6iEhp3u3P8e9vemo2oVz8FxR4LyoEgI6PUrUb2wbpK',
       updated_at = now()
 WHERE login_id = 'smoke_sales' AND portal_type = 'INTERNAL';
