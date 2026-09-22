-- 000006_flow_audit: 逐字照搬设计文档 §2.4.6（含 3 张系统表）；仅补 P0 缺口（request_id / created_by / updated_by / updated_at）。
-- 系统表 cache_version / cron_lock 无 request_id（纯内部机制）；sys_config 保留 DDL 定义，种子数据移入 000007。

CREATE TABLE sync_job (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  job_type    varchar(32) NOT NULL,                          -- SYNC_MODELS/SYNC_PRICES/SYNC_COMMUNITY
  source      varchar(32) NOT NULL,
  status      varchar(16) NOT NULL DEFAULT 'RUNNING',        -- RUNNING/SUCCESS/FAILED
  started_at  timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz NULL,
  error_msg   text NULL,
  item_count  integer NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  request_id  varchar(64) NULL,
  created_by  bigint      NULL,
  updated_by  bigint      NULL
);
CREATE INDEX idx_syncjob_type ON sync_job(job_type, started_at DESC);

CREATE TABLE staging_price (                                 -- 采集暂存区，绝不直接写生产
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  sync_job_id   bigint NOT NULL REFERENCES sync_job(id),
  sku_id        bigint NULL,
  raw_sku_code  varchar(128) NULL,
  currency      char(3) NULL,
  payload       jsonb NOT NULL,                              -- 逐组件原始采集值
  source        varchar(32) NOT NULL,
  confidence    varchar(8) NULL,
  match_status  varchar(16) NOT NULL DEFAULT 'UNMATCHED',    -- UNMATCHED/MATCHED/CONFLICT
  processed     boolean NOT NULL DEFAULT false,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  request_id    varchar(64) NULL,
  created_by    bigint      NULL,
  updated_by    bigint      NULL
);
CREATE INDEX idx_staging_job ON staging_price(sync_job_id, processed);

CREATE TABLE change_request (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  change_type   varchar(24) NOT NULL,                        -- NEW_MODEL/CAPABILITY/PRICE_DOWN/PRICE_UP/DEPRECATE
  sku_id        bigint NOT NULL REFERENCES model_sku(id),
  risk_level    varchar(8) NOT NULL,                         -- LOW/MID/HIGH
  payload       jsonb NOT NULL,
  status        varchar(24) NOT NULL DEFAULT 'PENDING',
  margin_preview jsonb NULL,
  confirm_required boolean NOT NULL DEFAULT false,           -- 多源不一致 → 强制人工确认
  created_by    varchar(24) NOT NULL DEFAULT 'SYNC_JOB',
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  request_id    varchar(64) NULL,
  updated_by    varchar(24) NULL
);
CREATE INDEX idx_cr_status ON change_request(status, created_at);

CREATE TABLE approval_step (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  biz_type      varchar(24) NOT NULL,                        -- CHANGE_REQUEST/PRICE_BOOK/SPECIAL_PRICE/RETRO_GRANT/DEPRECATE
  biz_id        bigint NOT NULL,
  step_no       integer NOT NULL,
  required_role varchar(32) NOT NULL,
  approver_id   bigint NULL,
  decision      varchar(16) NULL,
  comment       varchar(512) NULL,
  decided_at    timestamptz NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  request_id    varchar(64) NULL,
  created_by    bigint      NULL,
  updated_by    bigint      NULL,
  CONSTRAINT uk_step UNIQUE (biz_type, biz_id, step_no)
);
CREATE INDEX idx_appr_biz ON approval_step(biz_type, biz_id);

CREATE TABLE model_application (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  supplier_id   bigint NOT NULL REFERENCES supplier_profile(id),
  model_name    varchar(128) NOT NULL,
  vendor_id     bigint NULL,
  payload       jsonb NOT NULL,
  dup_top3      jsonb NULL,
  status        varchar(16) NOT NULL DEFAULT 'SUBMITTED',      -- SUBMITTED/MERGED/APPROVED/REJECTED
  merged_sku_id bigint NULL,
  reject_reason varchar(512) NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  request_id    varchar(64) NULL,
  created_by    bigint      NULL,
  updated_by    bigint      NULL
);
CREATE INDEX idx_ma_supplier ON model_application(supplier_id, status);

CREATE TABLE task_job (                                      -- 本地消息表
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  job_type    varchar(32) NOT NULL,                          -- COST_RECALC/NOTIFY/ACTIVATE_QUOTE/...
  payload     jsonb NOT NULL,
  status      varchar(16) NOT NULL DEFAULT 'PENDING',
  retry_count integer NOT NULL DEFAULT 0,
  next_run_at timestamptz NOT NULL DEFAULT now(),
  last_error  text NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  request_id  varchar(64) NULL,
  created_by  bigint      NULL,
  updated_by  bigint      NULL
);
CREATE INDEX idx_task_poll ON task_job(status, next_run_at);

