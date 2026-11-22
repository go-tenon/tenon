-- +goose Up
CREATE TABLE authz_assignments (
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    tenant text NOT NULL,
    role_id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (subject_type, subject_id, tenant, role_id)
);
CREATE INDEX authz_assignments_tenant ON authz_assignments (tenant);
CREATE INDEX authz_assignments_role_id ON authz_assignments (role_id);
COMMENT ON TABLE authz_assignments IS '主体在某个域中拥有的角色';
COMMENT ON COLUMN authz_assignments.subject_type IS '拥有角色的主体类型';
COMMENT ON COLUMN authz_assignments.subject_id IS '拥有角色的主体 ID';
COMMENT ON COLUMN authz_assignments.tenant IS '空字符串为平台本身，* 为所有租户，其余为租户 ID';
COMMENT ON COLUMN authz_assignments.role_id IS '分配的角色 ID，关联 authz_roles.id';
COMMENT ON COLUMN authz_assignments.created_at IS '分配时间';

-- +goose Down
DROP TABLE authz_assignments;
