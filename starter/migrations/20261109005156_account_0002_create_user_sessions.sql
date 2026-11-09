-- +goose Up
-- 登录会话：每次登录（网页或 App）一行，用于刷新 token、列出登录设备与踢下线。
CREATE TABLE user_sessions (
	id              uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at      timestamptz NOT NULL DEFAULT now(),
	updated_at      timestamptz NOT NULL DEFAULT now(),
	user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	kind            text NOT NULL CHECK (kind IN ('web', 'api')),
	token_hash      text CONSTRAINT user_sessions_token_hash_key UNIQUE, -- 当前 refresh token 的 SHA-256；网页会话为空
	prev_token_hash text,                                                -- 上一个 refresh token，宽限期内仍可使用
	rotated_at      timestamptz,
	remember        boolean NOT NULL DEFAULT false,
	ip              text NOT NULL DEFAULT '',
	user_agent      text NOT NULL DEFAULT '',
	last_seen_at    timestamptz NOT NULL DEFAULT now(),
	expires_at      timestamptz NOT NULL
);
CREATE INDEX user_sessions_user_id_idx ON user_sessions (user_id);
CREATE INDEX user_sessions_prev_token_hash_idx ON user_sessions (prev_token_hash);

-- +goose Down
DROP TABLE user_sessions;
