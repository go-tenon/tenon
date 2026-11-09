-- +goose Up
CREATE TABLE notes (
	id         uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	text       text NOT NULL
);
CREATE INDEX notes_user_id_idx ON notes (user_id);

-- +goose Down
DROP TABLE notes;
