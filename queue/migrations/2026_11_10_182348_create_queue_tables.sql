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

CREATE TABLE queue_failed_jobs (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    topic text NOT NULL,
    payload bytea NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}',
    attempts integer NOT NULL,
    error text NOT NULL
);
CREATE INDEX queue_failed_jobs_created_at ON queue_failed_jobs (created_at);
COMMENT ON TABLE queue_failed_jobs IS '重试次数用完或永久失败的任务；由 queue.FailedJobs 重新发布或清理';
COMMENT ON COLUMN queue_failed_jobs.id IS '主键';
COMMENT ON COLUMN queue_failed_jobs.created_at IS '记录创建时间（任务进入失败队列的时间）';
COMMENT ON COLUMN queue_failed_jobs.updated_at IS '更新时间';
COMMENT ON COLUMN queue_failed_jobs.topic IS '任务的主题名';
COMMENT ON COLUMN queue_failed_jobs.payload IS '任务的原始二进制负载';
COMMENT ON COLUMN queue_failed_jobs.metadata IS '任务附带的元数据（JSON）';
COMMENT ON COLUMN queue_failed_jobs.attempts IS '失败前累计尝试执行的次数';
COMMENT ON COLUMN queue_failed_jobs.error IS '最后一次执行失败的错误信息';

-- +goose Down
DROP TABLE queue_failed_jobs;
DROP TABLE queue_outbox;
