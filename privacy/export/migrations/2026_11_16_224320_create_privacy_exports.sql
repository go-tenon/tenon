-- +goose Up
CREATE TABLE privacy_exports (
    operation_id uuid PRIMARY KEY REFERENCES operations (id) ON DELETE CASCADE,
    file_id uuid,
    filename text NOT NULL,
    expires_at timestamptz
);
COMMENT ON TABLE privacy_exports IS '导出个人数据生成的压缩包，状态和进度在 operations 表中';
COMMENT ON COLUMN privacy_exports.operation_id IS '对应的长操作 ID（主键），关联 operations.id';
COMMENT ON COLUMN privacy_exports.file_id IS 'completed 时的压缩包，文件在 expires_at 之后被回收';
COMMENT ON COLUMN privacy_exports.filename IS '下载时的文件名';
COMMENT ON COLUMN privacy_exports.expires_at IS '压缩包的过期回收时间；尚未生成时为空';

-- +goose Down
DROP TABLE privacy_exports;
