-- 000018_smoke_sales_pwd.down：无安全方式还原 smoke_sales 原口令（bcrypt 不可逆且原文已失考）。
-- 这里选择置空并留警告——dev 环境可接受，生产环境不存在 smoke_sales（它是手工种子），
-- 即使误跑 down 也只是登录被锁，需要人工重设，比伪造一个旧 hash 更诚实。
UPDATE account
   SET password_hash = NULL,
       updated_at = now()
 WHERE login_id = 'smoke_sales' AND portal_type = 'INTERNAL';
