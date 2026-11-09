-- +goose Up
ALTER TABLE items ADD COLUMN note text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE items DROP COLUMN note;
