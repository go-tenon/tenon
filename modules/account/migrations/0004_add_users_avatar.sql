-- +goose Up
ALTER TABLE users ADD COLUMN avatar_key text NOT NULL DEFAULT ''; -- 头像在 storage 中的对象键，空表示没有头像

-- +goose Down
ALTER TABLE users DROP COLUMN avatar_key;
