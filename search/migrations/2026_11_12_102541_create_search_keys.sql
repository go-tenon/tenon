-- +goose Up
CREATE TABLE search_keys (
    index_name text NOT NULL,
    doc_key text NOT NULL,
    synced_at timestamptz NOT NULL,
    PRIMARY KEY (index_name, doc_key)
);
COMMENT ON TABLE search_keys IS '已同步到搜索索引的 key，同步时锁住它，使同一个 key 的同步依次执行';
COMMENT ON COLUMN search_keys.index_name IS '搜索索引名称';
COMMENT ON COLUMN search_keys.doc_key IS '文档的业务主键';
COMMENT ON COLUMN search_keys.synced_at IS '最近一次同步完成的时间';

-- +goose Down
DROP TABLE search_keys;
