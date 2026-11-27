-- +goose Up
ALTER TABLE api_tokens ADD COLUMN read_only boolean NOT NULL DEFAULT false;
ALTER TABLE impersonation_tickets ADD COLUMN read_only boolean NOT NULL DEFAULT false;
COMMENT ON COLUMN api_tokens.read_only IS '凭据是否限定为只读模式（例如只读代为登录排查）';
COMMENT ON COLUMN impersonation_tickets.read_only IS '交换后的代为登录令牌是否限定为只读模式';

-- +goose Down
ALTER TABLE impersonation_tickets DROP COLUMN IF EXISTS read_only;
ALTER TABLE api_tokens DROP COLUMN IF EXISTS read_only;
