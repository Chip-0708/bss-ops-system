CREATE TABLE legal_subject (
  id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subject_type        varchar(16) NOT NULL,                 -- COMPANY/INDIVIDUAL
  legal_name          varchar(128) NOT NULL,
  uscc                varchar(18) NULL,                     -- 公司：统一社会信用代码
  mobile              varchar(20) NULL,                     -- 个人/联系人手机号
  real_name           varchar(64) NULL,
  verification_status varchar(16) NOT NULL DEFAULT 'PENDING',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  request_id          varchar(64) NULL,
  created_by          bigint      NULL,
  updated_by          bigint      NULL
);
-- 类型作用域唯一：公司按 USCC，个人按手机号，互不干扰
CREATE UNIQUE INDEX uk_subject_uscc   ON legal_subject(uscc)   WHERE subject_type = 'COMPANY';
CREATE UNIQUE INDEX uk_subject_mobile ON legal_subject(mobile) WHERE subject_type = 'INDIVIDUAL';

CREATE TABLE subject_operator (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subject_id  bigint      NOT NULL REFERENCES legal_subject(id),
  name        varchar(64) NOT NULL,
  mobile      varchar(20) NOT NULL,
  is_primary  boolean     NOT NULL DEFAULT false,           -- 主操作员
  status      varchar(16) NOT NULL DEFAULT 'ACTIVE',
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  request_id  varchar(64) NULL,
  created_by  bigint      NULL,
  updated_by  bigint      NULL
);
CREATE UNIQUE INDEX uk_operator_mobile ON subject_operator(mobile);
CREATE INDEX idx_operator_subject ON subject_operator(subject_id);

CREATE TABLE supplier_profile (
  id                            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subject_id                    bigint NOT NULL REFERENCES legal_subject(id),
  channel_type                  varchar(24) NOT NULL,       -- 官方直连/官方转售/中转聚合/逆向
  settle_type                   varchar(16) NOT NULL DEFAULT 'PREPAID',  -- PREPAID/MONTHLY
  billing_cycle                 integer     NOT NULL DEFAULT 30,
  min_recharge                  numeric(20,2) NOT NULL DEFAULT 0,
  credit_line                   numeric(20,2) NOT NULL DEFAULT 0,
  credit_used                   numeric(20,2) NOT NULL DEFAULT 0,
  deposit_amount                numeric(20,2) NOT NULL DEFAULT 0,
  settle_status                 varchar(16) NOT NULL DEFAULT 'NORMAL',   -- NORMAL/WARNING/FROZEN
  qual_status                   varchar(16) NOT NULL DEFAULT 'VALID',    -- VALID/EXPIRING/FROZEN
  owner_procurement_operator_id bigint NOT NULL REFERENCES internal_staff(id),
  status                        varchar(16) NOT NULL DEFAULT 'ACTIVE',
  created_at                    timestamptz NOT NULL DEFAULT now(),
  updated_at                    timestamptz NOT NULL DEFAULT now(),
  request_id                    varchar(64) NULL,
  created_by                    bigint      NULL,
  updated_by                    bigint      NULL
);
CREATE UNIQUE INDEX uk_supplier_subject ON supplier_profile(subject_id);
CREATE INDEX idx_supplier_owner ON supplier_profile(owner_procurement_operator_id);  -- 行级过滤

CREATE TABLE customer_profile (
  id                      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subject_id              bigint NOT NULL REFERENCES legal_subject(id),
  level_code              varchar(16) NOT NULL DEFAULT 'BASIC',   -- FREE/BASIC/PRO/ENTERPRISE
  payment_type            varchar(16) NOT NULL DEFAULT 'PREPAID', -- PREPAID/MONTHLY/PAYG
  billing_cycle           integer     NOT NULL DEFAULT 30,
  credit_limit            numeric(20,2) NOT NULL DEFAULT 0,
  credit_used             numeric(20,2) NOT NULL DEFAULT 0,
  deposit_amount          numeric(20,2) NOT NULL DEFAULT 0,
  deposit_status          varchar(16) NOT NULL DEFAULT 'UNPAID',  -- UNPAID/PAID/FROZEN/REFUNDED
  credit_status           varchar(16) NOT NULL DEFAULT 'NORMAL',  -- NORMAL/WARNING/FROZEN
  invoice_type            varchar(16) NOT NULL DEFAULT 'SPECIAL',
  owner_sales_operator_id bigint NOT NULL REFERENCES internal_staff(id),
  status                  varchar(16) NOT NULL DEFAULT 'ACTIVE',
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  request_id              varchar(64) NULL,
  created_by              bigint      NULL,
  updated_by              bigint      NULL
);
CREATE UNIQUE INDEX uk_customer_subject ON customer_profile(subject_id);
CREATE INDEX idx_customer_owner ON customer_profile(owner_sales_operator_id);

CREATE TABLE credit_txn (                                    -- 授信流水 append-only
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  customer_id bigint NOT NULL REFERENCES customer_profile(id),
  direction   varchar(8)   NOT NULL,                         -- CONSUME/REPAY/ADJUST
  amount      numeric(20,2) NOT NULL,
  balance_after numeric(20,2) NOT NULL,
  ref_type    varchar(32) NULL,
  ref_id      bigint NULL,
  operator_id bigint NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  request_id  varchar(64) NULL,
  created_by  bigint      NULL,
  updated_by  bigint      NULL
);
CREATE INDEX idx_credit_cust ON credit_txn(customer_id, created_at);

CREATE TABLE deposit_txn (                                   -- 押金流水 append-only
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subject_side varchar(8) NOT NULL,                          -- CUSTOMER/SUPPLIER
  profile_id  bigint NOT NULL,
  direction   varchar(8)  NOT NULL,                          -- PAY/DEDUCT/REFUND
  amount      numeric(20,2) NOT NULL,
  balance_after numeric(20,2) NOT NULL,
  reason      varchar(256) NULL,
  operator_id bigint NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  request_id  varchar(64) NULL,
  created_by  bigint      NULL,
  updated_by  bigint      NULL
);
CREATE INDEX idx_deposit_profile ON deposit_txn(subject_side, profile_id, created_at);
