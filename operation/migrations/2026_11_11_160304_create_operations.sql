-- +goose Up
CREATE TABLE operations (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    tenant text NOT NULL,
    owner_type text NOT NULL,
    owner_id text NOT NULL,
    kind text NOT NULL,
    status text NOT NULL,
    total integer NOT NULL,
    processed integer NOT NULL,
    failed integer NOT NULL,
    version integer NOT NULL,
    error text NOT NULL,
    error_data jsonb NOT NULL,
    completed_at timestamptz
);
CREATE INDEX operations_owner ON operations (tenant, owner_type, owner_id, id);
CREATE INDEX operations_created_at ON operations (created_at);
COMMENT ON TABLE operations IS '用户在后台发起的长操作，例如导入、导出和批量处理，记录它的状态和进度';
COMMENT ON COLUMN operations.id IS '主键';
COMMENT ON COLUMN operations.created_at IS '创建时间';
COMMENT ON COLUMN operations.updated_at IS '更新时间';
COMMENT ON COLUMN operations.tenant IS '空字符串为平台本身';
COMMENT ON COLUMN operations.owner_type IS '发起操作的主体类型';
COMMENT ON COLUMN operations.owner_id IS '发起操作的主体 ID';
COMMENT ON COLUMN operations.kind IS '操作的类型，由应用定义，例如 articles.import';
COMMENT ON COLUMN operations.status IS 'pending、running、completed 或 failed';
COMMENT ON COLUMN operations.total IS '预计的单位数，0 表示还不知道';
COMMENT ON COLUMN operations.processed IS '已经处理的单位数，包括失败的单位';
COMMENT ON COLUMN operations.failed IS '处理失败的单位数';
COMMENT ON COLUMN operations.version IS '每次保存加一，同一个操作的两次执行据此发现对方';
COMMENT ON COLUMN operations.error IS 'failed 时的错误码，按 i18n 消息翻译，error_data 是模板数据；其他状态为空字符串';
COMMENT ON COLUMN operations.error_data IS '错误码对应的翻译模板参数（JSON）';
COMMENT ON COLUMN operations.completed_at IS '操作结束（completed 或 failed）的时间；进行中为空';

-- +goose Down
DROP TABLE operations;
