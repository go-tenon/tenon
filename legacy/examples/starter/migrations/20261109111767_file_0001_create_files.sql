-- +goose Up
CREATE TABLE files (
	id           uuid PRIMARY KEY DEFAULT uuidv7(),
	created_at   timestamptz NOT NULL DEFAULT now(),
	updated_at   timestamptz NOT NULL DEFAULT now(),
	user_id      uuid,                 -- 上传者；系统生成的文件为空。不加外键，file 模块不依赖账号模块
	filename     text NOT NULL,        -- 原始文件名，下载时使用
	content_type text NOT NULL,
	size         bigint NOT NULL,
	key          text NOT NULL CONSTRAINT files_key_key UNIQUE -- storage 中的对象键
);
CREATE INDEX files_user_id_idx ON files (user_id);

-- +goose Down
DROP TABLE files;
