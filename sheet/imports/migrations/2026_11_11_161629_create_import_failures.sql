-- +goose Up
CREATE TABLE import_failures (
    import_id uuid NOT NULL REFERENCES imports (operation_id) ON DELETE CASCADE,
    line integer NOT NULL,
    cells jsonb NOT NULL,
    errors jsonb NOT NULL,
    message text NOT NULL,
    PRIMARY KEY (import_id, line)
);
COMMENT ON TABLE import_failures IS '后台导入中没有导入的行';
COMMENT ON COLUMN import_failures.import_id IS '所属导入任务 ID，关联 imports.operation_id';
COMMENT ON COLUMN import_failures.line IS '失败行在源文件中的行号';
COMMENT ON COLUMN import_failures.cells IS '这一行的原始单元格，与 imports.header 对应';
COMMENT ON COLUMN import_failures.errors IS '以字段名为键的错误提示';
COMMENT ON COLUMN import_failures.message IS '整行级别的错误提示；无整行错误时为空字符串';

-- +goose Down
DROP TABLE import_failures;
