-- +goose Up
CREATE TABLE auth_passkeys (
    credential_id text PRIMARY KEY,
    subject_type text NOT NULL,
    credential text NOT NULL,
    name text NOT NULL,
    created_at timestamptz NOT NULL,
    last_used_at timestamptz
);
CREATE INDEX auth_passkeys_created_at ON auth_passkeys (created_at);
COMMENT ON TABLE auth_passkeys IS 'passkey 的公钥，由 login/passkey 管理；属于哪个主体由 auth_identifiers 中 kind 为 passkey 的标识决定';
COMMENT ON COLUMN auth_passkeys.credential_id IS 'credential ID 的 URL 安全 base64，也是对应登录标识的 value';
COMMENT ON COLUMN auth_passkeys.credential IS 'go-webauthn 的 Credential（JSON）：公钥、签名计数、标志位、传输方式';
COMMENT ON COLUMN auth_passkeys.name IS '用户可见的名称，例如“iPhone”';

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
DROP TABLE auth_passkeys;
