CREATE EXTENSION btree_gist;
CREATE EXTENSION pg_trgm;

-- 以下 6 张表 DDL 严格按设计文档 2.4.3 逐字保留，仅补 P0 缺口（request_id / created_by / updated_by / updated_at）。
-- 对已有 created_by 的 price_version，不重复补列，统一放在 P0 缺口区。

CREATE TABLE vendor (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  code       varchar(32) NOT NULL,
  name       varchar(128) NOT NULL,
  country    varchar(32) NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  request_id varchar(64) NULL,
  created_by bigint      NULL,
  updated_by bigint      NULL,
  CONSTRAINT uk_vendor_code UNIQUE (code)
);

CREATE TABLE model_family (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  vendor_id  bigint NOT NULL REFERENCES vendor(id),
  name       varchar(128) NOT NULL,                          -- 如 GPT-5
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  request_id varchar(64) NULL,
  created_by bigint      NULL,
  updated_by bigint      NULL
);

CREATE UNIQUE INDEX uk_family ON model_family(vendor_id, name);

CREATE TABLE model_sku (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  vendor_id       bigint NOT NULL REFERENCES vendor(id),
  family_id       bigint NOT NULL REFERENCES model_family(id),
  sku_code        varchar(128) NOT NULL,                     -- gpt-5-2026-04-11
  model_type      varchar(24) NOT NULL,                      -- 对话/推理/多模态/向量/...
  native_currency char(3)   NOT NULL,                        -- USD/CNY
  context_window  integer   NULL,
  capability      jsonb     NULL,                            -- 能力参数（内联，原 MODEL_CAPABILITY 实体）
  verify_status   varchar(16) NOT NULL DEFAULT 'UNVERIFIED', -- UNVERIFIED/MANUAL/PROBED
  tier_tag        varchar(16) NULL,                          -- 旗舰/主力/经济/长尾
  is_sensitive    boolean   NOT NULL DEFAULT false,
  cross_border    boolean   NOT NULL DEFAULT false,
  lifecycle_status varchar(16) NOT NULL DEFAULT 'DRAFT',
  sunset_date     date      NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  request_id      varchar(64) NULL,
  created_by      bigint      NULL,
  updated_by      bigint      NULL,
  CONSTRAINT uk_sku_code UNIQUE (sku_code)
);
CREATE INDEX idx_sku_family ON model_sku(family_id);
CREATE INDEX idx_sku_status ON model_sku(lifecycle_status);
CREATE INDEX idx_sku_name_trgm ON model_sku USING gin (sku_code gin_trgm_ops);  -- 查重

CREATE TABLE model_alias (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  sku_id     bigint NOT NULL REFERENCES model_sku(id),
  alias      varchar(128) NOT NULL,
  source     varchar(16) NOT NULL DEFAULT 'MANUAL',          -- MANUAL/MERGE/IMPORT
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  request_id varchar(64) NULL,
  created_by bigint      NULL,
  updated_by bigint      NULL,
  CONSTRAINT uk_alias UNIQUE (alias)                         -- 一个别名只能指向一个 SKU
);
CREATE INDEX idx_alias_sku ON model_alias(sku_id);

CREATE TABLE price_version (                                 -- 官方价：不可变版本
  id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  sku_id            bigint NOT NULL REFERENCES model_sku(id),
  version_no        integer NOT NULL,
  currency          char(3) NOT NULL,
  tax_basis         varchar(16) NOT NULL,                    -- GROSS/NET
  effective_from    timestamptz NOT NULL,
  effective_to      timestamptz NULL,
  is_current        boolean NOT NULL DEFAULT true,
  source            varchar(24) NOT NULL,                    -- MANUAL/IMPORT/SYNC/SILENT_FOLLOW
  confidence        varchar(8) NULL,
  change_request_id bigint NULL,
  created_by        bigint NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  request_id        varchar(64) NULL,
  updated_by        bigint      NULL,
  CONSTRAINT uk_price_ver UNIQUE (sku_id, version_no)
);
CREATE UNIQUE INDEX uk_price_current ON price_version(sku_id) WHERE is_current;
CREATE INDEX idx_price_range ON price_version(sku_id, effective_from, effective_to);
-- DB 层保证：同一 SKU 的官方价版本区间不重叠
ALTER TABLE price_version ADD COLUMN eff_range tstzrange
  GENERATED ALWAYS AS (tstzrange(effective_from, effective_to)) STORED;
ALTER TABLE price_version ADD CONSTRAINT ex_price_no_overlap
  EXCLUDE USING gist (sku_id WITH =, eff_range WITH &&);

CREATE TABLE price_component (                               -- 逐组件计价
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  price_version_id bigint NOT NULL REFERENCES price_version(id),
  component_type   varchar(32) NOT NULL,                     -- input/output/cached_input/cache_write_5m/...
  unit_price       numeric(20,8) NOT NULL,                   -- 每百万 token 单价
  tier_rules       jsonb NULL,                               -- 阶梯规则（非价格，可 JSON）
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  request_id       varchar(64) NULL,
  created_by       bigint      NULL,
  updated_by       bigint      NULL,
  CONSTRAINT uk_pc UNIQUE (price_version_id, component_type)
);
CREATE INDEX idx_pc_type ON price_component(component_type);
