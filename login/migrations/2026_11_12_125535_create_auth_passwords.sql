-- +goose Up
CREATE TABLE auth_passwords (
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    hash text NOT NULL,
    changed_at timestamptz NOT NULL,
    PRIMARY KEY (subject_type, subject_id)
);
COMMENT ON TABLE auth_passwords IS '主体的密码哈希（PHC 格式，见 hashing 包）';
COMMENT ON COLUMN auth_passwords.subject_type IS '主体类型';
COMMENT ON COLUMN auth_passwords.subject_id IS '主体 ID';
COMMENT ON COLUMN auth_passwords.hash IS '密码哈希（PHC 格式）';
COMMENT ON COLUMN auth_passwords.changed_at IS '最近一次设置或修改密码的时间';

-- +goose Down
DROP TABLE auth_passwords;
