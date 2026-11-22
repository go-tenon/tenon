-- +goose Up
CREATE TABLE activity_log (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    tenant text NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    impersonator_type text NOT NULL,
    impersonator_id text NOT NULL,
    event text NOT NULL,
    target_type text NOT NULL,
    target_id text NOT NULL,
    properties jsonb NOT NULL,
    request_id text NOT NULL
);
CREATE INDEX activity_log_tenant ON activity_log (tenant, id);
CREATE INDEX activity_log_target ON activity_log (tenant, target_type, target_id, id);
CREATE INDEX activity_log_actor ON activity_log (tenant, actor_type, actor_id, id);
CREATE INDEX activity_log_created_at ON activity_log (created_at);
COMMENT ON TABLE activity_log IS '操作日志：谁（actor）在哪个租户中对什么（target）做了什么（event）';
COMMENT ON COLUMN activity_log.id IS '主键';
COMMENT ON COLUMN activity_log.created_at IS '创建时间';
COMMENT ON COLUMN activity_log.updated_at IS '更新时间';
COMMENT ON COLUMN activity_log.tenant IS '空字符串为平台本身';
COMMENT ON COLUMN activity_log.actor_type IS '操作人的主体类型；空字符串表示没有操作人';
COMMENT ON COLUMN activity_log.actor_id IS '空字符串表示没有操作人';
COMMENT ON COLUMN activity_log.impersonator_type IS '代为登录时实际操作的人，与 impersonator_id 一起引用；空字符串表示操作人本人';
COMMENT ON COLUMN activity_log.impersonator_id IS '代为登录时实际操作人的主体 ID；空字符串表示操作人本人';
COMMENT ON COLUMN activity_log.event IS '发生的事件名称';
COMMENT ON COLUMN activity_log.target_type IS '被操作对象的类型；空字符串表示无特定对象';
COMMENT ON COLUMN activity_log.target_id IS '被操作对象的 ID；空字符串表示无特定对象';
COMMENT ON COLUMN activity_log.properties IS '事件附带的扩展属性（JSON）';
COMMENT ON COLUMN activity_log.request_id IS '触发本次操作的请求 ID；无请求上下文时为空字符串';

-- +goose Down
DROP TABLE activity_log;
