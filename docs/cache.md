# 缓存

[文档首页](README.md) / 进阶使用

缓存直接使用 eko/gocache 的原生 Cache 和官方 store。应用选择后端并拥有客户端，业务服务接收需要的缓存对象。

本章内容：

- [选择缓存组件](#configuration)
- [使用原生接口](#native-interface)
- [缓存读取策略](#reads)
- [键与序列化](#keys)
- [更新后失效](#invalidation)
- [资源关闭](#ownership)

<a id="configuration"></a>

## 选择缓存组件

需要缓存的应用在装配入口直接选择官方 store。以下依赖只在应用使用 Redis 缓存时添加：

```bash
go get github.com/eko/gocache/lib/v4@v4.2.0
go get github.com/eko/gocache/store/redis/v4@v4.2.2
go get github.com/redis/go-redis/v9@v9.23.0
```

这些版本是一组明确的使用示例，不是框架强制安装的依赖。应用应通过自己的集成测试验证所选版本与部署后端。

内存缓存可以使用官方 go_cache store，按需要添加 `github.com/eko/gocache/store/go_cache/v4`。应用不需要缓存时，直接省略缓存的构造和注入。

连接地址、TTL 和命名空间由应用配置；没有框架级 driver 配置、默认 Redis 连接或全局 CacheManager。

<a id="native-interface"></a>

## 使用原生接口

业务服务可以接收上游接口，例如 `cache.CacheInterface[string]`。下面是完整的 Redis 写入示例：

```go
package cachedemo

import (
    "context"
    "time"

    "github.com/eko/gocache/lib/v4/cache"
    "github.com/eko/gocache/lib/v4/store"
    redisstore "github.com/eko/gocache/store/redis/v4"
    "github.com/redis/go-redis/v9"
)

func Put(ctx context.Context, addr string, value string) error {
    client := redis.NewClient(&redis.Options{Addr: addr})
    defer client.Close()

    c := cache.New[string](redisstore.NewRedis(client))
    return c.Set(ctx, "demo:message:v1:hello", value,
        store.WithExpiration(time.Minute),
    )
}
```

在长期运行的应用中，应在入口构造一次客户端并复用，退出时再关闭。内存 store 与 Redis store 各自遵循上游资源契约，应用负责其清理。

<a id="reads"></a>

## 缓存读取策略

对于可丢弃的查询缓存，可以采用 cache-aside：先读缓存，未命中时查数据库，再写回缓存。

| 情况 | 查询缓存策略 |
| --- | --- |
| 命中且数据可用 | 再检查资源所有权，然后返回 |
| store.NotFound | 正常回源，不当作后端故障 |
| 后端 Get 失败 | 记录 get 诊断，再回源 |
| JSON 解码失败 | 记录 decode 诊断，再回源 |
| Set 失败 | 记录 set 诊断，保留数据库查询结果 |

不要把所有 error 都当成 miss。身份或限流等用途需要自行确定后端失败策略，不能直接套用查询缓存的回源行为。

<a id="keys"></a>

## 键与序列化

键可以包含应用命名空间、资源类型、格式版本和资源 ID：

```text
<namespace>:note:v1:<id>
```

namespace 隔离应用和部署环境，v1 表示序列化布局。可以将对象编码为 JSON 字符串，避免假设每个 store 都能原样保存任意 Go 对象。

缓存命中不等于获得访问权限。服务必须检查缓存对象属于当前主体。

<a id="invalidation"></a>

## 更新后失效

业务更新可以在数据库事务提交后删除缓存键；事务回滚时保留原值。删除失败应记录诊断，成功的数据库提交不会因此回滚。

这是一种最终一致的查询缓存：并发读取可能在更新失效之后重新填入旧值，直到 TTL 到期。需要更强一致性时，根据业务增加版本控制等机制，或直接读取数据库。

<a id="ownership"></a>

## 资源关闭

Redis 客户端由创建它的入口关闭一次。内存组件如有过期清理任务，也需要按上游契约管理其生命周期；不要假设所有缓存实例都无需清理。

多个服务共享一个客户端时，服务不自行 Close。按构建依赖的逆序释放资源，具体做法见[资源生命周期](lifecycle.md#resources)。

---

[上一篇：数据库迁移](migrations.md) · [下一篇：本地化](localization.md)
