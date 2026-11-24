-- +goose Up
-- trpc-agent-go session/postgres 的表结构，表名前缀 agent_；由 ai/postgres.NewSessions 使用。
CREATE TABLE agent_session_summaries (
    id bigserial PRIMARY KEY,
    app_name varchar(255) NOT NULL,
    user_id varchar(255) NOT NULL,
    session_id varchar(255) NOT NULL,
    filter_key varchar(255) NOT NULL DEFAULT '',
    summary jsonb,
    updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamp,
    deleted_at timestamp
);
CREATE UNIQUE INDEX idx_agent_session_summaries_unique_active ON agent_session_summaries (app_name, user_id, session_id, filter_key) WHERE deleted_at IS NULL;
CREATE INDEX idx_agent_session_summaries_expires ON agent_session_summaries (expires_at) WHERE expires_at IS NOT NULL;
COMMENT ON TABLE agent_session_summaries IS '智能体会话摘要（trpc-agent-go session/postgres）';
COMMENT ON COLUMN agent_session_summaries.id IS '自增主键';
COMMENT ON COLUMN agent_session_summaries.app_name IS '智能体应用名称';
COMMENT ON COLUMN agent_session_summaries.user_id IS '用户标识';
COMMENT ON COLUMN agent_session_summaries.session_id IS '会话标识';
COMMENT ON COLUMN agent_session_summaries.filter_key IS '摘要过滤键；默认为空字符串表示全量摘要';
COMMENT ON COLUMN agent_session_summaries.summary IS '会话摘要内容（JSON）';
COMMENT ON COLUMN agent_session_summaries.updated_at IS '更新时间';
COMMENT ON COLUMN agent_session_summaries.expires_at IS '过期时间；为空表示不过期';
COMMENT ON COLUMN agent_session_summaries.deleted_at IS '软删除时间；为空表示未删除';

-- +goose Down
DROP TABLE agent_session_summaries;
