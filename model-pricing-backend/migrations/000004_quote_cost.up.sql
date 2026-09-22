-- 000004_quote_cost: 逐字照搬设计文档 §2.4.4；仅补 P0 缺口（request_id / created_by / updated_by / updated_at）。

CREATE TABLE quote_sheet (                                   -- 供应商报价单：不可变版本
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  supplier_id   bigint NOT NULL REFERENCES supplier_profile(id),
  version_no    integer NOT NULL,
  status        varchar(24) NOT NULL DEFAULT 'DRAFT',
  valid_from    timestamptz NOT NULL,                        -- 生效时间（必填，可预约）
  valid_to      timestamptz NOT NULL,                        -- 有效期止
  source        varchar(16) NOT NULL DEFAULT 'MANUAL',       -- MANUAL/IMPORT/RETRO/SILENT_FOLLOW
  retroactive   boolean NOT NULL DEFAULT false,
  audit_reason  text NULL,
  change_request_id bigint NULL,                             -- 静默跟随的触发变更单
  submitted_by  bigint NULL,                                 -- 静默版本为 NULL（系统生成）
  submitted_at  timestamptz NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  request_id    varchar(64) NULL,
  created_by    bigint      NULL,
  updated_by    bigint      NULL,
  CONSTRAINT uk_quote_ver UNIQUE (supplier_id, version_no)
);
-- DB 层保证：同一供应商最多一条「已生效」、最多一条「已批准·待生效」
CREATE UNIQUE INDEX uk_quote_effective ON quote_sheet(supplier_id) WHERE status = 'EFFECTIVE';
CREATE UNIQUE INDEX uk_quote_pending   ON quote_sheet(supplier_id) WHERE status = 'APPROVED_PENDING';
CREATE INDEX idx_quote_status_from ON quote_sheet(status, valid_from);
CREATE INDEX idx_quote_expire      ON quote_sheet(status, valid_to);

CREATE TABLE quote_item (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  quote_sheet_id bigint NOT NULL REFERENCES quote_sheet(id),
  sku_id         bigint NOT NULL REFERENCES model_sku(id),
  currency       char(3) NOT NULL,                           -- 锁定 = 模型币种
  fx_tier        numeric(6,3) NULL,                          -- 汇率档位（仅 USD 模型）
  constraints_   jsonb NULL,                                 -- 非价格约束（RPM/TPM/配额/上下文/兼容度）
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  request_id     varchar(64) NULL,
  created_by     bigint      NULL,
  updated_by     bigint      NULL,
  CONSTRAINT uk_quote_item UNIQUE (quote_sheet_id, sku_id)
);
CREATE INDEX idx_qi_sku ON quote_item(sku_id);

CREATE TABLE quote_component (                               -- 逐组件报价（从 JSON 拆出）
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  quote_item_id  bigint NOT NULL REFERENCES quote_item(id),
  component_type varchar(32) NOT NULL,
  multiplier     numeric(12,6) NULL,                         -- 倍率模式：相对官方价
  unit_price     numeric(20,8) NOT NULL,                     -- 折算后单价（每百万 token）
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  request_id     varchar(64) NULL,
  created_by     bigint      NULL,
  updated_by     bigint      NULL,
  CONSTRAINT uk_qc UNIQUE (quote_item_id, component_type)
);
CREATE INDEX idx_qc_type ON quote_component(component_type, unit_price);  -- 比价/最低价

CREATE TABLE cost_baseline (                                 -- 成本基线：不可变版本（红线 2）
  id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  sku_id              bigint NOT NULL REFERENCES model_sku(id),
  version             integer NOT NULL,
  currency            char(3) NOT NULL,
  primary_supplier_id bigint NOT NULL REFERENCES supplier_profile(id),
  loss_rate           numeric(8,4) NOT NULL,
  channel_rate        numeric(8,4) NOT NULL,
  backup_sequence     jsonb NULL,
  calc_snapshot       jsonb NOT NULL,                        -- 报价版本/官方价版本/汇率/参数版本
  locked_manual       boolean NOT NULL DEFAULT false,
  change_reason       varchar(32) NOT NULL,                  -- 见下方枚举
  valid_from          timestamptz NOT NULL,
  valid_to            timestamptz NULL,
  is_current          boolean NOT NULL DEFAULT true,
  created_by          bigint NOT NULL,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  request_id          varchar(64) NULL,
  updated_by          bigint      NULL,
  CONSTRAINT uk_cost_ver UNIQUE (sku_id, version)
);
CREATE UNIQUE INDEX uk_cost_current ON cost_baseline(sku_id) WHERE is_current;
CREATE INDEX idx_cost_asof ON cost_baseline(sku_id, valid_from, valid_to);
-- 可选：区间不重叠（与 price_version 同法）
ALTER TABLE cost_baseline ADD COLUMN valid_range tstzrange
  GENERATED ALWAYS AS (tstzrange(valid_from, valid_to)) STORED;
ALTER TABLE cost_baseline ADD CONSTRAINT ex_cost_no_overlap
  EXCLUDE USING gist (sku_id WITH =, valid_range WITH &&);

CREATE TABLE cost_component (                                -- 成本逐组件（从 JSON 拆出）
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  cost_baseline_id bigint NOT NULL REFERENCES cost_baseline(id),
  component_type  varchar(32) NOT NULL,
  unit_cost       numeric(20,8) NOT NULL,                    -- 完全成本口径
  supplier_cost   numeric(20,8) NOT NULL,                    -- 供应商原始成本（溯源）
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  request_id      varchar(64) NULL,
  created_by      bigint      NULL,
  updated_by      bigint      NULL,
  CONSTRAINT uk_cc UNIQUE (cost_baseline_id, component_type)
);
CREATE INDEX idx_cc_type ON cost_component(component_type, unit_cost);

CREATE TABLE fx_rate_lock (                                  -- 月度汇率锁定
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  ym             char(7)     NOT NULL,                           -- '2026-09'
  fx_tier        numeric(6,3) NOT NULL,                          -- 8 档之一
  locked_by      bigint      NOT NULL,
  locked_at      timestamptz NOT NULL DEFAULT now(),
  adjust_reason  varchar(256) NULL,                            -- 月中调整：双人审批 + 理由
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  request_id     varchar(64) NULL,
  created_by     bigint      NULL,
  updated_by     bigint      NULL,
  CONSTRAINT uk_fx_ym UNIQUE (ym)
);