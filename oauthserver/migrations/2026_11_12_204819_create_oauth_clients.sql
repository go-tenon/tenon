-- +goose Up
CREATE TABLE oauth_clients (
    id text PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    name text NOT NULL,
    redirect_uris jsonb NOT NULL,
    last_used_at timestamptz NOT NULL
);
CREATE INDEX oauth_clients_last_used_at ON oauth_clients (last_used_at);
COMMENT ON TABLE oauth_clients IS 'OAuth 动态客户端注册（RFC 7591）登记的公开客户端；长期没有用于授权的由 oauthserver.Server.CleanupClients 删除';
COMMENT ON COLUMN oauth_clients.id IS '客户端 ID（主键）';
COMMENT ON COLUMN oauth_clients.created_at IS '创建时间';
COMMENT ON COLUMN oauth_clients.updated_at IS '更新时间';
COMMENT ON COLUMN oauth_clients.name IS '客户端展示名称';
COMMENT ON COLUMN oauth_clients.redirect_uris IS '允许的回调地址列表（JSON 数组）';
COMMENT ON COLUMN oauth_clients.last_used_at IS '最近一次用于授权的时间';

-- +goose Down
DROP TABLE oauth_clients;
