-- +goose Up
CREATE TABLE privacy_exports (
    operation_id uuid PRIMARY KEY REFERENCES operations (id) ON DELETE CASCADE,
    file_id uuid,
    filename text NOT NULL,
    expires_at timestamptz
);
COMMENT ON TABLE privacy_exports IS '导出个人数据生成的压缩包，状态和进度在 operations 表中';
COMMENT ON COLUMN privacy_exports.file_id IS 'completed 时的压缩包，文件在 expires_at 之后被回收';

-- +goose Down
DROP TABLE privacy_exports;
