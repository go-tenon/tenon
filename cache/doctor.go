package cache

import (
	"context"

	"github.com/redis/go-redis/v9"

	"github.com/go-tenon/tenon/doctor"
)

// Doctor 返回检查 Redis 的一组自检，组名为 redis：connection 确认服务器连得上。
// 连不上时只提示：缓存读写失败时 Cache 直接读取数据库，应用照常工作，只是数据库的压力变大。rdb 在检查时才使用。
func Doctor(rdb redis.UniversalClient) doctor.Group {
	return doctor.Group{Name: "redis", Checks: []doctor.Check{
		{Name: "connection", Run: func(ctx context.Context) error {
			if err := rdb.Ping(ctx).Err(); err != nil {
				return doctor.Warn(
					"cannot reach redis: "+err.Error(),
					"check the redis address and credentials and that this host may reach it;\n"+
						"the application keeps working without the cache, but every read goes to the database",
				).See("docs/libraries/cache.md")
			}
			return nil
		}},
	}}
}
