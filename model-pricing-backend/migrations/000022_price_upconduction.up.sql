-- 000022_price_upconduction.up.sql：涨价传导决策队列表（08-pricing §5）。
--
-- 设计裁决（stage8b-2 提示词）：
--   裁决 1：表结构按提示词落——数值 numeric(20,8)、时间 timestamptz、状态字典
--     PENDING/FOLLOWED/NOT_FOLLOWED（与契约 §5 一致）。
--   裁决 4：margin_before/margin_after 落库（决策 5 的快照——决策时冻结毛利口径；
--     若实时算则"当时决策参考的毛利"丢失）。权限剔除走 SALES.field_mask 追加
--     margin_before/margin_after（见本迁移末尾 UPDATE）。
--   裁决 5：frozen_until 落库（7 天），超时自动 NOT_FOLLOWED 的 worker 留给阶段 F。

CREATE TABLE IF NOT EXISTS price_upconduction (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sku_id           bigint          NOT NULL,
    level_code       varchar(32)     NOT NULL,
    cost_before      numeric(20,8)   NOT NULL,
    cost_after       numeric(20,8)   NOT NULL,
    cost_delta_pct   numeric(12,6)   NOT NULL,
    price_current    numeric(20,8)   NOT NULL,
    price_suggested  numeric(20,8)   NOT NULL,
    floor_price      numeric(20,8)   NOT NULL,
    margin_before    numeric(12,6),
    margin_after     numeric(12,6),
    status           varchar(16)     NOT NULL DEFAULT 'PENDING',
    frozen_until     timestamptz,
    reason           text,
    decided_by       bigint,
    decided_at       timestamptz,
    override_price   numeric(20,8),
    created_at       timestamptz     NOT NULL DEFAULT now(),
    updated_at       timestamptz     NOT NULL DEFAULT now(),
    request_id       varchar(64),
    created_by       bigint,
    updated_by       bigint,
    CONSTRAINT ck_price_upconduction_status CHECK (status IN ('PENDING','FOLLOWED','NOT_FOLLOWED'))
);

CREATE INDEX IF NOT EXISTS idx_price_upconduction_status
    ON price_upconduction (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_price_upconduction_sku
    ON price_upconduction (sku_id, level_code);

-- 裁决 4 挂接：SALES 无成本权限，margin_before/margin_after 物理剔除。
-- 与 000017 同口径——精确 key 匹配，追加而非替换。
UPDATE role
SET field_mask = '{"hide":["unit_cost","unit_cost_basis","cost_min","cost_max","cost_weighted","supplier_cost","calc_snapshot","margin_before","margin_after"]}'::jsonb
WHERE code = 'SALES';
