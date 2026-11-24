-- +goose Up
CREATE TABLE impersonation_tickets (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    ticket_hash text NOT NULL,
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    tenant text NOT NULL,
    impersonator_type text NOT NULL,
    impersonator_id text NOT NULL,
    scopes text NOT NULL,
    reason text NOT NULL,
    lifetime bigint NOT NULL,
    idle_timeout bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    CONSTRAINT impersonation_tickets_ticket_hash_unique UNIQUE (ticket_hash)
);
CREATE INDEX impersonation_tickets_expires_at ON impersonation_tickets (expires_at);
CREATE INDEX impersonation_tickets_impersonator ON impersonation_tickets (impersonator_type, impersonator_id);
COMMENT ON TABLE impersonation_tickets IS '代为登录的一次性交换票据；只存票据明文的 SHA-256，在目标域消费一次后立即删除';
COMMENT ON COLUMN impersonation_tickets.id IS '主键';
COMMENT ON COLUMN impersonation_tickets.created_at IS '创建时间';
COMMENT ON COLUMN impersonation_tickets.updated_at IS '更新时间';
COMMENT ON COLUMN impersonation_tickets.ticket_hash IS '票据明文的 SHA-256 哈希';
COMMENT ON COLUMN impersonation_tickets.subject_type IS '被代为登录的目标主体类型';
COMMENT ON COLUMN impersonation_tickets.subject_id IS '被代为登录的目标主体 ID';
COMMENT ON COLUMN impersonation_tickets.tenant IS '代为登录凭据绑定的租户；空字符串表示不绑定';
COMMENT ON COLUMN impersonation_tickets.impersonator_type IS '发起代为登录的实际操作者主体类型';
COMMENT ON COLUMN impersonation_tickets.impersonator_id IS '发起代为登录的实际操作者主体 ID';
COMMENT ON COLUMN impersonation_tickets.scopes IS '代为登录会话的权限限制，空格分隔；空字符串表示不额外限制';
COMMENT ON COLUMN impersonation_tickets.reason IS '代为登录原因或工单号';
COMMENT ON COLUMN impersonation_tickets.lifetime IS '交换后代为登录令牌的最长有效期（秒）';
COMMENT ON COLUMN impersonation_tickets.idle_timeout IS '交换后代为登录令牌的空闲超时时长（秒）';
COMMENT ON COLUMN impersonation_tickets.expires_at IS '票据过期时间';

-- +goose Down
DROP TABLE impersonation_tickets;
