-- +goose Up
-- 异步导出任务。user_id 不加外键，export 模块不依赖账号模块。
CREATE TABLE export_jobs (
	id          uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now(),
	user_id     uuid NOT NULL,
	kind        text NOT NULL,                -- 导出类型，对应 Jobs.Register 的 kind
	format      text NOT NULL,                -- csv | xlsx
	params      jsonb NOT NULL DEFAULT '{}',  -- 筛选条件等参数
	status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'done', 'failed')),
	filename    text NOT NULL DEFAULT '',
	key         text NOT NULL DEFAULT '',     -- 生成的文件在 storage 中的对象键
	size        bigint NOT NULL DEFAULT 0,
	error       text NOT NULL DEFAULT '',
	finished_at timestamptz,
	expires_at  timestamptz NOT NULL          -- 到期后被 Cleanup 删除
);
CREATE INDEX export_jobs_user_id_idx ON export_jobs (user_id, id);
CREATE INDEX export_jobs_expires_at_idx ON export_jobs (expires_at);

-- +goose Down
DROP TABLE export_jobs;
