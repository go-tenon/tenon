-- +goose Up
-- users 表归产品所有：发布后可以在本文件或新的迁移中加入产品自己的列。
-- 下面这些列是账号模块要求的（account.UserFields），不要删除或改名。
CREATE TABLE users (
	id                uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at        timestamptz NOT NULL DEFAULT now(),
	updated_at        timestamptz NOT NULL DEFAULT now(),
	email             text NOT NULL CONSTRAINT users_email_key UNIQUE, -- 小写
	password_hash     text NOT NULL DEFAULT '',                        -- argon2id；为空表示不能用密码登录
	email_verified_at timestamptz,
	disabled_at       timestamptz,                                     -- 非空表示已停用
	last_login_at     timestamptz,
	locale            text NOT NULL DEFAULT '',                        -- 用户选择的语言，空表示跟随请求
	avatar_key        text NOT NULL DEFAULT ''                         -- 头像在 storage 中的对象键
);

-- +goose Down
DROP TABLE users;
