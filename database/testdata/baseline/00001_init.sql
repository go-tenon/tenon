-- +goose Up
CREATE TABLE items (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    sku text NOT NULL,
    CONSTRAINT items_sku_key UNIQUE (sku)
);

-- +goose Down
DROP TABLE IF EXISTS items;
