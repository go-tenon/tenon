-- +goose Up
CREATE TABLE schedule_tasks (
    name text PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    last_tick timestamptz,
    running_owner text NOT NULL DEFAULT '',
    running_until timestamptz
);
COMMENT ON TABLE schedule_tasks IS '定时任务的触发水位和运行租约（schedule/pgschedule）；每个任务永久一行';
COMMENT ON COLUMN schedule_tasks.name IS '定时任务名称';
COMMENT ON COLUMN schedule_tasks.created_at IS '首次登记时间';
COMMENT ON COLUMN schedule_tasks.updated_at IS '最后更新时间';
COMMENT ON COLUMN schedule_tasks.last_tick IS '最近一次已领取的按表达式触发时刻，单调递增';
COMMENT ON COLUMN schedule_tasks.running_owner IS '当前持有运行租约的实例标识';
COMMENT ON COLUMN schedule_tasks.running_until IS '当前运行租约的过期时间';

-- +goose Down
DROP TABLE schedule_tasks;
