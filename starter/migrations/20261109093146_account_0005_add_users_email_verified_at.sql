-- +goose Up
ALTER TABLE users ADD COLUMN email_verified_at timestamptz; -- 邮箱验证时间，空表示未验证

-- +goose Down
ALTER TABLE users DROP COLUMN email_verified_at;
