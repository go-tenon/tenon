-- +goose Up
CREATE TABLE users (
	id         uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	email      text NOT NULL UNIQUE,
	name       text NOT NULL DEFAULT ''
);
CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
	FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE users;
