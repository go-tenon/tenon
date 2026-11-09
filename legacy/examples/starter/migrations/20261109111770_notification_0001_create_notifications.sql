-- +goose Up
-- 站内通知。user_id 不加外键，notification 模块不依赖账号模块。
CREATE TABLE notifications (
	id         uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at timestamptz NOT NULL DEFAULT now(),
	user_id    uuid NOT NULL,
	type       text NOT NULL,              -- 如 order.shipped，客户端据此选择图标、决定跳转
	title      text NOT NULL,
	body       text NOT NULL DEFAULT '',
	url        text NOT NULL DEFAULT '',   -- 点击后打开的地址
	data       jsonb NOT NULL DEFAULT '{}',
	read_at    timestamptz
);
CREATE INDEX notifications_user_id_idx ON notifications (user_id, id);
CREATE INDEX notifications_unread_idx ON notifications (user_id) WHERE read_at IS NULL;

-- +goose Down
DROP TABLE notifications;
