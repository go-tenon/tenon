-- +goose Up
CREATE TABLE queue_outbox (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    topic text NOT NULL,
    payload bytea NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}',
    available_at timestamptz NOT NULL
);
CREATE INDEX queue_outbox_available_at ON queue_outbox (available_at, id);
COMMENT ON TABLE queue_outbox IS '待转发到 NATS JetStream 的任务和延迟任务；由 Worker 批量转发后删除';
COMMENT ON COLUMN queue_outbox.id IS '主键';
COMMENT ON COLUMN queue_outbox.created_at IS '任务发布时间';
COMMENT ON COLUMN queue_outbox.updated_at IS '更新时间';
COMMENT ON COLUMN queue_outbox.topic IS '任务的主题名';
COMMENT ON COLUMN queue_outbox.payload IS '任务的 JSON 负载';
COMMENT ON COLUMN queue_outbox.metadata IS '任务附带的元数据（JSON），包含 request_id 和链路信息';
COMMENT ON COLUMN queue_outbox.available_at IS '任务最早可以转发并执行的时间';

-- +goose Down
DROP TABLE queue_outbox;
