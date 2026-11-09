-- +goose Up
-- 产品自己的用户字段：直接加在 users 表上（users 表归产品所有）。
ALTER TABLE users ADD COLUMN name text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE users DROP COLUMN name;
