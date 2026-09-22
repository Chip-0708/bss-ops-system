-- 000024: 客户门户通知表（9c §6 /notifications + /home 未读计数）
--
-- 设计依据（裁决 3）：
--   - 04-models.md 已说明「全库无通知表」——本批新建。
--   - 本批**不自动生成**通知（涨价/退役触发器待 10/11 阶段），E2E 用 fixture 造数。
--   - read_at NULL = 未读；标记已读接口（POST /notifications/{id}/read）登记 9c-② 遗留。
CREATE TABLE customer_notification (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  customer_id bigint NOT NULL REFERENCES customer_profile(id),
  type        varchar(32) NOT NULL,   -- PRICE_UP / DEPRECATE / SYSTEM
  title       varchar(128) NOT NULL,
  content     text NOT NULL,
  read_at     timestamptz NULL,       -- NULL = 未读
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  request_id  varchar(64) NULL,
  created_by  bigint NULL,
  updated_by  bigint NULL,
  CONSTRAINT ck_cn_type CHECK (type IN ('PRICE_UP','DEPRECATE','SYSTEM'))
);
-- 未读计数 / 按 created_at DESC 分页都走这条复合索引。
CREATE INDEX idx_cust_notify ON customer_notification(customer_id, read_at, created_at DESC);
