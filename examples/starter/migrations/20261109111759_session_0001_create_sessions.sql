-- +goose Up
-- scs 的会话存储（github.com/alexedwards/scs/postgresstore）。
CREATE TABLE sessions (
	token  text PRIMARY KEY,
	data   bytea NOT NULL,
	expiry timestamptz NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions (expiry);

-- +goose Down
DROP TABLE sessions;
