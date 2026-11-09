-- +goose Up
-- 角色：管理员在后台创建，勾选权限（权限是代码中声明的字符串，* 表示全部）。
CREATE TABLE roles (
	id          uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now(),
	name        text NOT NULL CONSTRAINT roles_name_key UNIQUE,
	description text NOT NULL DEFAULT '',
	permissions text[] NOT NULL DEFAULT '{}'
);

-- 用户的角色。user_id 不加外键，role 模块不依赖账号模块。
CREATE TABLE user_roles (
	user_id    uuid NOT NULL,
	role_id    uuid NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
	created_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (user_id, role_id)
);
CREATE INDEX user_roles_role_id_idx ON user_roles (role_id);

-- +goose Down
DROP TABLE user_roles;
DROP TABLE roles;
