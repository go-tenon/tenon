-- 延迟任务改为全部交给 JetStream 定时投递，不再存入 queue_scheduled。早先建过这张表的数据库中，
-- 还在等待的任务写回 WAL，由 Worker 按原来的执行时间转发为定时消息，然后删除这张表。新的数据库中没有这张表，什么也不做。

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF to_regclass('queue_scheduled') IS NOT NULL THEN
        PERFORM pg_logical_emit_message(true, 'tenon.queue:' || topic, body)
        FROM queue_scheduled ORDER BY available_at;
        DROP TABLE queue_scheduled;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- 不重建 queue_scheduled：写回的任务已经交给 JetStream。
