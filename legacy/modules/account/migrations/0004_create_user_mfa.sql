-- +goose Up
-- 两步验证（TOTP）：每个账号最多一行。enabled_at 为空表示已生成密钥但尚未确认启用。
CREATE TABLE user_mfa (
	user_id        uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
	created_at     timestamptz NOT NULL DEFAULT now(),
	updated_at     timestamptz NOT NULL DEFAULT now(),
	secret         text NOT NULL,                -- 加密后的 TOTP 密钥
	recovery_codes text[] NOT NULL DEFAULT '{}', -- 恢复码的 SHA-256，用过即删除
	last_step      bigint NOT NULL DEFAULT 0,    -- 最近一次使用的 TOTP 时间步，防止同一验证码被重复使用
	enabled_at     timestamptz
);

-- +goose Down
DROP TABLE user_mfa;
