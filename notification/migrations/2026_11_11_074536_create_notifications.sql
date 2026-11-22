-- +goose Up
CREATE TABLE notifications (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    tenant text NOT NULL,
    recipient_type text NOT NULL,
    recipient_id text NOT NULL,
    event text NOT NULL,
    data jsonb NOT NULL,
    read_at timestamptz
);
CREATE INDEX notifications_recipient ON notifications (tenant, recipient_type, recipient_id, id);
CREATE INDEX notifications_unread ON notifications (tenant, recipient_type, recipient_id, id) WHERE read_at IS NULL;
CREATE INDEX notifications_created_at ON notifications (created_at);
COMMENT ON TABLE notifications IS '站内通知：发给某个租户中某个主体（recipient）的通知';
COMMENT ON COLUMN notifications.id IS '主键';
COMMENT ON COLUMN notifications.created_at IS '创建时间';
COMMENT ON COLUMN notifications.updated_at IS '更新时间';
COMMENT ON COLUMN notifications.tenant IS '空字符串为平台本身';
COMMENT ON COLUMN notifications.recipient_type IS '接收人的主体类型';
COMMENT ON COLUMN notifications.recipient_id IS '接收人的主体 ID';
COMMENT ON COLUMN notifications.event IS '通知事件类型';
COMMENT ON COLUMN notifications.data IS '通知内容数据（JSON）';
COMMENT ON COLUMN notifications.read_at IS '已读时间；NULL 表示未读';

-- +goose Down
DROP TABLE notifications;
