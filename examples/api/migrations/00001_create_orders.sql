-- +goose Up
CREATE TABLE orders (
    id uuid PRIMARY KEY,
    owner_id text NOT NULL,
    title text NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT orders_owner_id_idx_chk CHECK (owner_id <> '')
);
CREATE INDEX orders_owner_created_id_idx ON orders (owner_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE IF EXISTS orders;
