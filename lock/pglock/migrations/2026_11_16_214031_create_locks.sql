-- +goose Up
CREATE TABLE locks (
    name text PRIMARY KEY,
    owner text NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX locks_expires_at ON locks (expires_at);
COMMENT ON TABLE locks IS '原子锁（lock/pglock）；过期的行等同于不存在，由 pglock.Store.Cleanup 删除';
COMMENT ON COLUMN locks.name IS '锁的名称';
COMMENT ON COLUMN locks.owner IS '持有者：每次取得锁时随机生成';
COMMENT ON COLUMN locks.expires_at IS '过期时间，按应用服务器的时钟计算';

-- +goose Down
DROP TABLE locks;
