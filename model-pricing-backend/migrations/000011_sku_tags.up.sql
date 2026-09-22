-- 000011_sku_tags：批量打标签（04-models.md §6 ADD_TAG / REMOVE_TAG）的存储列。
-- model_sku 原表（000003）无 tags 存储，按拍板决议新增 jsonb 数组列。

ALTER TABLE model_sku
  ADD COLUMN tags jsonb NOT NULL DEFAULT '[]';

-- 标签包含查询（@> 操作符）加速
CREATE INDEX idx_sku_tags ON model_sku USING gin (tags);
