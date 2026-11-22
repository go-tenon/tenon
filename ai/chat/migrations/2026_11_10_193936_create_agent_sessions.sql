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

CREATE TABLE agent_session_track_events (
    id bigserial PRIMARY KEY,
    app_name varchar(255) NOT NULL,
    user_id varchar(255) NOT NULL,
    session_id varchar(255) NOT NULL,
    track varchar(255) NOT NULL,
    event jsonb NOT NULL,
    created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamp,
    deleted_at timestamp
);
CREATE INDEX idx_agent_session_track_events_lookup ON agent_session_track_events (app_name, user_id, session_id, track, created_at);
CREATE INDEX idx_agent_session_track_events_expires ON agent_session_track_events (expires_at) WHERE expires_at IS NOT NULL;
COMMENT ON TABLE agent_session_track_events IS '智能体会话分轨事件记录（trpc-agent-go session/postgres）';
COMMENT ON COLUMN agent_session_track_events.id IS '自增主键';
COMMENT ON COLUMN agent_session_track_events.app_name IS '智能体应用名称';
COMMENT ON COLUMN agent_session_track_events.user_id IS '用户标识';
COMMENT ON COLUMN agent_session_track_events.session_id IS '会话标识';
COMMENT ON COLUMN agent_session_track_events.track IS '事件轨道名称';
COMMENT ON COLUMN agent_session_track_events.event IS '分轨事件内容（JSON）';
COMMENT ON COLUMN agent_session_track_events.created_at IS '创建时间';
COMMENT ON COLUMN agent_session_track_events.updated_at IS '更新时间';
COMMENT ON COLUMN agent_session_track_events.expires_at IS '过期时间；为空表示不过期';
COMMENT ON COLUMN agent_session_track_events.deleted_at IS '软删除时间；为空表示未删除';

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

CREATE TABLE agent_user_states (
    id bigserial PRIMARY KEY,
    app_name varchar(255) NOT NULL,
    user_id varchar(255) NOT NULL,
    key varchar(255) NOT NULL,
    value text,
    created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamp,
    deleted_at timestamp
);
CREATE UNIQUE INDEX idx_agent_user_states_unique_active ON agent_user_states (app_name, user_id, key) WHERE deleted_at IS NULL;
CREATE INDEX idx_agent_user_states_expires ON agent_user_states (expires_at) WHERE expires_at IS NOT NULL;
COMMENT ON TABLE agent_user_states IS '智能体用户级键值状态（trpc-agent-go session/postgres）';
COMMENT ON COLUMN agent_user_states.id IS '自增主键';
COMMENT ON COLUMN agent_user_states.app_name IS '智能体应用名称';
COMMENT ON COLUMN agent_user_states.user_id IS '用户标识';
COMMENT ON COLUMN agent_user_states.key IS '状态键名';
COMMENT ON COLUMN agent_user_states.value IS '状态值';
COMMENT ON COLUMN agent_user_states.created_at IS '创建时间';
COMMENT ON COLUMN agent_user_states.updated_at IS '更新时间';
COMMENT ON COLUMN agent_user_states.expires_at IS '过期时间；为空表示不过期';
COMMENT ON COLUMN agent_user_states.deleted_at IS '软删除时间；为空表示未删除';

-- +goose Down
DROP TABLE agent_user_states;
DROP TABLE agent_app_states;
DROP TABLE agent_session_summaries;
DROP TABLE agent_session_track_events;
DROP TABLE agent_session_events;
DROP TABLE agent_session_states;
