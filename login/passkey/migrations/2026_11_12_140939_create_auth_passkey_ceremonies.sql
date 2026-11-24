-- +goose Up
CREATE TABLE auth_passkey_ceremonies (
    challenge text PRIMARY KEY,
    expires_at timestamptz NOT NULL
);
CREATE INDEX auth_passkey_ceremonies_expires_at ON auth_passkey_ceremonies (expires_at);
COMMENT ON TABLE auth_passkey_ceremonies IS '已经完成的 passkey 仪式，由 login/passkey 管理：同一个客户端结果在仪式过期前不能再次提交';
COMMENT ON COLUMN auth_passkey_ceremonies.challenge IS '仪式的 challenge（URL 安全 base64），客户端的结果签了它';
COMMENT ON COLUMN auth_passkey_ceremonies.expires_at IS '仪式的过期时间，之后由 Cleanup 删除';

-- +goose Down
DROP TABLE auth_passkey_ceremonies;
