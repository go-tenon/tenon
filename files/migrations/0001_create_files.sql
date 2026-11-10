-- +goose Up
CREATE TABLE files (
    id uuid PRIMARY KEY,
    disk text NOT NULL,
    key text NOT NULL,
    filename text NOT NULL,
    content_type text NOT NULL,
    size bigint NOT NULL CHECK (size >= 0),
    owner_type text NOT NULL,
    owner_id text NOT NULL,
    status text NOT NULL DEFAULT 'temporary' CHECK (status IN ('temporary', 'ready', 'deleted')),
    upload_mode text NOT NULL DEFAULT '' CHECK (upload_mode IN ('', 'single', 'multipart')),
    upload_id text,
    part_size bigint NOT NULL CHECK (part_size >= 5242880 AND part_size <= 5368709120),
    uploaded_at timestamptz,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT files_disk_key_unique UNIQUE (disk, key)
);
CREATE INDEX files_owner ON files (owner_type, owner_id);
CREATE INDEX files_expiry ON files (expires_at) WHERE expires_at IS NOT NULL;
COMMENT ON TABLE files IS '文件元数据及当前上传会话';
COMMENT ON COLUMN files.disk IS '稳定的存储配置名，不是驱动名';
COMMENT ON COLUMN files.expires_at IS '上传会话到期和临时对象回收时间，ready 文件本身不失效';

-- +goose Down
DROP TABLE files;
