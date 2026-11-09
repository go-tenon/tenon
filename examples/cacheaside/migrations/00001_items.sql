-- +goose Up
CREATE TABLE items (
    id text PRIMARY KEY,
    name text NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS items;
