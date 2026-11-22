-- +goose Up
CREATE TABLE imports (
    operation_id uuid PRIMARY KEY REFERENCES operations (id) ON DELETE CASCADE,
    file_id uuid NOT NULL,
    filename text NOT NULL,
    locale text NOT NULL,
    header jsonb NOT NULL,
    last_line integer NOT NULL
);
COMMENT ON TABLE imports IS '后台导入：一个上传的表格文件，状态和进度在 operations 表中';
COMMENT ON COLUMN imports.operation_id IS '对应的长操作 ID（主键），关联 operations.id';
COMMENT ON COLUMN imports.file_id IS '上传表格文件的记录 ID';
COMMENT ON COLUMN imports.filename IS '上传时的原始文件名';
COMMENT ON COLUMN imports.locale IS '发起导入的请求的语言，处理时用它翻译验证提示';
COMMENT ON COLUMN imports.header IS '文件的表头，开始处理时写入';
COMMENT ON COLUMN imports.last_line IS '已经处理到的行号，任务重试时从下一行继续';

-- +goose Down
DROP TABLE imports;
