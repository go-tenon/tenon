-- +goose Up
CREATE TABLE files (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    disk text NOT NULL,
    key text NOT NULL,
    filename text NOT NULL,
    content_type text NOT NULL,
    size bigint NOT NULL CHECK (size >= 0),
    meta jsonb NOT NULL DEFAULT '{}',
    owner_type text NOT NULL,
    owner_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('temporary', 'ready', 'deleted')),
    expires_at timestamptz,
    CONSTRAINT files_disk_key_unique UNIQUE (disk, key)
);
CREATE INDEX files_owner ON files (owner_type, owner_id);
CREATE INDEX files_expires_at ON files (expires_at) WHERE expires_at IS NOT NULL;
COMMENT ON TABLE files IS '已上传文件；公开地址 = 磁盘 public_url + key';
COMMENT ON COLUMN files.id IS '主键';
COMMENT ON COLUMN files.created_at IS '创建时间';
COMMENT ON COLUMN files.updated_at IS '更新时间';
COMMENT ON COLUMN files.disk IS '应用装配时的磁盘名，不是驱动名';
COMMENT ON COLUMN files.key IS '文件在存储磁盘中的对象路径';
COMMENT ON COLUMN files.filename IS '上传时的原始文件名';
COMMENT ON COLUMN files.content_type IS '文件的 MIME 类型';
COMMENT ON COLUMN files.size IS '文件大小（字节）';
COMMENT ON COLUMN files.meta IS '文件内容的固有元数据，例如图片的像素宽高（width、height）';
COMMENT ON COLUMN files.owner_type IS '文件所属的主体类型';
COMMENT ON COLUMN files.owner_id IS '文件所属的主体 ID';
COMMENT ON COLUMN files.status IS '文件状态：temporary、ready 或 deleted';
COMMENT ON COLUMN files.expires_at IS 'temporary 未确认的回收时间，deleted 等待清理任务删除对象的时间；ready 为空';

-- +goose Down
DROP TABLE files;
