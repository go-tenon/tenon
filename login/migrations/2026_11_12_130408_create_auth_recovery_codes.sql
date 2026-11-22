-- +goose Up
CREATE TABLE auth_recovery_codes (
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    code_hash text NOT NULL,
    PRIMARY KEY (subject_type, subject_id, code_hash)
);
COMMENT ON TABLE auth_recovery_codes IS '二次验证的恢复码，每个只能使用一次；code_hash 是恢复码的 HMAC-SHA256，密钥由应用密钥派生';
COMMENT ON COLUMN auth_recovery_codes.subject_type IS '主体类型';
COMMENT ON COLUMN auth_recovery_codes.subject_id IS '主体 ID';
COMMENT ON COLUMN auth_recovery_codes.code_hash IS '恢复码的 HMAC-SHA256，密钥由应用密钥派生';

-- +goose Down
DROP TABLE auth_recovery_codes;
