-- 000005_pricing: 逐字照搬设计文档 §2.4.5；仅补 P0 缺口（request_id / created_by / updated_by / updated_at）。

CREATE TABLE pricing_policy (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  code          varchar(32) NOT NULL,
  name          varchar(64) NOT NULL,
  scope_type    varchar(16) NOT NULL,                        -- ALL/MODEL_TYPE/VENDOR/FAMILY/SKU
  scope_id      bigint NULL,
  level_code    varchar(16) NULL,                            -- 叠加客户等级；NULL=全部
  customer_id   bigint NULL,                                 -- 特定客户优先
  price_method  varchar(16) NOT NULL,                        -- MARGIN/COST_UP/OFFICIAL_ANCHOR/FIXED
  param_value   numeric(12,6) NOT NULL,                      -- 目标毛利率/加成率/锚定倍数/固定价
  rounding_rule varchar(16) NOT NULL DEFAULT 'CEIL',         -- 尾数向上取整
  priority      integer NOT NULL DEFAULT 100,                -- 越小越优先
  status        varchar(16) NOT NULL DEFAULT 'ACTIVE',
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  request_id    varchar(64) NULL,
  created_by    bigint      NULL,
  updated_by    bigint      NULL
);
CREATE INDEX idx_policy_scope ON pricing_policy(scope_type, scope_id, priority);

CREATE TABLE price_book (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  version_no     integer NOT NULL,
  level_code     varchar(16) NOT NULL,                       -- 客户等级
  status         varchar(24) NOT NULL DEFAULT 'DRAFT',
  effective_time timestamptz NULL,
  valid_to       timestamptz NULL,
  rollback_of    bigint NULL,
  diff_report    jsonb NULL,
  created_by     bigint NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  request_id     varchar(64) NULL,
  updated_by     bigint      NULL,
  CONSTRAINT uk_pb_ver UNIQUE (level_code, version_no)
);
CREATE UNIQUE INDEX uk_pb_effective ON price_book(level_code) WHERE status = 'EFFECTIVE';
CREATE INDEX idx_pb_status ON price_book(status, effective_time);

CREATE TABLE price_book_item (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  price_book_id    bigint NOT NULL REFERENCES price_book(id),
  sku_id           bigint NOT NULL REFERENCES model_sku(id),
  currency         char(3) NOT NULL,
  floor_price      numeric(20,8) NOT NULL,                   -- 完全成本 /(1-15%)，销售侧唯一下限
  policy_id        bigint NOT NULL REFERENCES pricing_policy(id),
  baseline_version integer NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  request_id       varchar(64) NULL,
  created_by       bigint      NULL,
  updated_by       bigint      NULL,
  CONSTRAINT uk_pbi UNIQUE (price_book_id, sku_id)
);
CREATE INDEX idx_pbi_sku ON price_book_item(sku_id, currency);

CREATE TABLE price_book_component (
  id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  price_book_item_id bigint NOT NULL REFERENCES price_book_item(id),
  component_type    varchar(32) NOT NULL,
  unit_price        numeric(20,8) NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  request_id        varchar(64) NULL,
  created_by        bigint      NULL,
  updated_by        bigint      NULL,
  CONSTRAINT uk_pbc UNIQUE (price_book_item_id, component_type)
);

CREATE TABLE customer_quote (
  id                      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  customer_id             bigint NOT NULL REFERENCES customer_profile(id),
  version_no              integer NOT NULL,
  status                  varchar(24) NOT NULL DEFAULT 'DRAFT',
  need_refresh            boolean NOT NULL DEFAULT false,
  refresh_diff            jsonb NULL,
  special_price_status    varchar(16) NULL,
  valid_until             timestamptz NULL,
  price_book_version      integer NOT NULL,
  owner_sales_operator_id bigint NOT NULL REFERENCES internal_staff(id),
  origin_owner_id         bigint NULL,                       -- 移交前的原归属（业绩核算用）
  created_by              bigint NOT NULL,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  request_id              varchar(64) NULL,
  updated_by              bigint      NULL,
  CONSTRAINT uk_cq_ver UNIQUE (customer_id, version_no)
);
CREATE UNIQUE INDEX uk_cq_formal ON customer_quote(customer_id) WHERE status = 'FORMAL';
CREATE INDEX idx_cq_owner ON customer_quote(owner_sales_operator_id);

CREATE TABLE customer_quote_item (
  id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  customer_quote_id bigint NOT NULL REFERENCES customer_quote(id),
  sku_id            bigint NOT NULL REFERENCES model_sku(id),
  currency          char(3) NOT NULL,
  unit_price        numeric(20,8) NOT NULL,                  -- 价格快照
  floor_price       numeric(20,8) NOT NULL,                  -- 当时 floor（快照）
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  request_id        varchar(64) NULL,
  created_by        bigint      NULL,
  updated_by        bigint      NULL,
  CONSTRAINT uk_cqi UNIQUE (customer_quote_id, sku_id)
);

CREATE TABLE customer_price_book (                           -- 客户专属价目表（合同价载体）
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  customer_id bigint NOT NULL REFERENCES customer_profile(id),
  sku_id      bigint NOT NULL REFERENCES model_sku(id),
  currency    char(3) NOT NULL,
  unit_price  numeric(20,8) NOT NULL,
  contract_from timestamptz NOT NULL,
  contract_to   timestamptz NOT NULL,                        -- 合同期内价格受保护
  source_quote_id bigint NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  request_id  varchar(64) NULL,
  created_by  bigint      NULL,
  updated_by  bigint      NULL,
  CONSTRAINT uk_cpb UNIQUE (customer_id, sku_id, contract_from)
);
CREATE INDEX idx_cpb_customer ON customer_price_book(customer_id, contract_to);
