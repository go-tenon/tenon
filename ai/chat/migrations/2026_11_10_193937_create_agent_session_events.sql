-- +goose Up
-- trpc-agent-go session/postgres 的表结构，表名前缀 agent_；由 ai/postgres.NewSessions 使用。
CREATE TABLE agent_session_events (
    id bigserial PRIMARY KEY,
    app_name varchar(255) NOT NULL,
    user_id varchar(255) NOT NULL,
    session_id varchar(255) NOT NULL,
    event jsonb NOT NULL,
    created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamp,
    deleted_at timestamp
);
CREATE INDEX idx_agent_session_events_lookup ON agent_session_events (app_name, user_id, session_id, created_at);
CREATE INDEX idx_agent_session_events_expires ON agent_session_events (expires_at) WHERE expires_at IS NOT NULL;
COMMENT ON TABLE agent_session_events IS '智能体会话事件记录（trpc-agent-go session/postgres）';
COMMENT ON COLUMN agent_session_events.id IS '自增主键';
COMMENT ON COLUMN agent_session_events.app_name IS '智能体应用名称';
COMMENT ON COLUMN agent_session_events.user_id IS '用户标识';
COMMENT ON COLUMN agent_session_events.session_id IS '会话标识';
COMMENT ON COLUMN agent_session_events.event IS '会话事件内容（JSON）';
COMMENT ON COLUMN agent_session_events.created_at IS '创建时间';
COMMENT ON COLUMN agent_session_events.updated_at IS '更新时间';
COMMENT ON COLUMN agent_session_events.expires_at IS '过期时间；为空表示不过期';
COMMENT ON COLUMN agent_session_events.deleted_at IS '软删除时间；为空表示未删除';

-- +goose Down
DROP TABLE agent_session_events;
