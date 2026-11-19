-- +goose Up
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

-- +goose Down
DROP TABLE queue_failed_jobs;
