-- 000016_cost_param：成本参数表（D1/D6 完全成本公式的可调参数，06-cost.md §7）。
--
-- 三级 scope，最具体者优先（解析顺序：SUPPLIER > MODEL > GLOBAL，06-cost §10-3）。
-- GLOBAL 用 scope_id=0 哨兵而不是 NULL：
--   UNIQUE(scope_type, scope_id) 在 scope_id 为 NULL 时失效（NULL 与任何值都不相等），
--   所以必须给 GLOBAL 一个确定的 0，并用 chk_cost_param_global_zero 兜底挡脏数据。
--
--tax_inclusive / withholding_tax 本阶段（6b）不参与计算（完全成本在模型币种内算、不含税项），
-- 先把列建好，避免 6c / 阶段 8 再加列时的迁移颠簸。
CREATE TABLE cost_param (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  scope_type      varchar(16)  NOT NULL,        -- GLOBAL / MODEL / SUPPLIER
  scope_id        bigint       NOT NULL,        -- GLOBAL 用 0 哨兵
  loss_rate       numeric(8,4) NOT NULL,        -- 损耗系数（默认 0.0300）
  channel_rate    numeric(8,4) NOT NULL,        -- 通道费率（默认 0.0100）
  tax_inclusive   boolean      NOT NULL DEFAULT true,
  withholding_tax numeric(8,4) NOT NULL DEFAULT 0,
  created_at      timestamptz  NOT NULL DEFAULT now(),
  updated_at      timestamptz  NOT NULL DEFAULT now(),
  created_by      bigint       NULL,
  updated_by      bigint       NULL,
  request_id      varchar(64)  NULL,
  CONSTRAINT uk_cost_param_scope UNIQUE (scope_type, scope_id),
  CONSTRAINT chk_cost_param_scope CHECK (scope_type IN ('GLOBAL','MODEL','SUPPLIER')),
  CONSTRAINT chk_cost_param_global_zero CHECK (scope_type <> 'GLOBAL' OR scope_id = 0)
);

-- 种子：GLOBAL 默认行（loss=0.03, channel=0.01，对应 6b 验收 #B4）
INSERT INTO cost_param(scope_type, scope_id, loss_rate, channel_rate, tax_inclusive, withholding_tax, created_by)
VALUES ('GLOBAL', 0, 0.0300, 0.0100, true, 0, 0);
