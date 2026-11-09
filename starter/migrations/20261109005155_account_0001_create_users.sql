-- +goose Up
CREATE TABLE users (
	id            uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at    timestamptz NOT NULL DEFAULT now(),
	updated_at    timestamptz NOT NULL DEFAULT now(),
	email         text NOT NULL CONSTRAINT users_email_key UNIQUE, -- 小写
	name          text NOT NULL DEFAULT '',
	password_hash text NOT NULL DEFAULT '',                        -- argon2id；为空表示不能用密码登录
	disabled_at   timestamptz,                                     -- 非空表示已停用
	last_login_at timestamptz
);

-- +goose Down
DROP TABLE users;
