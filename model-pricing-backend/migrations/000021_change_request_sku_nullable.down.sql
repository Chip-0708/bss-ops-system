-- 000021 down：恢复 NOT NULL。
-- 注意：若已有 sku_id IS NULL 的行（8b-1 发布/回滚产生的），本迁移会失败——
-- 需先手工处理这些行（回填或删除）再执行 down。
ALTER TABLE change_request ALTER COLUMN sku_id SET NOT NULL;
