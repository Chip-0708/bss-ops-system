-- customer_quote.quote_type（9a：APPLY / CLONE / TEMP / SPECIAL / CONTRACT）。
-- 契约 09-customer-quote.md §0.1 提到该列，但 000005 迁移遗漏——本迁移补齐。
-- 默认值 'APPLY'：既有行（若有）按套用标准价归类（最保守）。
ALTER TABLE customer_quote
  ADD COLUMN quote_type varchar(16) NOT NULL DEFAULT 'APPLY';

-- 枚举约束（红线 4：禁止自创状态值，字典由 CHECK 钉死）。
ALTER TABLE customer_quote
  ADD CONSTRAINT ck_customer_quote_type
  CHECK (quote_type IN ('APPLY', 'CLONE', 'TEMP', 'SPECIAL', 'CONTRACT'));

-- 常用查询模式：按客户 + 类型列报价（9b/9c 门户列表）。
CREATE INDEX idx_cq_customer_type ON customer_quote(customer_id, quote_type);
