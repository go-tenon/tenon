-- +goose Up
-- 系统设置：每组设置一行，value 为整组设置的 JSON。
CREATE TABLE system_settings (
	key        text PRIMARY KEY,
	updated_at timestamptz NOT NULL DEFAULT now(),
	value      jsonb NOT NULL
);

-- +goose Down
DROP TABLE system_settings;
