-- +goose Up
CREATE TABLE saved_views (
    id              uuid PRIMARY KEY,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    tenant          text NOT NULL DEFAULT '',
    target          text NOT NULL,
    owner_type      text NOT NULL DEFAULT '',
    owner_id        text NOT NULL DEFAULT '',
    name            text NOT NULL,
    config          jsonb NOT NULL DEFAULT '{}'::jsonb,
    position        integer NOT NULL DEFAULT 0,
    created_by_type text NOT NULL DEFAULT '',
    created_by_id   text NOT NULL DEFAULT ''
);

CREATE INDEX idx_saved_views_shared
    ON saved_views (tenant, target, position, created_at, id)
    WHERE owner_id = '';

CREATE INDEX idx_saved_views_personal
    ON saved_views (tenant, target, owner_type, owner_id, position, created_at, id)
    WHERE owner_id <> '';

CREATE UNIQUE INDEX uq_saved_views_shared_name
    ON saved_views (tenant, target, lower(name))
    WHERE owner_id = '';

CREATE UNIQUE INDEX uq_saved_views_personal_name
    ON saved_views (tenant, target, owner_type, owner_id, lower(name))
    WHERE owner_id <> '';

COMMENT ON TABLE saved_views IS '已保存的视图与筛选条件：按租户和业务对象隔离，owner_id 为空表示共享视图，非空表示主体个人视图';
COMMENT ON COLUMN saved_views.id IS '视图 ID（UUIDv7）';
COMMENT ON COLUMN saved_views.created_at IS '创建时间';
COMMENT ON COLUMN saved_views.updated_at IS '最近修改时间';
COMMENT ON COLUMN saved_views.tenant IS '所属租户 ID，单租户应用为空字符串';
COMMENT ON COLUMN saved_views.target IS '业务对象标识，例如 conversation、contact';
COMMENT ON COLUMN saved_views.owner_type IS '个人视图归属主体的类型，共享视图为空字符串';
COMMENT ON COLUMN saved_views.owner_id IS '个人视图归属主体的 ID，共享视图为空字符串';
COMMENT ON COLUMN saved_views.name IS '视图名称，在同一租户、对象和作用域下忽略大小写唯一';
COMMENT ON COLUMN saved_views.config IS '视图保存的筛选与排序配置（JSON）';
COMMENT ON COLUMN saved_views.position IS '同一作用域下的展示顺序，值越小越靠前';
COMMENT ON COLUMN saved_views.created_by_type IS '创建该视图的主体类型，系统主体为空字符串';
COMMENT ON COLUMN saved_views.created_by_id IS '创建该视图的主体 ID，系统主体为空字符串';

-- +goose Down
DROP TABLE saved_views;
