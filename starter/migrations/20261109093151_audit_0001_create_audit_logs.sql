-- +goose Up
-- 审计日志：只追加。actor_id 不设外键，删除用户后日志仍然保留。
CREATE TABLE audit_logs (
	id            uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at    timestamptz NOT NULL DEFAULT now(),
	actor_id      uuid,                         -- 操作者，系统操作为空
	action        text NOT NULL,                -- 如 account.login、order.refund
	resource_type text NOT NULL DEFAULT '',
	resource_id   text NOT NULL DEFAULT '',
	summary       text NOT NULL DEFAULT '',     -- 给人看的描述
	data          jsonb NOT NULL DEFAULT '{}',  -- 细节
	failed        boolean NOT NULL DEFAULT false,
	ip            text NOT NULL DEFAULT '',
	user_agent    text NOT NULL DEFAULT '',
	request_id    text NOT NULL DEFAULT ''
);
CREATE INDEX audit_logs_actor_id_idx ON audit_logs (actor_id, id);
CREATE INDEX audit_logs_resource_idx ON audit_logs (resource_type, resource_id, id);
CREATE INDEX audit_logs_action_idx ON audit_logs (action, id);

-- +goose Down
DROP TABLE audit_logs;
