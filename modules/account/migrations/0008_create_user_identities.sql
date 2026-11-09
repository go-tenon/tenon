-- +goose Up
-- 第三方登录（OAuth）绑定的外部账号。
CREATE TABLE user_identities (
	id         uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at timestamptz NOT NULL DEFAULT now(),
	user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	provider   text NOT NULL, -- 如 github、google
	subject    text NOT NULL, -- 外部账号 ID
	email      text NOT NULL DEFAULT '',
	name       text NOT NULL DEFAULT '',
	CONSTRAINT user_identities_provider_subject_key UNIQUE (provider, subject)
);
CREATE INDEX user_identities_user_id_idx ON user_identities (user_id);

-- +goose Down
DROP TABLE user_identities;
