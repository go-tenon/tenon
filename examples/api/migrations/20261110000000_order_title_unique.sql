-- +goose Up
ALTER TABLE orders ADD CONSTRAINT orders_owner_title_key UNIQUE (owner_id, title);

-- +goose Down
ALTER TABLE orders DROP CONSTRAINT orders_owner_title_key;
