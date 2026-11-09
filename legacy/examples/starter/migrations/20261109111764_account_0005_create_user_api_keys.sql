-- +goose Up
-- API Key：用户为脚本或第三方系统创建的长期凭证，可限定能力（scope）。
CREATE TABLE user_api_keys (
	id           uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at   timestamptz NOT NULL DEFAULT now(),
	user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	name         text NOT NULL,
	token_hash   text NOT NULL CONSTRAINT user_api_keys_token_hash_key UNIQUE, -- SHA-256
	hint         text NOT NULL,                -- token 末尾几位，便于用户辨认
	scopes       text[] NOT NULL DEFAULT '{}', -- 空表示不限
	last_used_at timestamptz,
	expires_at   timestamptz                   -- 空表示永不过期
);
CREATE INDEX user_api_keys_user_id_idx ON user_api_keys (user_id);

-- +goose Down
DROP TABLE user_api_keys;
