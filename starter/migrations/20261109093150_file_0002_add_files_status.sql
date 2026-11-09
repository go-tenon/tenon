-- +goose Up
-- pending：客户端直传尚未完成；expires_at 非空表示到期后被 Cleanup 删除（未完成的直传、未被业务引用的上传）。
ALTER TABLE files
	ADD COLUMN status text NOT NULL DEFAULT 'ready' CHECK (status IN ('pending', 'ready')),
	ADD COLUMN expires_at timestamptz;
CREATE INDEX files_expires_at_idx ON files (expires_at) WHERE expires_at IS NOT NULL;

-- +goose Down
ALTER TABLE files DROP COLUMN status, DROP COLUMN expires_at;
