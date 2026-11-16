-- +goose Up
CREATE TABLE privacy_erasures (
    id uuid PRIMARY KEY,
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    requested_at timestamptz NOT NULL,
    due_at timestamptz NOT NULL,
    erased_at timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    last_error text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX privacy_erasures_pending ON privacy_erasures (subject_type, subject_id) WHERE erased_at IS NULL;
CREATE INDEX privacy_erasures_due ON privacy_erasures (due_at) WHERE erased_at IS NULL;
COMMENT ON TABLE privacy_erasures IS '注销账号：待执行的注销和已经删除的记录，由 privacy 管理';
COMMENT ON COLUMN privacy_erasures.due_at IS '冷静期结束、开始删除的时间；执行失败后推迟到下一次重试的时间';
COMMENT ON COLUMN privacy_erasures.erased_at IS '删除完成的时间；为空表示还在冷静期或等待重试';
COMMENT ON COLUMN privacy_erasures.last_error IS '最近一次执行失败的原因，成功后清空';

-- +goose Down
DROP TABLE privacy_erasures;
