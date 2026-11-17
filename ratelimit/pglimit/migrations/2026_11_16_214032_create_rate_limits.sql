-- +goose Up
CREATE TABLE rate_limits (
    name text NOT NULL,
    key text NOT NULL,
    tat timestamptz NOT NULL,
    PRIMARY KEY (name, key)
);
CREATE INDEX rate_limits_tat ON rate_limits (tat);
COMMENT ON TABLE rate_limits IS '限流计数（ratelimit/pglimit）；tat 早于当前时间的行等同于不存在，由 pglimit.Limiter.Cleanup 删除';
COMMENT ON COLUMN rate_limits.name IS '限流器的名字，同名的限流器共享配额';
COMMENT ON COLUMN rate_limits.tat IS 'GCRA 的理论到达时间，按应用服务器的时钟计算';

-- +goose Down
DROP TABLE rate_limits;
