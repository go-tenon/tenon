-- +goose Up
-- trpc-agent-go session/postgres 的表结构，表名前缀 agent_；由 ai/postgres.NewSessions 使用。
CREATE TABLE agent_session_states (
    id bigserial PRIMARY KEY,
    app_name varchar(255) NOT NULL,
    user_id varchar(255) NOT NULL,
    session_id varchar(255) NOT NULL,
    state jsonb,
    created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamp,
    deleted_at timestamp
);
CREATE UNIQUE INDEX idx_agent_session_states_unique_active ON agent_session_states (app_name, user_id, session_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_agent_session_states_expires ON agent_session_states (expires_at) WHERE expires_at IS NOT NULL;
COMMENT ON TABLE agent_session_states IS '智能体会话状态（trpc-agent-go session/postgres）';
COMMENT ON COLUMN agent_session_states.id IS '自增主键';
COMMENT ON COLUMN agent_session_states.app_name IS '智能体应用名称';
COMMENT ON COLUMN agent_session_states.user_id IS '用户标识';
COMMENT ON COLUMN agent_session_states.session_id IS '会话标识';
COMMENT ON COLUMN agent_session_states.state IS '会话状态数据（JSON）';
COMMENT ON COLUMN agent_session_states.created_at IS '创建时间';
COMMENT ON COLUMN agent_session_states.updated_at IS '更新时间';
COMMENT ON COLUMN agent_session_states.expires_at IS '过期时间；为空表示不过期';
COMMENT ON COLUMN agent_session_states.deleted_at IS '软删除时间；为空表示未删除';

-- +goose Down
DROP TABLE agent_session_states;
