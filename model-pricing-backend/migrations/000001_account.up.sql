CREATE TABLE org_unit (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  parent_id        bigint NULL REFERENCES org_unit(id),
  name             varchar(64)  NOT NULL,
  unit_type        varchar(16)  NOT NULL DEFAULT 'DEPT',   -- COMPANY/DEPT/TEAM
  leader_staff_id  bigint NULL,                            -- 负责人 → DEPT_SUB 覆盖
  path             varchar(512) NOT NULL,                  -- 物化路径 '/1/3/7/'
  status           varchar(16)  NOT NULL DEFAULT 'ACTIVE',
  created_at       timestamptz  NOT NULL DEFAULT now(),
  updated_at       timestamptz  NOT NULL DEFAULT now(),
  request_id       varchar(64)  NULL,
  created_by       bigint       NULL,
  updated_by       bigint       NULL
);
CREATE INDEX idx_org_parent ON org_unit(parent_id);
CREATE INDEX idx_org_path   ON org_unit(path);

CREATE TABLE internal_staff (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  org_unit_id bigint      NOT NULL REFERENCES org_unit(id),
  name        varchar(64) NOT NULL,
  mobile      varchar(20) NOT NULL,
  email       varchar(128) NULL,
  status      varchar(16) NOT NULL DEFAULT 'ACTIVE',        -- ACTIVE/SUSPENDED
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  request_id  varchar(64) NULL,
  created_by  bigint      NULL,
  updated_by  bigint      NULL
);
CREATE UNIQUE INDEX uk_staff_mobile ON internal_staff(mobile);
CREATE INDEX idx_staff_org ON internal_staff(org_unit_id);

CREATE TABLE account (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  portal_type   varchar(16) NOT NULL,                       -- INTERNAL/SUPPLIER/CUSTOMER
  owner_type    varchar(16) NOT NULL,                       -- STAFF/OPERATOR
  owner_id      bigint      NOT NULL,                       -- internal_staff.id 或 subject_operator.id
  login_id      varchar(64) NOT NULL,
  password_hash varchar(128) NULL,                          -- OTP-only 账号可为空
  wx_openid     varchar(64) NULL,                           -- 小程序绑定（客户/供应商端）
  status        varchar(16) NOT NULL DEFAULT 'ACTIVE',
  last_login_at timestamptz NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  request_id    varchar(64) NULL,
  created_by    bigint      NULL,
  updated_by    bigint      NULL,
  CONSTRAINT uk_portal_login UNIQUE (portal_type, login_id) -- 按门户隔离账号空间
);
CREATE UNIQUE INDEX uk_account_owner ON account(owner_type, owner_id);
CREATE INDEX idx_account_openid ON account(wx_openid) WHERE wx_openid IS NOT NULL;

CREATE TABLE login_session (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  account_id    bigint      NOT NULL REFERENCES account(id),
  token_hash    char(64)    NOT NULL,                       -- sha256(token)
  portal_type   varchar(16) NOT NULL,
  client_type   varchar(16) NOT NULL DEFAULT 'WEB',         -- WEB/H5/MP/OPEN
  operator_type varchar(16) NOT NULL,                       -- STAFF/SUPPLIER/CUSTOMER
  operator_id   bigint      NOT NULL,
  role_snapshot jsonb       NOT NULL,                       -- 权限包快照：角色/数据域/字段剔除
  expires_at    timestamptz NOT NULL,                       -- 12h 滑动续期
  revoked       boolean     NOT NULL DEFAULT false,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  request_id    varchar(64) NULL,
  created_by    bigint      NULL,
  updated_by    bigint      NULL
);
CREATE UNIQUE INDEX uk_session_token ON login_session(token_hash);
CREATE INDEX idx_session_expiry ON login_session(expires_at);
CREATE INDEX idx_session_operator ON login_session(operator_type, operator_id);

CREATE TABLE role (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  code       varchar(32) NOT NULL,                          -- PLATFORM_ADMIN/MODEL_OPS/PROCUREMENT/...
  name       varchar(64) NOT NULL,
  data_scope varchar(16) NOT NULL,                          -- SELF/DEPT/DEPT_SUB/ALL
  field_mask jsonb       NULL,                              -- {"hide":["cost","margin"]}
  is_functional boolean  NOT NULL DEFAULT false,            -- 职能角色：不受组织位置限制，直接 ALL
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  request_id   varchar(64) NULL,
  created_by   bigint      NULL,
  updated_by   bigint      NULL,
  CONSTRAINT uk_role_code UNIQUE (code)
);

CREATE TABLE permission_point (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  module_code varchar(8)  NOT NULL,                         -- M1..M12（见 3.4 对照表）
  action_code varchar(8)  NOT NULL,                         -- V/E/A/C/P
  name        varchar(64) NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  request_id  varchar(64) NULL,
  created_by  bigint      NULL,
  updated_by  bigint      NULL,
  CONSTRAINT uk_module_action UNIQUE (module_code, action_code)
);

CREATE TABLE role_permission (
  role_id             bigint NOT NULL REFERENCES role(id),
  permission_point_id bigint NOT NULL REFERENCES permission_point(id),
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  request_id          varchar(64) NULL,
  created_by          bigint      NULL,
  updated_by          bigint      NULL,
  PRIMARY KEY (role_id, permission_point_id)
);

CREATE TABLE role_grant (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  staff_id   bigint      NOT NULL REFERENCES internal_staff(id),
  role_id    bigint      NOT NULL REFERENCES role(id),
  granted_by bigint      NOT NULL,
  granted_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  request_id varchar(64) NULL,
  created_by bigint      NULL,
  updated_by bigint      NULL
);
CREATE INDEX idx_grant_staff ON role_grant(staff_id);

CREATE TABLE role_mutex (
  role_a varchar(32) NOT NULL,
  role_b varchar(32) NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  request_id varchar(64) NULL,
  created_by bigint      NULL,
  updated_by bigint      NULL,
  PRIMARY KEY (role_a, role_b)
);
