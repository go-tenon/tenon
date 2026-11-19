-- +goose Up
CREATE TABLE api_tokens (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    token_hash text NOT NULL,
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    tenant text NOT NULL,
    impersonator_type text NOT NULL,
    impersonator_id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('login', 'personal', 'oauth')),
    name text NOT NULL,
    scopes text NOT NULL,
    idle_timeout bigint NOT NULL,
    ends_at timestamptz,
    ip text NOT NULL,
    user_agent text NOT NULL,
    last_used_at timestamptz NOT NULL,
    expires_at timestamptz,
    client_id text NOT NULL,
    client_name text NOT NULL,
    resource text NOT NULL,
    auth_methods text NOT NULL,
    auth_time timestamptz,
    CONSTRAINT api_tokens_token_hash_unique UNIQUE (token_hash)
);
CREATE INDEX api_tokens_subject ON api_tokens (subject_type, subject_id);
CREATE INDEX api_tokens_expires_at ON api_tokens (expires_at) WHERE expires_at IS NOT NULL;
COMMENT ON TABLE api_tokens IS 'API token；只存 token 的 SHA-256；过期行由 apitoken.Store.Cleanup 删除';
COMMENT ON COLUMN api_tokens.kind IS 'login：登录时签发，按签发时的配置滑动过期；personal：个人访问令牌，过期时间由创建者指定；oauth：用户授权 OAuth 客户端后签发，只能用于 resource';
COMMENT ON COLUMN api_tokens.tenant IS 'token 绑定的租户；空字符串表示不绑定';
COMMENT ON COLUMN api_tokens.impersonator_type IS '代为登录时签发 token 的实际操作者，与 impersonator_id 一起引用；空字符串表示用户本人';
COMMENT ON COLUMN api_tokens.scopes IS '个人访问令牌和 OAuth 令牌的权限限制，空格分隔；登录 token 为空字符串，表示不限制';
COMMENT ON COLUMN api_tokens.idle_timeout IS '登录 token 和 OAuth 令牌签发时确定的空闲时长（秒），使用时据此滑动延长；0 表示不延长，个人访问令牌为 0';
COMMENT ON COLUMN api_tokens.ends_at IS '登录 token 和 OAuth 令牌签发时确定的最长期限（签发时间加 lifetime）；个人访问令牌为空';
COMMENT ON COLUMN api_tokens.expires_at IS '实际截止时间；login、oauth 在使用时滑动为使用时间加 idle_timeout，不超过 ends_at；personal 为空表示不过期';
COMMENT ON COLUMN api_tokens.client_id IS 'oauth：授权的 OAuth 客户端 ID；其余为空字符串';
COMMENT ON COLUMN api_tokens.client_name IS 'oauth：授权时客户端的名称，用于“已连接的应用”列表；其余为空字符串';
COMMENT ON COLUMN api_tokens.resource IS 'oauth：令牌只能用于这个资源（例如 MCP 服务的地址）；其余为空字符串';
COMMENT ON COLUMN api_tokens.auth_methods IS '登录 token 这次登录通过的验证方式（空格分隔），如 password totp；确认身份时补充；其他种类为空';
COMMENT ON COLUMN api_tokens.auth_time IS '登录 token 最近一次验证身份的时间：登录或确认身份；为空表示不经过本人登录（如代为登录）';

-- +goose Down
DROP TABLE api_tokens;
