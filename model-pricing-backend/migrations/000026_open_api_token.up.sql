-- 11a 开放接口：open_api_token（短期令牌，与三大门户 login_session 完全隔离）
-- 设计文档 §8.0.2：开放接口独立鉴权，client_id/client_secret 存 sys_config，
-- 明文 token 仅下发一次，库中只存 sha256 hex。
CREATE TABLE IF NOT EXISTS open_api_token (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_hash  varchar(64)  NOT NULL UNIQUE,
    client_id   varchar(64)  NOT NULL,
    expires_at  timestamptz  NOT NULL,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    request_id  varchar(64)  NULL
);

-- 有效 token 快速检索（普通索引；部分索引谓词不能用 now()——非 IMMUTABLE）
CREATE INDEX IF NOT EXISTS idx_open_api_token_valid
    ON open_api_token (token_hash);
