-- +goose Up
CREATE TABLE operation_units (
    operation_id uuid NOT NULL REFERENCES operations (id) ON DELETE CASCADE,
    id uuid NOT NULL,
    failed boolean NOT NULL,
    PRIMARY KEY (operation_id, id)
);
COMMENT ON TABLE operation_units IS '扇出到队列、还没有成功的单位；单位成功时删除，失败时保留，failed 为 true';
COMMENT ON COLUMN operation_units.operation_id IS '所属长操作 ID，关联 operations.id';
COMMENT ON COLUMN operation_units.id IS '单个子工作单位的 ID';
COMMENT ON COLUMN operation_units.failed IS '该单位是否已永久失败';

-- +goose Down
DROP TABLE operation_units;
