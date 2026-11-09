-- +goose Up
CREATE TABLE notes (
	id         uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	text       text NOT NULL,
	author     text NOT NULL
);

-- +goose Down
DROP TABLE notes;