CREATE TABLE event_outbox (                                  -- 对外事件出站（见 8.11）
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_type  varchar(48) NOT NULL,                          -- model.published/model.deprecated/price.effective/...
  payload     jsonb NOT NULL,
  status      varchar(16) NOT NULL DEFAULT 'PENDING',
  retry_count integer NOT NULL DEFAULT 0,
  next_run_at timestamptz NOT NULL DEFAULT now(),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  request_id  varchar(64) NULL,
  created_by  bigint      NULL,
  updated_by  bigint      NULL
);
CREATE INDEX idx_event_poll ON event_outbox(status, next_run_at);

CREATE TABLE idempotency_key (                               -- 幂等（见 2.3 与第 12 章）
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  biz_type     varchar(48)  NOT NULL,
  biz_key      varchar(255) NULL,                            -- 业务语义键（Agent 场景必需）
  request_id   varchar(64)  NOT NULL,
  request_hash char(64)     NOT NULL,
  status       varchar(16)  NOT NULL,                        -- PROCESSING/DONE/FAILED
  result_json  jsonb NULL,
  created_at   timestamptz  NOT NULL DEFAULT now(),
  updated_at   timestamptz  NOT NULL DEFAULT now(),
  expire_at    timestamptz  NOT NULL,
  created_by   bigint      NULL,
  updated_by   bigint      NULL,
  CONSTRAINT uk_idem UNIQUE (biz_type, request_id)
);
CREATE INDEX idx_idem_bizkey ON idempotency_key(biz_type, biz_key) WHERE biz_key IS NOT NULL;
CREATE INDEX idx_idem_expire ON idempotency_key(expire_at);

CREATE TABLE audit_log (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  operator_id   bigint NOT NULL,                             -- 系统任务填系统账号 0
  operator_role varchar(32) NOT NULL,
  action        varchar(48) NOT NULL,
  target_type   varchar(32) NOT NULL,
  target_id     bigint NOT NULL,
  before_value  jsonb NULL,
  after_value   jsonb NULL,
  reason        varchar(512) NULL,
  source_type   varchar(16) NOT NULL DEFAULT 'HUMAN',        -- HUMAN/CRON/WORKER/AGENT
  source_id     varchar(64) NULL,                            -- Agent：工作流 ID / 会话 ID
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  request_id    varchar(64) NULL,
  created_by    bigint      NULL,
  updated_by    bigint      NULL
);
CREATE INDEX idx_audit_target ON audit_log(target_type, target_id);
CREATE INDEX idx_audit_op_time ON audit_log(operator_id, created_at DESC);
CREATE INDEX idx_audit_source ON audit_log(source_type, source_id) WHERE source_id IS NOT NULL;

CREATE TABLE alert (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  alert_type    varchar(32) NOT NULL,
  severity      varchar(8)  NOT NULL,                        -- LOW/MID/HIGH/CRITICAL
  target_type   varchar(32) NULL,
  target_id     bigint NULL,
  message       varchar(512) NOT NULL,
  status        varchar(16) NOT NULL DEFAULT 'OPEN',           -- OPEN/HANDLING/RESOLVED/IGNORED
  assigned_role varchar(32) NULL,
  handle_note   varchar(512) NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  request_id    varchar(64) NULL,
  created_by    bigint      NULL,
  updated_by    bigint      NULL,
  resolved_at   timestamptz NULL
);
CREATE INDEX idx_alert_status ON alert(status, severity, created_at DESC);

CREATE TABLE todo_task (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  biz_type      varchar(32) NOT NULL,
  biz_id        bigint NOT NULL,
  assignee_id   bigint NULL,
  assignee_role varchar(32) NULL,
  title         varchar(256) NOT NULL,
  priority      varchar(8) NOT NULL DEFAULT 'MID',
  status        varchar(16) NOT NULL DEFAULT 'OPEN',
  due_at        timestamptz NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  request_id    varchar(64) NULL,
  created_by    bigint      NULL,
  updated_by    bigint      NULL
);
CREATE INDEX idx_todo_assignee ON todo_task(assignee_id, status);
CREATE INDEX idx_todo_role ON todo_task(assignee_role, status);

CREATE TABLE cache_version (
  cache_key  varchar(64) PRIMARY KEY,
  version    bigint      NOT NULL DEFAULT 1,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE cron_lock (
  job_name   varchar(64) NOT NULL,
  run_date   date        NOT NULL,
  locked_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (job_name, run_date)
);

CREATE TABLE sys_config (
  config_key   varchar(64) PRIMARY KEY,
  config_value text        NOT NULL,
  description  varchar(256) NULL,
  updated_at   timestamptz NOT NULL DEFAULT now()
);
-- 初始参数在 000007_seed 中集中插入，此处仅建表。
