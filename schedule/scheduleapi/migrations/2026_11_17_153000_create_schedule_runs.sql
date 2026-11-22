-- +goose Up
-- 定时任务的执行记录：scheduleapi.Observer 在每次执行结束、被跳过时写入一行，后台的“定时任务”页面读取。
CREATE TABLE schedule_runs (
    id          uuid PRIMARY KEY,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
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
COMMENT ON TABLE schedule_runs IS '定时任务的执行记录：scheduleapi.Observer 在每次执行结束、被跳过时写入一行，后台的“定时任务”页面读取';
COMMENT ON COLUMN schedule_runs.id IS '主键';
COMMENT ON COLUMN schedule_runs.created_at IS '记录写入时间，即执行结束或被跳过的时间';
COMMENT ON COLUMN schedule_runs.updated_at IS '更新时间';
COMMENT ON COLUMN schedule_runs.task IS '定时任务名称';
COMMENT ON COLUMN schedule_runs.tick IS '本次触发计划执行的时间；立即执行时为空';
COMMENT ON COLUMN schedule_runs.started_at IS '任务开始执行的时间；被跳过时为计划时间或写入时间';
COMMENT ON COLUMN schedule_runs.duration_ms IS '本次执行耗时（毫秒）';
COMMENT ON COLUMN schedule_runs.status IS '执行结果：succeeded、failed 或 skipped';
COMMENT ON COLUMN schedule_runs.error IS '失败时的错误信息或跳过原因；成功时为空字符串';
COMMENT ON COLUMN schedule_runs.manual IS '是否为手动触发执行';

-- +goose Down
DROP TABLE schedule_runs;
