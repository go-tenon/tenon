# 缓存

- [简介](#introduction)
- [快速开始](#quickstart)
    - [安装](#installation)
    - [创建缓存实例](#creating-a-cache)
- [使用缓存](#using-the-cache)
    - [读取与未命中](#retrieving-items)
    - [写入与过期时间](#storing-items)
    - [删除与清空](#removing-items)
- [接入应用](#application-integration)
    - [配置](#configuration)
    - [在服务中使用](#using-in-services)
    - [在入口装配](#assembling-cache)
    - [关闭缓存客户端](#closing-clients)
- [使用内存缓存](#memory-cache)
- [缓存键与序列化](#keys-and-serialization)
- [缓存读取策略](#cache-aside)
- [缓存失效](#invalidation)
- [进阶使用](#advanced-usage)
    - [自动加载数据](#loadable-cache)
    - [自动序列化对象](#marshaler)
    - [按标签失效](#cache-tags)
    - [多级缓存](#chain-cache)
    - [缓存指标](#cache-metrics)
- [测试](#testing)
- [常见问题](#troubleshooting)

<a id="introduction"></a>
## 简介

对于查询代价较高、频繁读取但很少变化的数据，你可以把查询结果暂存在缓存中，让后续请求直接读取结果，减少数据库查询。

mygoravel 推荐直接使用 [eko/gocache](https://github.com/eko/gocache)。缓存依赖由应用按需安装，在入口创建后传给业务服务。一个缓存实例由三部分组成：

| 对象 | 职责 | Redis 示例 |
| --- | --- | --- |
| 客户端 | 连接存储服务，管理连接池 | `redis.NewClient(...)` |
| store | 把缓存操作转换为后端操作 | `redisstore.NewRedis(client, ...)` |
| cache | 提供带类型的读写方法 | `cache.New[string](redisStore)` |

业务代码通常使用最后一层的 `Get`、`Set` 和 `Delete`。切换到内存缓存时，替换客户端和 store 即可。本章先用 Redis 跑通基础操作，再把同一个缓存实例接入博客应用。

<a id="quickstart"></a>
## 快速开始

<a id="installation"></a>
### 安装

先创建一个独立的演示目录，初始化 Go module：

```bash
mkdir cache-demo
cd cache-demo
go mod init example.com/cache-demo
```

然后安装以下依赖：

```bash
go get github.com/eko/gocache/lib/v4@v4.2.0
go get github.com/eko/gocache/store/redis/v4@v4.2.2
go get github.com/redis/go-redis/v9@v9.23.0
```

本文的 API 和示例以这组版本为准。gocache 的核心库和各个 store 分别发布版本，上游主分支的示例可能使用不同的 API。内存后端的依赖见[使用内存缓存](#memory-cache)。

你需要一个可访问的 Redis。使用 Docker 时，可以启动一个仅供本地示例使用的实例：

```bash
docker run --name blog-cache-demo --rm -d -p 127.0.0.1:16379:6379 redis:7-alpine
docker exec blog-cache-demo redis-cli ping
```

第二条命令返回 `PONG` 后即可继续。不使用 Docker 时，将下面的 `REDIS_ADDR` 设置为你自己的 Redis 地址。

<a id="creating-a-cache"></a>
### 创建缓存实例

在刚才的 `cache-demo` 目录中创建 `main.go`，完整内容如下。它创建一次客户端，为缓存设置一分钟的默认 TTL，然后写入、读取、删除一个值，最后确认该键已经不存在：

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "log"
    "os"
    "time"

    "github.com/eko/gocache/lib/v4/cache"
    "github.com/eko/gocache/lib/v4/store"
    redisstore "github.com/eko/gocache/store/redis/v4"
    "github.com/redis/go-redis/v9"
)

func main() {
    if err := run(); err != nil {
        log.Fatal(err)
    }
}

func run() error {
    addr := os.Getenv("REDIS_ADDR")
    if addr == "" {
        addr = "127.0.0.1:16379"
    }
    client := redis.NewClient(&redis.Options{
        Addr:                  addr,
        ContextTimeoutEnabled: true,
    })
    defer client.Close()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := client.Ping(ctx).Err(); err != nil {
        return err
    }

    redisStore := redisstore.NewRedis(client, store.WithExpiration(time.Minute))
    c := cache.New[string](redisStore)

    // 缓存操作从这里开始。
    const key = "blog:demo:message:v1:hello"

    if err := c.Set(ctx, key, "Hello, cache!"); err != nil {
        return err
    }
    value, err := c.Get(ctx, key)
    if err != nil {
        return err
    }
    fmt.Println(value)

    if err := c.Delete(ctx, key); err != nil {
        return err
    }
    _, err = c.Get(ctx, key)
    if !errors.Is(err, store.NotFound{}) {
        return fmt.Errorf("expected a cache miss, got %v", err)
    }
    fmt.Println("cache miss")
    return nil
}
```

保存文件后，在 `cache-demo` 目录中补齐导入所需的依赖和校验记录，再运行：

```bash
go mod tidy
go run .
```

预期输出：

```text
Hello, cache!
cache miss
```

`redis.NewClient` 创建连接池对象，`Ping` 才检查是否能访问 Redis。`ContextTimeoutEnabled` 让客户端在执行命令时使用 context 的截止时间。`cache.New[string]` 表示业务代码读写字符串；它不会自动把结构体编码为 JSON，见[缓存键与序列化](#keys-and-serialization)。

完成本章的 Redis 示例后，可以用 `docker stop blog-cache-demo` 删除这个临时实例及其中的数据。

<a id="using-the-cache"></a>
## 使用缓存

下面的片段分别展示一个操作。要在快速开始中试运行，保留 `run()` 中客户端、`ctx` 和 `c` 的初始化，把“缓存操作从这里开始”之后、最后一个 `return nil` 之前的代码替换成所选片段，并保留末尾的 `return nil`。每次只替换一个片段，移除该次示例不再使用的导入，例如 `errors` 或 `fmt`。

`ctx` 是本次操作的上下文，`c` 是可复用的缓存实例；HTTP 请求中应继续传递请求的 context。

<a id="retrieving-items"></a>
### 读取与未命中

`Get` 返回缓存值和错误。键不存在或已过期时，返回 `store.NotFound`；网络连接失败等情况则返回其他错误。使用标准库 `errors.Is` 区分这两种情况：

```go
value, err := c.Get(ctx, "blog:demo:message:v1:hello")
switch {
case errors.Is(err, store.NotFound{}):
    value = "默认消息"
case err != nil:
    return err
}
fmt.Println(value)
```

不要通过 `value == ""` 判断未命中：空字符串也可以是有效的缓存内容。上面的默认值只用于本次返回，不会自动写入缓存。

如果需要查看剩余存活时间，具体的 `*cache.Cache[string]` 还提供 `GetWithTTL`。下面先写入再读取，因此可以独立替换到快速开始中运行：

```go
if err := c.Set(ctx, "blog:demo:message:v1:hello", "Hello, cache!"); err != nil {
    return err
}
value, ttl, err := c.GetWithTTL(ctx, "blog:demo:message:v1:hello")
if err != nil {
    return err
}
fmt.Println(value, ttl)
```

运行后会输出 `Hello, cache!` 和接近一分钟的剩余 TTL。Redis 的剩余 TTL 按秒返回，不适合测量亚秒精度。

> [!NOTE]
> `GetWithTTL` 不属于 `cache.CacheInterface[T]`；需要它时，可使用具体的 `*cache.Cache[T]` 或 `cache.SetterCacheInterface[T]`。Redis 中没有过期时间时，go-redis 返回 `-1*time.Nanosecond`。读取值与读取 TTL 是两次命令，期间键可能变化。

<a id="storing-items"></a>
### 写入与过期时间

`Set` 写入一个键，已有同名键时覆盖它的值和过期时间。TTL 使用 Go 的 `time.Duration`，例如 `30*time.Second`，不要用没有单位的整数表示秒数：

```go
if err := c.Set(ctx, "blog:demo:message:v1:hello", "新消息",
    store.WithExpiration(30*time.Second),
); err != nil {
    return err
}
```

Redis store 可以设置默认 TTL，单次 `Set` 的选项会覆盖默认选项。快速开始中设置的默认值是一分钟，因此省略选项时使用一分钟：

```go
if err := c.Set(ctx, "blog:demo:message:v1:hello", "使用默认 TTL"); err != nil {
    return err
}
```

对于本文使用的 Redis store，显式设置 `store.WithExpiration(0)` 表示不过期；创建 store 时也没有配置 TTL，则默认不过期。查询结果通常应设置正数 TTL，作为数据变更或失效失败后的兜底。删除键请使用 `Delete`，不要依赖负数 TTL。

> [!NOTE]
> 过期时间的特殊值由后端解释。内存后端的 `0` 表示使用客户端默认 TTL，见[使用内存缓存](#memory-cache)，不能直接套用 Redis 的规则。

<a id="removing-items"></a>
### 删除与清空

删除单个键：

```go
if err := c.Delete(ctx, "blog:demo:message:v1:hello"); err != nil {
    return err
}
```

本文的 Redis 和内存后端删除一个不存在的键也会返回 `nil`，因此可以在数据修改后直接删除，不必提前读取。

`Clear(ctx)` 用于清空后端中的数据，其范围取决于 store。只有在确认缓存后端可整体清空时才使用它：

```go
if err := c.Clear(ctx); err != nil {
    return err
}
```

> [!WARNING]
> 本文的 Redis store v4.2.2 调用的是 `FLUSHALL`，会清空该 Redis 实例的**所有数据库**，不只限于配置的 DB，也不受键前缀限制。不要对共享 Redis 实例使用此操作。内存后端的 `Clear` 只清空该内存客户端中的项目。实现见[固定版本的 Redis store](https://github.com/eko/gocache/blob/store/redis/v4.2.2/store/redis/redis.go)。

<a id="application-integration"></a>
## 接入应用

快速开始用于单独体验 API。接下来回到已有博客应用的 module 根目录，执行[安装](#installation)中的三条 `go get` 命令，再按下面的顺序修改应用文件。博客入口的完整代码见[依赖装配](dependencies.md#assembling-the-application)。

接入分为三步：定义配置、让服务接收缓存、在入口创建并传入缓存。客户端在应用运行期间复用，退出时统一关闭。

<a id="configuration"></a>
### 配置

缓存配置由应用自己定义。下面是完整的 `internal/settings/cache.go`：

```go
package settings

import (
    "errors"
    "time"
)

type CacheConfig struct {
    Addr       string        `yaml:"addr"`
    Password   string        `yaml:"password"`
    DB         int           `yaml:"db"`
    DefaultTTL time.Duration `yaml:"default_ttl"`
}

func (c *CacheConfig) SetDefaults() {
    c.Addr = "127.0.0.1:16379"
    c.DefaultTTL = 10 * time.Minute
}

func (c CacheConfig) Validate() error {
    if c.Addr == "" {
        return errors.New("addr is required")
    }
    if c.DB < 0 {
        return errors.New("db must not be negative")
    }
    if c.DefaultTTL <= 0 {
        return errors.New("default_ttl must be positive for query caching")
    }
    return nil
}
```

在已有的 `internal/settings/config.go` 中，为 `Config` 增加 `Cache` 字段。下面的注释代表已有字段，合并时保留它们：

```go
type Config struct {
    // 原有的 HTTP、日志、数据库等配置字段。
    Cache CacheConfig `yaml:"cache"`
}
```

然后在 `config/app.yaml` 中追加：

```yaml
cache:
  addr: ${REDIS_ADDR:-127.0.0.1:16379}
  password: ${REDIS_PASSWORD:-}
  db: 0
  default_ttl: 10m
```

`config.Load` 会调用 `SetDefaults` 和 `Validate`。在 YAML 中新增 `cache` 段前，先在根配置结构体中加入对应字段，否则严格解码会拒绝未知字段；已有字段但省略 YAML 配置时，会使用默认值，见[配置](configuration.md#loading-configuration)。这里要求正数 TTL 是博客查询缓存的应用约定；Redis 本身也支持不过期的键。

如果 Redis 使用 ACL 用户、TLS 或其他连接选项，在应用配置中增加所需字段并传给原生 `redis.Options`。

<a id="using-in-services"></a>
### 在服务中使用

修改 `internal/article/service.go` 中已有的服务定义和构造函数，使它接收缓存接口。下面展示需要的导入和定义，已有业务方法继续保留：

```go
package article

import (
    "github.com/eko/gocache/lib/v4/cache"
    "github.com/uptrace/bun"
)

type Service struct {
    db    *bun.DB
    cache cache.CacheInterface[string]
}

func NewService(db *bun.DB, c cache.CacheInterface[string]) *Service {
    return &Service{db: db, cache: c}
}
```

该接口提供 `Get`、`Set`、`Delete`、`Invalidate`、`Clear` 和 `GetType`，定义见[gocache v4.2.0](https://github.com/eko/gocache/blob/lib/v4.2.0/lib/cache/interface.go)。后续可传入 Redis 或内存缓存。

这是对博客示例的可选扩展：接入后，入口和测试中的 `article.NewService(db)` 都要改为传入缓存参数。服务要求一个可用的缓存对象，不要传入 `nil` 来表示关闭缓存。

<a id="assembling-cache"></a>
### 在入口装配

在 `internal/bootstrap/serve.go` 已有的 `import` 块中加入：

```go
"github.com/eko/gocache/lib/v4/cache"
"github.com/eko/gocache/lib/v4/store"
redisstore "github.com/eko/gocache/store/redis/v4"
"github.com/redis/go-redis/v9"
```

然后在 `Serve` 函数中，找到数据库初始化之后的 `articles := article.NewService(db)`，用下面整段代码替换这一行。`cfg` 是传入的应用配置，`db` 是已经打开的数据库，`stack` 是函数开头声明的 `cleanup.Stack`；保留原有的退出清理和后续 HTTP 启动代码：

```go
client := redis.NewClient(&redis.Options{
    Addr:                  cfg.Cache.Addr,
    Password:              cfg.Cache.Password,
    DB:                    cfg.Cache.DB,
    ContextTimeoutEnabled: true,
})
stack.MustAdd("redis", func(context.Context) error {
    return client.Close()
})

redisStore := redisstore.NewRedis(client,
    store.WithExpiration(cfg.Cache.DefaultTTL),
)
articleCache := cache.New[string](redisStore)
articles := article.NewService(db, articleCache)
```

这里将缓存视为可选的查询加速设施，启动时不以 `Ping` 成功作为必要条件；运行时的读取和写回故障按[缓存读取策略](#cache-aside)处理。如果你的业务必须依赖 Redis 才能启动，可以在登记关闭操作后增加带超时的 `Ping`，并返回启动错误。

缓存连接只在 `serve` 等需要它的命令中创建，`openapi`、`routes` 等文档模式不需要访问 Redis。

至此完成了缓存的创建和注入。要让文章接口实际使用缓存，继续加入[缓存读取策略](#cache-aside)中的 `cache.go` 并修改 `Get`，再按[缓存失效](#invalidation)调整写操作。

完成代码和导入的修改后，在博客应用根目录运行 `go mod tidy`，再用原来的 `serve` 命令启动应用。

<a id="closing-clients"></a>
### 关闭缓存客户端

Redis 客户端由创建它的入口关闭，并且只关闭一次。多个缓存实例或服务可以共享同一个客户端，业务服务本身不关闭它。上面的 `cleanup.Stack` 会在 HTTP 服务停止处理请求后关闭客户端，见[关闭资源](dependencies.md#closing-resources)。普通 `cache.Cache` 和 Redis store 没有需要额外调用的 `Close`。

带后台任务的缓存组合需要分别处理生命周期，具体见[自动加载](#loadable-cache)、[多级缓存](#chain-cache)和[缓存指标](#cache-metrics)。

<a id="memory-cache"></a>
## 使用内存缓存

开发、单进程应用或业务测试可以使用进程内缓存，不需要 Redis 服务。它不在多个进程之间共享，重启后数据丢失；多个应用副本需要共享数据时，使用 Redis 等外部存储。

安装本文验证的内存后端版本：

```bash
go get github.com/eko/gocache/lib/v4@v4.2.0
go get github.com/eko/gocache/store/go_cache/v4@v4.2.2
go get github.com/patrickmn/go-cache@v2.1.0+incompatible
```

在入口的导入中加入下面两个包，保留 gocache 的 `cache` 包。用内存示例替换后，移除不再使用的 `github.com/eko/gocache/lib/v4/store`、`github.com/eko/gocache/store/redis/v4` 和 `github.com/redis/go-redis/v9` 导入：

```go
gocachestore "github.com/eko/gocache/store/go_cache/v4"
memory "github.com/patrickmn/go-cache"
```

将[入口装配](#assembling-cache)中从 `client := redis.NewClient(...)` 到 `articles := article.NewService(...)` 的整段代码替换为：

```go
memoryClient := memory.New(cfg.Cache.DefaultTTL, cfg.Cache.DefaultTTL)
articleCache := cache.New[string](gocachestore.NewGoCache(memoryClient))
articles := article.NewService(db, articleCache)
```

`memory.New` 的第一个参数是默认 TTL，第二个参数是后台清理间隔。这里沿用应用的默认 TTL 作为清理间隔；过期键会立即变为未命中，占用的内存由后台定期清理。应用配置中只有 `default_ttl` 会用于这个内存后端，Redis 地址等连接参数不再参与初始化。

整段替换也移除了 Redis 的关闭登记。内存客户端没有公开的 `Close`，后台任务由库在客户端被垃圾回收后停止，不保证立即停止。[测试](#testing)中将第二个参数设为 `0`，可以避免创建后台任务；读取仍会检查过期时间，但不会定期清理占用的内存。

| TTL 设置 | Redis store v4.2.2 | go_cache store v4.2.2 |
| --- | --- | --- |
| `Set` 省略 TTL | 使用 store 默认值；未配置则不过期 | 使用 `memory.New` 的默认 TTL |
| `store.WithExpiration(30*time.Second)` | 30 秒后过期 | 30 秒后过期 |
| `store.WithExpiration(0)` | 不过期 | 使用内存客户端默认 TTL |
| 明确要求不过期 | `store.WithExpiration(0)` | `store.WithExpiration(memory.NoExpiration)` |

> [!NOTE]
> go_cache store v4.2.2 的构造选项不会应用到 `Set`，因此请像示例一样在 `memory.New` 上设置默认 TTL，或在每次 `Set` 时显式传入 TTL。不要依赖 `NewGoCache(client, store.WithExpiration(...))` 设置默认值。行为见[该版本源码](https://github.com/eko/gocache/blob/store/go_cache/v4.2.2/store/go_cache/go_cache.go)。

<a id="keys-and-serialization"></a>
## 缓存键与序列化

缓存键建议使用可读的字符串，包含应用与环境、资源类型、格式版本和资源 ID，例如：

```text
blog:prod:article:v1:<id>
```

不同应用或环境共用 Redis 时，应使用不同前缀。修改序列化格式后可以把 `v1` 改成 `v2`：新代码不再读取旧键，旧键仍需等待 TTL 到期或主动清理。键前缀只用于组织与隔离名称，不会限制 `Clear` 的范围。

本章 Redis 示例使用 `cache.New[string]`，因为该 store 的 `Get` 返回字符串。要缓存结构体，可以先编码为 JSON，再把 JSON 字符串写入缓存。按[使用缓存](#using-the-cache)说明替换快速开始中的操作片段，增加标准库 `encoding/json` 导入；该例使用 `fmt`，不再使用 `errors`：

```go
type ArticleSummary struct {
    ID    string `json:"id"`
    Title string `json:"title"`
}

key := "blog:demo:article-summary:v1:1"
item := ArticleSummary{ID: "1", Title: "第一篇文章"}
data, err := json.Marshal(item)
if err != nil {
    return err
}
if err := c.Set(ctx, key, string(data), store.WithExpiration(time.Minute)); err != nil {
    return err
}

raw, err := c.Get(ctx, key)
if err != nil {
    return err
}
var restored ArticleSummary
if err := json.Unmarshal([]byte(raw), &restored); err != nil {
    return err
}
fmt.Println(restored.Title)
```

Redis 和内存后端都使用同一种 JSON 字符串格式，切换后端时业务代码无需改变。也可以使用 gocache 的 [Marshaler](#marshaler)，它采用不同的编码格式和调用方式。

> [!NOTE]
> 泛型参数不是序列化器。lib v4.2.0 的 `Cache[T].Get` 遇到后端返回类型与 `T` 不匹配时，会返回 `T` 的零值和 `nil` 错误。因此不要直接把 Redis 缓存声明为 `cache.New[Article]` 或 `cache.New[[]byte]` 来读取这些字符串，也不要混用不同类型的内存缓存操作同一批键。

<a id="cache-aside"></a>
## 缓存读取策略

对于数据库查询结果，常用流程是：先读缓存；未命中时查询数据库；查询成功后写回缓存。这个模式称为 **cache-aside**。

查询缓存可以从数据库重建，所以可以在缓存故障时回退到数据库。各分支的行为如下：

| 情况 | 处理方式 |
| --- | --- |
| 命中且内容有效 | 返回数据，继续执行业务权限检查 |
| 未命中 | 查询数据库 |
| 读取失败或内容无法解码 | 记录错误，查询数据库 |
| 数据库查询失败或未找到 | 返回业务错误，不写入缓存 |
| 编码或写回失败 | 记录错误，仍返回数据库结果 |

下面是完整的 `internal/article/cache.go` 文件，加入已有的 `article` 包。`Service` 使用前面的构造函数，`Article` 来自[数据库模型](database.md#defining-models)，`ErrNotFound` 来自[业务错误定义](errors.md#defining-errors)。文件中的 `find` 是直接查询数据库的方法；如果你已经定义了它，保留一份即可。

```go
package article

import (
    "context"
    "database/sql"
    "encoding/json"
    "log/slog"

    "github.com/cockroachdb/errors"
    "github.com/eko/gocache/lib/v4/store"
    "github.com/google/uuid"
)

func cacheKey(id uuid.UUID) string {
    return "blog:article:v1:" + id.String()
}

func (s *Service) find(ctx context.Context, id uuid.UUID) (*Article, error) {
    var item Article
    err := s.db.NewSelect().Model(&item).Where("id = ?", id).Scan(ctx)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, ErrNotFound.New()
    }
    if err != nil {
        return nil, errors.Wrap(err, "load article")
    }
    return &item, nil
}

func (s *Service) load(ctx context.Context, id uuid.UUID) (*Article, error) {
    key := cacheKey(id)
    raw, err := s.cache.Get(ctx, key)
    switch {
    case err == nil:
        var item Article
        if decodeErr := json.Unmarshal([]byte(raw), &item); decodeErr != nil {
            slog.WarnContext(ctx, "decode article cache", "key", key, "error", decodeErr)
        } else if item.ID == id {
            return &item, nil
        } else {
            slog.WarnContext(ctx, "discard mismatched article cache", "key", key)
        }
    case !errors.Is(err, store.NotFound{}):
        slog.WarnContext(ctx, "read article cache", "key", key, "error", err)
    }

    item, err := s.find(ctx, id)
    if err != nil {
        return nil, err
    }
    data, err := json.Marshal(item)
    if err != nil {
        slog.WarnContext(ctx, "encode article cache", "key", key, "error", err)
        return item, nil
    }
    if err := s.cache.Set(ctx, key, string(data)); err != nil {
        slog.WarnContext(ctx, "write article cache", "key", key, "error", err)
    }
    return item, nil
}

func (s *Service) invalidate(ctx context.Context, id uuid.UUID) {
    if err := s.cache.Delete(ctx, cacheKey(id)); err != nil {
        slog.WarnContext(ctx, "invalidate article cache", "article_id", id, "error", err)
    }
}
```

这个文件通过入口设置的默认 TTL 控制查询结果的有效期。`cacheKey` 中的 `blog` 是示例命名空间；多个环境共享 Redis 时，换成各环境独立的前缀，或将前缀作为应用配置传入服务。

在[授权章节的 `Get`](authorization.md#ownership) 中，把 `s.find(ctx, id)` 改成 `s.load(ctx, id)`，身份和所有权检查保留在原位置。下面是修改后的方法片段，需要 `github.com/ryan053857/mygoravel/identity`、`uuid`、`context` 包以及该章节定义的 `ErrForbidden`、`StatusDraft`：

```go
func (s *Service) Get(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Article, error) {
    if subject.ID == "" {
        return nil, ErrForbidden.New()
    }
    item, err := s.load(ctx, id)
    if err != nil {
        return nil, err
    }
    if item.Status == StatusDraft && item.AuthorID != subject.ID {
        return nil, ErrNotFound.New()
    }
    return item, nil
}
```

> [!WARNING]
> 缓存命中后仍要检查访问权限。写操作的授权应使用事务中读取的当前数据，见[事务中的权限检查](authorization.md#checking-in-transactions)，不要使用可能过期的缓存决定是否允许修改。

上面的降级策略用于可丢弃的查询缓存。它仍会增加一次缓存访问的延迟；应按请求预算配置 Redis 客户端的连接、读写超时，并保留错误日志。缓存大量失效时，数据库也必须能承受回源流量。

<a id="invalidation"></a>
## 缓存失效

数据更新后，删除对应缓存，让下次读取重新加载。对于数据库事务，删除操作应放在**事务成功提交之后**；回滚时保持原缓存。

在[发布文章](authorization.md#checking-in-transactions)的 `Publish` 方法中，保留 `RunInTx` 内部的查询、权限检查和更新逻辑，把事务返回后的结尾替换为：

```go
// err 是 s.db.RunInTx(...) 的返回值，item 是事务中更新后的文章。
if err != nil {
    return nil, err
}
s.invalidate(ctx, id)
return item, nil
```

修改内容、删除文章等操作也应在成功后调用 `invalidate`。如果还缓存了文章列表，列表键也需要删除；只删除详情键不会刷新列表。

删除缓存失败时，前面的 `invalidate` 会记录日志，已经提交的数据库修改仍然成功。此时旧内容可能保留到 TTL 到期。

这是一种最终一致的策略：并发读请求可能在更新删除缓存之后，又把之前读到的旧值写回。因此应设置合理 TTL；如果业务不能接受这段时间内的旧数据，应直接读取数据库，或另行设计带版本检查的更新策略。

<a id="advanced-usage"></a>
## 进阶使用

基础读写可以满足大部分查询缓存需求。下面介绍 gocache v4.2.0 提供的组合能力；各示例独立使用，不需要把所有层叠加在一起。函数体片段可以按[使用缓存](#using-the-cache)说明替换到快速开始中；`ctx`、`c` 和 `client` 沿用初始化代码。新增导入后执行 `go mod tidy`，补齐对应功能的依赖。注明“入口初始化”的片段则放在应用入口，并使用创建出的对象。

<a id="loadable-cache"></a>
### 自动加载数据

如果多个调用点都需要“先读缓存，读不到就加载”，可以用 `cache.NewLoadable` 集中声明加载函数。函数接收键，返回数据、这次写入使用的选项和错误。

下面的函数片段需要 `context`、`fmt`、`time`、`github.com/eko/gocache/lib/v4/cache` 和 `github.com/eko/gocache/lib/v4/store`。`c` 是已经创建的字符串缓存；示例用 map 表示数据来源，实际应用可以在加载函数里查询数据库：

```go
source := map[string]string{
    "blog:demo:message:v1:welcome": "欢迎阅读博客",
}
load := func(ctx context.Context, key any) (string, []store.Option, error) {
    if err := ctx.Err(); err != nil {
        return "", nil, err
    }
    k, ok := key.(string)
    if !ok {
        return "", nil, fmt.Errorf("expected string key, got %T", key)
    }
    value, ok := source[k]
    if !ok {
        return "", nil, fmt.Errorf("message %q does not exist", k)
    }
    return value, []store.Option{store.WithExpiration(time.Minute)}, nil
}

loaded := cache.NewLoadable[string](load, c)
defer loaded.Close()

value, err := loaded.Get(ctx, "blog:demo:message:v1:welcome")
if err != nil {
    return err
}
fmt.Println(value)
```

同一实例中，同一个键的并发加载会合并执行。加载成功后，数据在后台写回缓存，因此 `Get` 返回时，底层缓存不一定已经写入。

在本文版本中，任何缓存读取错误都会触发加载，后台写回错误也不会返回给调用方。如果需要分别记录缓存读取和写回失败，请使用前面的手写读取策略。应用退出时，先停止使用该实例，再调用一次 `Close`，等待待写入任务结束，最后关闭 Redis 客户端。`Close` 不会关闭底层客户端。见 [Loadable 实现](https://github.com/eko/gocache/blob/lib/v4.2.0/lib/cache/loadable.go)。

<a id="marshaler"></a>
### 自动序列化对象

如果不需要让缓存内容保持可读的 JSON，可以使用 `marshaler.New`。它通过 MessagePack 编码对象，并在读取时填充你提供的目标指针。

下面的片段需要 `time`、`github.com/eko/gocache/lib/v4/cache`、`github.com/eko/gocache/lib/v4/marshaler`、`github.com/eko/gocache/lib/v4/store`，以及别名为 `redisstore` 的 Redis store 包。替换到快速开始中运行时，保留 `client` 和 `ctx` 的初始化，移除原来的 `redisStore := ...` 和 `c := ...` 两行；这里会创建自己的对象缓存：

```go
type ArticleSummary struct {
    ID    string
    Title string
}

objects := marshaler.New(cache.New[any](redisstore.NewRedis(client)))
key := "blog:demo:article-summary:msgpack-v1:1"

if err := objects.Set(ctx, key, ArticleSummary{ID: "1", Title: "第一篇文章"},
    store.WithExpiration(10*time.Minute),
); err != nil {
    return err
}

var summary ArticleSummary
if _, err := objects.Get(ctx, key, &summary); err != nil {
    return err
}
```

这里使用 `cache.New[any]`，因为编码器写入的是字节，而 Redis 读取返回字符串。`Marshaler.Get` 需要目标指针，它不实现 `cache.CacheInterface[T]`，不能直接替换文章服务中的字符串缓存。JSON 与 MessagePack 应使用不同的键格式版本。见 [Marshaler 实现](https://github.com/eko/gocache/blob/lib/v4.2.0/lib/marshaler/marshaler.go)。

<a id="cache-tags"></a>
### 按标签失效

当多个缓存项应当一起失效时，可以在写入时为它们添加相同的标签。例如不同页码的文章列表，可以共享 `blog:demo:articles` 标签。更新文章后，按该标签删除相关缓存，而不必逐一维护列表键。

下面的片段沿用前面创建的 `c`，需要导入 `time` 和 `github.com/eko/gocache/lib/v4/store`：

```go
if err := c.Set(ctx, "blog:demo:article-list:v1:page:1", "[1,2]",
    store.WithExpiration(5*time.Minute),
    store.WithTags([]string{"blog:demo:articles"}),
); err != nil {
    return err
}

if err := c.Invalidate(ctx,
    store.WithInvalidateTags([]string{"blog:demo:articles"}),
); err != nil {
    return err
}
```

一个缓存项可以有多个标签，按其中任意一个标签失效都会删除该项。标签名也应包含应用和环境信息，避免不同应用共用相同标签。

Redis 适配器另存标签到键的索引。本文版本的标签索引写入和部分失效错误不会向调用方返回；索引本身也可能过期或被 Redis 淘汰。因此标签适合批量清理查询缓存，仍应给缓存项设置有限的 TTL，不能仅凭 `Invalidate` 返回 `nil` 判断所有项已经删除。具体行为见 [redis/v4 v4.2.2](https://github.com/eko/gocache/blob/store/redis/v4.2.2/store/redis/redis.go)。

<a id="chain-cache"></a>
### 多级缓存

如果同一进程反复读取同一批数据，可以先查询进程内缓存，再查询 Redis，以减少网络请求。`cache.NewChain` 按传入顺序读取；下层命中后，会在后台把值和剩余 TTL 回填到上层。

下面的入口初始化片段需要 `time`、`github.com/eko/gocache/lib/v4/cache`，以及别名为 `memory` 的 `github.com/patrickmn/go-cache`、别名为 `gocachestore` 的 go_cache store 包和别名为 `redisstore` 的 Redis store 包。`client` 是复用的 Redis 客户端：

```go
local := memory.New(time.Minute, 5*time.Minute)
chain := cache.NewChain[string](
    cache.New[string](gocachestore.NewGoCache(local)),
    cache.New[string](redisstore.NewRedis(client)),
)
```

之后用 `chain.Get`、`chain.Set` 等方法代替普通缓存操作。写入时应设置有限的正数 TTL，并让各层使用相同的值类型。`Set` 会向每一层写入；一个进程删除缓存不会主动通知其他进程清空它们的内存缓存，因此各实例仍可能短暂读到旧值。

本文版本的 `Chain` 在读取一层失败时继续查询下一层，后台回填及 `Delete`、`Invalidate`、`Clear` 不会返回各层错误。它还没有 `Close` 方法，后台任务持续到进程退出，应在入口创建一次并复用，不要按请求创建。需要严格确认失效结果的场景，优先使用单层 Redis 缓存。见 [Chain 实现](https://github.com/eko/gocache/blob/lib/v4.2.0/lib/cache/chain.go)。

<a id="cache-metrics"></a>
### 缓存指标

需要观察缓存命中和读写情况时，可以用 `cache.NewMetric` 包装普通缓存。gocache 内置 Prometheus provider。

下面的入口初始化片段需要 `github.com/eko/gocache/lib/v4/cache`、`github.com/eko/gocache/lib/v4/metrics`、`github.com/prometheus/client_golang/prometheus` 和 `github.com/prometheus/client_golang/prometheus/promhttp`；`c` 必须是前面创建的普通 `*cache.Cache[string]`：

```go
registry := prometheus.NewRegistry()
provider := metrics.NewPrometheus("blog", metrics.WithRegisterer(registry))
measured := cache.NewMetric[string](provider, c)
handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
```

把 `measured` 传给需要缓存的服务，把 `handler` 挂到应用的指标路由，例如 `router.Handle("/metrics", handler)`。`router` 是入口中的 chi 路由器；指标路由的访问权限由你的部署决定。

指标按 store 类型和操作区分，包含命中、未命中及写入、删除、失效的成功和错误次数。本文版本把所有读取错误都计入 `miss_count`，它不能替代缓存故障日志；写操作统计在后续读取触发更新时上报。provider 有后台记录任务且没有 `Close` 方法，应在入口创建一次并复用。见 [Metric](https://github.com/eko/gocache/blob/lib/v4.2.0/lib/cache/metric.go) 和 [Prometheus provider](https://github.com/eko/gocache/blob/lib/v4.2.0/lib/metrics/prometheus.go)。

<a id="testing"></a>
## 测试

先用内存后端验证业务逻辑，再用实际部署的 Redis 验证连接、类型、TTL 和标签行为。内存后端无法模拟 Redis 的网络错误或全部过期规则。

下面是可独立保存运行的 `cache_test.go`，使用[内存后端](#memory-cache)安装的依赖：

```go
package main

import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/eko/gocache/lib/v4/cache"
    "github.com/eko/gocache/lib/v4/store"
    gocachestore "github.com/eko/gocache/store/go_cache/v4"
    memory "github.com/patrickmn/go-cache"
)

func TestCache(t *testing.T) {
    ctx := context.Background()
    c := cache.New[string](gocachestore.NewGoCache(memory.New(time.Minute, 0)))
    const key = "blog:test:message:v1:hello"

    if _, err := c.Get(ctx, key); !errors.Is(err, store.NotFound{}) {
        t.Fatalf("expected miss, got %v", err)
    }
    if err := c.Set(ctx, key, "hello"); err != nil {
        t.Fatal(err)
    }
    value, err := c.Get(ctx, key)
    if err != nil || value != "hello" {
        t.Fatalf("value=%q, error=%v", value, err)
    }
    if err := c.Delete(ctx, key); err != nil {
        t.Fatal(err)
    }
    if _, err := c.Get(ctx, key); !errors.Is(err, store.NotFound{}) {
        t.Fatalf("expected miss after delete, got %v", err)
    }
}
```

保存文件后，在该 module 目录中运行：

```bash
go mod tidy
go test -v .
```

在博客业务测试中，每个测试创建独立内存客户端，把生成的缓存传给 `article.NewService(db, c)`；数据库测试仍使用 [dbtest](testing.md#database-tests)。重点检查以下行为：

- 首次读取从数据库加载，后续读取命中缓存；不存在的文章不会写入缓存。
- 预先写入坏 JSON 后，读取会回到数据库并修复内容。
- 使用返回错误的测试替身模拟缓存读取、写回或删除失败，确认数据库结果仍能正常返回。
- 数据修改提交后删除缓存；事务失败时不删除；下一次读取获得新值。
- 缓存中有草稿时，其他用户仍无法访问。

Redis 测试使用独立实例和测试键；如果需要验证 `Clear`，必须使用允许整体清空的临时实例。修改依赖版本后，重新运行这些测试，尤其检查返回类型、默认 TTL 和后台任务的行为。

<a id="troubleshooting"></a>
## 常见问题

| 现象 | 检查方向 |
| --- | --- |
| 连接被拒绝或认证失败 | Redis 是否就绪；地址、密码、DB 和客户端选项是否正确；可用短超时 `Ping` 单独验证连接 |
| 读取返回空值却没有错误 | 是否存入了空字符串；`Cache[T]` 的 `T` 是否与后端返回类型一致；本版本不会报告类型断言失败 |
| 看起来总是未命中 | 键前缀与 ID 是否一致；TTL 是否过短；是否在每次请求时重新创建内存客户端 |
| TTL 与预期不同 | 是否混淆 `time.Duration` 单位；是否依赖了内存 store 构造选项；`0` 在所选后端中代表什么 |
| JSON 或 MessagePack 解码失败 | 读写两端是否使用相同格式；结构是否改变；格式升级时是否更新了键版本 |
| 更新数据库后仍读到旧值 | 是否在事务提交后删除了全部相关键；删除是否失败；是否发生并发旧值回填；多级缓存是否还有其他进程的本地副本 |
| 标签失效后仍有内容 | 标签名是否一致；标签索引是否过期或被淘汰；该版本的标签操作不会返回所有底层错误 |

排查时保留操作名、键和错误信息，避免把缓存的业务内容或连接密码写入日志。进一步查阅实现时，请使用本文链接的版本标签，而不是默认假设上游主分支与已安装版本一致。
