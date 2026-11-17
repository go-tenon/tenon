-- +goose Up
-- 定时任务的执行记录：scheduleapi.Observer 在每次执行结束、被跳过时写入一行，后台的“定时任务”页面读取。
CREATE TABLE schedule_runs (
    id          uuid PRIMARY KEY,
    task        text NOT NULL,
    tick        timestamptz,
    started_at  timestamptz NOT NULL,
    duration_ms bigint NOT NULL,
    status      text NOT NULL,
    error       text NOT NULL,
    manual      boolean NOT NULL DEFAULT false
);
CREATE INDEX schedule_runs_task_started ON schedule_runs (task, started_at DESC);
CREATE INDEX schedule_runs_started ON schedule_runs (started_at);

-- +goose Down
DROP TABLE schedule_runs;
