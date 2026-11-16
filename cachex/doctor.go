package cachex

import (
	"context"

	"github.com/bradfitz/gomemcache/memcache"

	"github.com/go-tenon/tenon/doctor"
)

// Doctor 返回检查 Memcached 的一组自检，组名为 memcached：connection 确认每一台服务器都连得上。
// 连不上时只提示：缓存读写失败时 Cache 直接读取数据库，应用照常工作，只是数据库的压力变大。mc 在检查时才使用。
func Doctor(mc *memcache.Client) doctor.Group {
	return doctor.Group{Name: "memcached", Checks: []doctor.Check{
		{Name: "connection", Run: func(context.Context) error {
			if err := mc.Ping(); err != nil {
				return doctor.Warn(
					"cannot reach every memcached server: "+err.Error(),
					"check the cache server addresses and that this host may reach them;\n"+
						"the application keeps working without the cache, but every read goes to the database",
				).See("docs/libraries/cache.md")
			}
			return nil
		}},
	}}
}
