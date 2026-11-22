-- +goose Up
CREATE TABLE exports (
    operation_id uuid PRIMARY KEY REFERENCES operations (id) ON DELETE CASCADE,
    format text NOT NULL,
    file_id uuid,
    filename text NOT NULL,
    url text NOT NULL,
    expires_at timestamptz
);
COMMENT ON TABLE exports IS '后台导出生成的文件，状态和进度在 operations 表中';
COMMENT ON COLUMN exports.operation_id IS '对应的长操作 ID（主键），关联 operations.id';
COMMENT ON COLUMN exports.format IS '导出文件格式，如 csv、xlsx';
COMMENT ON COLUMN exports.file_id IS 'completed 时对应的文件记录 ID；尚未生成时为空';
COMMENT ON COLUMN exports.filename IS '下载时的文件名';
COMMENT ON COLUMN exports.url IS 'completed 时文件的公开地址，文件在 expires_at 之后被回收';
COMMENT ON COLUMN exports.expires_at IS '导出文件的过期回收时间；尚未生成时为空';

-- +goose Down
DROP TABLE exports;
