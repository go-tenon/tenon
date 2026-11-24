-- +goose Up
-- trpc-agent-go session/postgres 的表结构，表名前缀 agent_；由 ai/postgres.NewSessions 使用。
CREATE TABLE agent_app_states (
    id bigserial PRIMARY KEY,
    app_name varchar(255) NOT NULL,
    key varchar(255) NOT NULL,
    value text,
    created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamp,
    deleted_at timestamp
);
CREATE UNIQUE INDEX idx_agent_app_states_unique_active ON agent_app_states (app_name, key) WHERE deleted_at IS NULL;
CREATE INDEX idx_agent_app_states_expires ON agent_app_states (expires_at) WHERE expires_at IS NOT NULL;
COMMENT ON TABLE agent_app_states IS '智能体应用级键值状态（trpc-agent-go session/postgres）';
COMMENT ON COLUMN agent_app_states.id IS '自增主键';
COMMENT ON COLUMN agent_app_states.app_name IS '智能体应用名称';
COMMENT ON COLUMN agent_app_states.key IS '状态键名';
COMMENT ON COLUMN agent_app_states.value IS '状态值';
COMMENT ON COLUMN agent_app_states.created_at IS '创建时间';
COMMENT ON COLUMN agent_app_states.updated_at IS '更新时间';
COMMENT ON COLUMN agent_app_states.expires_at IS '过期时间；为空表示不过期';
COMMENT ON COLUMN agent_app_states.deleted_at IS '软删除时间；为空表示未删除';

-- +goose Down
DROP TABLE agent_app_states;
