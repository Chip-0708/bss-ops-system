-- 000010_deprecation_impact：退役影响分析清单（04-models.md §10.1）
-- 分析接口写入并返回 snapshot_id；发起退役时校验存在且未过期。

CREATE TABLE deprecation_impact (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  snapshot_id     varchar(64) NOT NULL,
  sku_id          bigint      NOT NULL REFERENCES model_sku(id),
  -- 注意：原字段名 references 是 SQL 保留字（外键语法），PG 下会语法错误，故改名 impact_refs
  impact_refs     jsonb       NOT NULL,               -- 价目表 / 合同 / 客户报价明细
  reference_count integer     NOT NULL DEFAULT 0,
  replacements    jsonb       NULL,                   -- 推荐替代 SKU 列表
  expires_at      timestamptz NOT NULL,               -- 清单有效期（建议 24h）
  created_by      bigint      NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  request_id      varchar(64) NULL,
  updated_by      bigint      NULL,
  CONSTRAINT uk_deprecation_snapshot UNIQUE (snapshot_id)
);
CREATE INDEX idx_deprecation_sku ON deprecation_impact(sku_id, created_at DESC);
CREATE INDEX idx_deprecation_expire ON deprecation_impact(expires_at);
