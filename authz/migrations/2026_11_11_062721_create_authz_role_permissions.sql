-- +goose Up
CREATE TABLE authz_role_permissions (
    role_id uuid NOT NULL,
    permission text NOT NULL,
    PRIMARY KEY (role_id, permission)
);
COMMENT ON TABLE authz_role_permissions IS '角色包含的权限，permission 可以以 :* 结尾或为 *';
COMMENT ON COLUMN authz_role_permissions.role_id IS '所属角色 ID，关联 authz_roles.id';
COMMENT ON COLUMN authz_role_permissions.permission IS '权限标识，可以以 :* 结尾或为 *';

-- +goose Down
DROP TABLE authz_role_permissions;
