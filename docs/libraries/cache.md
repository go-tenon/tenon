# 缓存

用 Redis 缓存查询结果：`Remember` 先读缓存，没有时查库并写回；数据修改后用 `Forget` 或 tag 删除。

包：`cache`。

- Redis 只放随时可以从数据库重建的查询缓存：内存满时按淘汰策略回收，不需要持久化或备份。锁和限流计数放在 PostgreSQL 中，见[原子锁](locks.md)和[限流](rate-limiting.md)。
- 客户端直接用 [go-redis](https://github.com/redis/go-redis) 的 `*redis.Client`、`*redis.ClusterClient` 或 `redis.UniversalClient`，整个应用共用一个。

<a id="example-usage"></a>
## 示例

```go
articles := cache.New[*article.Article](rdb, "app:article:v1:", 10*time.Minute) // 入口中，每种值一个

item, err := articles.Remember(ctx, id.String(), func(ctx context.Context) (*article.Article, error) {
    return find(ctx, db, id) // 缓存中没有时才调用
})

err = articles.Forget(ctx, id.String()) // 修改后，在事务的 AfterCommit 中删除
```

`rdb` 是[入口中创建的客户端](#creating-the-client)，`db` 是入口打开的 `*bun.DB`，`find` 是直接查询数据库的函数。

<a id="assembling-cache"></a>
## 配置与装配

<a id="config-redis"></a>
### 配置：`url`、`default_ttl`

缓存段由应用自己定义：

```go
type Config struct {
    // ...
    Cache struct {
        URL        string        `yaml:"url"`
        DefaultTTL time.Duration `yaml:"default_ttl"`
    } `yaml:"cache"`
}
```

```yaml
cache:
  url: ${REDIS_URL:-redis://127.0.0.1:6379/0} # redis://[:password@]host:port[/db]，TLS 用 rediss://
  default_ttl: 10m                            # 默认过期时间，查询缓存通常几分钟到十几分钟
```

<a id="creating-the-client"></a>
### 创建客户端和 `Cache`

在入口打开共用资源的地方（见[依赖装配](../concepts/dependencies.md#running-parts)）创建客户端，登记关闭：

```go
opts, err := redis.ParseURL(cfg.Cache.URL)
if err != nil {
    return nil, err
}
opts.DialTimeout = 200 * time.Millisecond
opts.ReadTimeout = 200 * time.Millisecond // 默认 3s；缓存在请求路径上，Redis 变慢时尽快改查数据库
opts.WriteTimeout = 200 * time.Millisecond
rdb := redis.NewClient(opts)
stack.MustAdd("redis", func(context.Context) error { return rdb.Close() })

articles := cache.New[*article.Article](rdb, "app:article:v1:", cfg.Cache.DefaultTTL,
    cache.Missing(article.ErrNotFound.New))
```

- 同一种值只创建一个 `Cache`，像 `db` 一样传给业务动作，读取和删除都用它。
- `rdb` 为 `nil` 或 `ttl` 不为正时 `cache.New` panic，所以在入口中创建再传给模块的 `RegisterRoutes`：[生成 OpenAPI 文档](../http/openapi.md#exporting-openapi)时它收到的依赖都是 `nil`。
- `cache.Doctor(rdb)` 检查服务器连得上，见[自检](../installation/doctor.md#framework-checks)。

<a id="cache-aside"></a>
## 用 `Remember` 缓存查询结果

`Remember` 先读缓存，命中就返回；没有时调用加载函数，把结果写回缓存再返回。下面是“获取文章”的业务动作，`a.articles` 是构造函数传入的 `*cache.Cache[*Article]`，`a.db` 是 `*bun.DB`，`find` 直接查询数据库：

```go
func (a *Get) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Article, error) {
    item, err := a.articles.Remember(ctx, id.String(), func(ctx context.Context) (*Article, error) {
        return find(ctx, a.db, id)
    })
    if err != nil {
        return nil, err
    }
    if item.Status == StatusDraft && item.AuthorID != subject.ID {
        return nil, ErrNotFound.New() // 别人的草稿，当作不存在
    }
    return item, nil
}
```

键只传 ID，实际的键加上前缀，是 `app:article:v1:<id>`。每次读取得到独立的一份，可以修改。

> [!WARNING]
> 命中缓存之后仍要检查权限：缓存中的数据不带权限，草稿同样被缓存。修改数据时的权限检查用事务中读取的数据，见[在事务中检查](../security/authorization.md#checking-in-transactions)。

<a id="when-cache-fails"></a>
### 缓存出错时

缓存出错时 `Remember` 照常返回结果：

| 情况 | `Remember` 的处理 |
| --- | --- |
| 命中 | 解码后返回 |
| 没有命中 | 调用加载函数，结果写回缓存 |
| 读取失败（例如 Redis 不可用），或内容无法解码 | 记录 `warn` 日志，调用加载函数 |
| 加载函数返回错误 | 原样返回，不写缓存（`Missing` 声明的错误除外） |
| 写回失败（例如内存已满且禁止写入） | 记录 `warn` 日志，照常返回加载的结果 |

每个 `Cache` 每分钟最多记一条 `warn`，带上这期间省略的条数。Redis 停掉后接口变慢时，调短客户端的 `DialTimeout`、`ReadTimeout` 和 `WriteTimeout`，见[创建客户端](#creating-the-client)。

<a id="hot-keys"></a>
### 击穿、雪崩与穿透：`cache.Missing`

`Remember` 处理了查询缓存的三个常见问题：

- **击穿**：同一个进程中同一个键的并发加载合并为一次，其他调用等它的结果。一个调用者取消时只有它返回，加载继续。多个实例时每个实例最多同时加载一次。
- **雪崩**：写入时过期时间随机增加至多 10%，同一批写入的键错开过期。
- **穿透**：创建时传入 `cache.Missing(article.ErrNotFound.New)`，加载函数返回的错误与它匹配（`errors.Is`，`errorx` 的错误按错误码匹配）时，结果缓存约一分钟，期间直接返回一个新的同类错误。其他错误不缓存。

<a id="invalidation"></a>
## 提交事务之后删除缓存：`Forget`

数据修改后删除它的缓存，下一次读取重新加载。在事务中用 [`tx.AfterCommit`](../database/database.md#after-commit) 登记：在提交之前删除，并发的读请求会把旧数据写回缓存；事务回滚时缓存保持不变。`a.db`、`a.articles` 由构造函数传入：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    // ……加锁读取、检查权限、更新文章
    tx.AfterCommit(func(ctx context.Context) error {
        return errors.Wrap(a.articles.Forget(ctx, id.String()), "forget article cache")
    })
    return nil
})
```

`Forget` 是最终一致的：删除之前已经开始的加载（包括其他实例上的）可能在删除之后写回旧值，保留到过期。要求即时一致时用 [tag](#tags)，或者直接读数据库。

<a id="tags"></a>
## 一次删除一批：tag 与 `Invalidate`

一次修改影响很多键时给值加上 tag，例如按页缓存的文章列表，发布新文章影响每一页。`a.lists` 是 `*cache.Cache[[]Summary]`，`listPublished` 查询第 `n` 页：

```go
page, err := a.lists.Remember(ctx, strconv.Itoa(n), func(ctx context.Context) ([]Summary, error) {
    return listPublished(ctx, a.db, n)
}, "app:articles") // 最后的参数是这个值的 tag
```

发布文章的事务用 `tx.AfterCommit` 登记 `a.lists.Invalidate(ctx, "app:articles")`，提交后带这个 tag 的值一起失效。

- tag 是即时一致的：`Invalidate` 返回之后，`Remember` 只返回在它之后开始加载的值。值和 tag 的版本经由 pipeline 一次往返读取，单机和 Redis Cluster 均可用。
- tag 在共用同一个 Redis 的所有 `Cache` 之间共享，名字带上应用的命名空间。同一个键每次传入相同的 tag。

<a id="keys-and-serialization"></a>
## 缓存键

传给 `cache.New` 的前缀由命名空间、值的种类和格式版本组成，例如 `app:article:v1:`；读取和删除只传 ID。

- 命名空间区分共用一个 Redis 的应用和环境。
- 修改缓存值的结构时把 `v1` 改为 `v2`，新代码只读新格式，旧键过期后消失。
- 多租户应用的键带上租户，例如传入 `<tenant>:<id>`，见[缓存、广播与其他资源](../security/tenancy.md#other-resources)。

值以 JSON 保存。

<a id="deployment"></a>
## 部署

- 只作查询缓存时，配置 `maxmemory` 和淘汰策略（例如 `allkeys-lru` 或 `allkeys-lfu`），关闭持久化（`--save "" --appendonly no`）以减少磁盘开销。
- 高可用或分片部署使用 Redis Sentinel 或 Redis Cluster，用 `redis.NewUniversalClient`（按地址数量和 `MasterName` 自动选择单机、哨兵或集群）：

```go
rdb := redis.NewUniversalClient(&redis.UniversalOptions{
    Addrs:        cfg.Cache.Addrs,
    MasterName:   cfg.Cache.MasterName, // 哨兵模式设置主节点名，单机和集群留空
    Password:     cfg.Cache.Password,
    DialTimeout:  200 * time.Millisecond,
    ReadTimeout:  200 * time.Millisecond,
    WriteTimeout: 200 * time.Millisecond,
})
```

<a id="testing"></a>
## 测试

连接真实的 Redis。和[数据库测试](../testing/testing.md#database-tests)一样从环境变量取地址，每个测试用随机前缀，并行的测试互不影响：

```go
opts, err := redis.ParseURL(os.Getenv("REDIS_TEST_URL"))
if err != nil {
    t.Fatal(err)
}
rdb := redis.NewClient(opts)
t.Cleanup(func() { rdb.Close() })
articles := cache.New[*article.Article](rdb, "test:"+rand.Text()+":article:", time.Minute)
```

应用的测试覆盖自己的规则，例如修改之后读到新状态、缓存中有别人的草稿时仍返回 `404`。
