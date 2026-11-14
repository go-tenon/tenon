# 缓存

Tenon 的缓存建立在 [Memcached](https://memcached.org) 之上。对于查询代价高、读多写少的数据，你可以把查询结果放进缓存，之后的请求直接从缓存读取。

Memcached 只做缓存这一件事：内存满了就淘汰最久没用的数据，多个实例共享同一份缓存，缓存丢了也能随时从数据库重建。因此，锁、限流计数这类**必须保留**的数据放在 [Redis](redis.md) 中。

Tenon 不封装 Memcached 客户端，而是直接使用社区的 [gomemcache](https://github.com/bradfitz/gomemcache)。在它之上，`cachex` 包提供“先读缓存，没有就查库再写回”的读取方式，并替你处理热点过期、集中过期和不存在的数据这几个常见问题。

- [示例用法](#example-usage)
- [配置缓存](#assembling-cache)
    - [servers](#config-servers)
    - [default_ttl](#config-default-ttl)
    - [创建客户端](#creating-the-client)
- [读取缓存](#cache-aside)
    - [缓存出错时](#when-cache-fails)
    - [热点、集中过期与不存在的数据](#hot-keys)
- [删除缓存](#invalidation)
- [一次删除一批：tag](#tags)
- [缓存键](#keys-and-serialization)
- [部署](#deployment)
- [测试](#testing)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面是在业务动作中使用缓存最常见的写法：先从缓存读取文章，缓存里没有时才查询数据库，并把结果写回缓存。

```go
item, err := a.articles.Remember(ctx, id.String(), func(ctx context.Context) (*Article, error) {
    return find(ctx, a.db, id) // 只有缓存中没有时才会调用
})
```

文章被修改后，删除它的缓存，下一次读取时就会重新加载：

```go
err := a.articles.Forget(ctx, id.String())
```

这就是使用缓存的全部基础。下面几节依次介绍怎样配置缓存、`Remember` 在各种情况下会怎样处理，以及怎样正确地删除缓存。

<a id="assembling-cache"></a>
## 配置缓存

缓存的配置由应用自己定义。在应用的配置结构体中加入一个缓存段：

```go
type CacheConfig struct {
    Servers    []string      `yaml:"servers"`
    DefaultTTL time.Duration `yaml:"default_ttl"`
}

type Config struct {
    // ...
    Cache CacheConfig `yaml:"cache"`
}
```

对应的配置文件是这样的：

```yaml
cache:
  servers: ["10.0.0.5:11211"]
  default_ttl: 10m
```

<a id="config-servers"></a>
### servers

Memcached 服务器的地址列表，格式为 `host:port`。传入多个地址时，缓存键会分散到各个节点上，节点之间不复制数据。节点较多、需要扩缩容时，请参阅[部署](#deployment)中的一致性哈希。

<a id="config-default-ttl"></a>
### default_ttl

缓存的默认过期时间。过期时间越长，命中率越高，但数据被修改后旧值停留的时间也可能越长。对于大多数查询缓存，几分钟到十几分钟是合适的。

<a id="creating-the-client"></a>
### 创建客户端

在入口打开共用资源的地方创建客户端，并在应用退出时关闭它（见[依赖装配](../concepts/dependencies.md#running-parts)）：

```go
mc := memcache.New(cfg.Cache.Servers...)
mc.Timeout = 200 * time.Millisecond
stack.MustAdd("memcached", func(context.Context) error { return mc.Close() })
```

整个应用共享这一个 `mc`，它可以被并发使用。gomemcache 的方法不接收 `ctx`，每次操作的超时由 `Timeout` 控制，默认是 500 毫秒。缓存处在请求的路径上，所以超时应当设得短一些：Memcached 变慢时，与其等它，不如直接查数据库。

接着，为每一种要缓存的值创建一个 `cachex.Cache`。例如文章详情：

```go
articles := cachex.New[*article.Article](mc, "app:article:v1:", cfg.Cache.DefaultTTL,
    cachex.Missing(article.ErrNotFound.New))
```

第一个参数是客户端，第二个参数是缓存键的前缀（见[缓存键](#keys-and-serialization)），第三个参数是过期时间。`cachex.Missing` 是可选的，它的作用见[不存在的数据](#hot-keys)。

然后，把它像数据库一样传给需要它的业务动作：

```go
func NewGet(db *bun.DB, articles *cachex.Cache[*Article]) *Get {
    return &Get{db: db, articles: articles}
}
```

> [!IMPORTANT]
> `cachex.New` 遇到 `nil` 客户端会 panic，所以要在入口中创建 `Cache`，再传给模块的 `Register`：[生成 OpenAPI 文档](../http/routing.md#exporting-openapi)时，`Register` 收到的依赖都是 `nil`。

同一种值只创建一个 `Cache`，读取和删除都使用它。

<a id="cache-aside"></a>
## 读取缓存

`Remember` 实现了最常用的缓存模式 **cache-aside**：

1. 先读取缓存，命中就直接返回；
2. 没有命中时，调用你传入的加载函数查询数据库；
3. 把加载的结果写回缓存，再返回。

下面是“获取文章”使用缓存后的完整写法。`find` 是直接查询数据库的函数：

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

注意，传给 `Remember` 的只是文章 ID。`cachex` 会在它前面加上创建时的前缀，实际的缓存键是 `app:article:v1:<id>`。

> [!WARNING]
> 草稿同样会被缓存。所以命中缓存之后，**仍然要检查访问权限**，草稿只返回给作者本人。修改数据时的权限检查使用事务中读取的数据，见[在事务中检查](../security/authorization.md#checking-in-transactions)。

每次读取得到的都是独立的一份，可以随意修改。

<a id="when-cache-fails"></a>
### 缓存出错时

查询缓存随时可以从数据库重建，因此缓存出错时 `Remember` 照常返回结果：

| 情况 | `Remember` 的处理 |
| --- | --- |
| 命中 | 解码后返回 |
| 没有命中 | 调用加载函数，结果写回缓存 |
| 读取失败（例如 Memcached 不可用），或内容无法解码 | 记录 `warn` 日志，然后调用加载函数 |
| 加载函数返回错误 | 原样返回，不写缓存（`Missing` 声明的错误除外） |
| 写回失败（例如值超过 1 MB） | 记录 `warn` 日志，照常返回加载的结果 |

Memcached 不可用时，每个请求都会遇到读取失败。为了不让日志刷屏，每个 `Cache` 每分钟最多记录一条，并附上这期间省略的条数。出错的次数可以通过[指标](../general/telemetry.md)观察。

> [!NOTE]
> 这种“出错就当没有缓存”的处理，只适用于可以丢弃的查询缓存。这也是 Memcached 只存放这类数据的原因：令牌保存在数据库中，限流计数和锁保存在 [Redis](redis.md) 中。

<a id="hot-keys"></a>
### 热点、集中过期与不存在的数据

查询缓存有三个常见的问题。使用 `Remember` 时，它们都已经替你处理好了。

**击穿**：一篇热门文章的缓存过期的那一刻，大量请求同时发现缓存里没有，于是一起去查数据库。`Remember` 会把同一个进程中、同一个键的并发加载合并为一次，其他请求等待这次加载的结果。如果某个请求在等待时被取消，只有它自己返回，加载会继续，其他请求照常拿到结果。部署多个实例时，每个实例最多同时加载一次，这通常已经足够。

**雪崩**：同一批写入的大量缓存在同一时刻过期，数据库瞬间承受所有的加载。`Remember` 写入时会把过期时间随机增加至多 10%，让它们错开过期。

**穿透**：请求一个不存在的 ID 时，每次请求都会查询数据库。在创建 `Cache` 时用 `cachex.Missing` 声明“不存在”的错误，这样的请求也能命中缓存：

```go
cachex.Missing(article.ErrNotFound.New)
```

之后，加载函数返回的错误与它匹配（`errors.Is`）时，这个结果也会被缓存约一分钟，期间直接返回一个新的同类错误。`errorx` 的错误按错误码匹配，所以传入错误模板的 `New` 方法即可，上面的 `find` 保持原样。只有匹配的错误会被缓存。

<a id="invalidation"></a>
## 删除缓存

文章被修改后，你需要删除它的缓存，让下一次读取重新加载。删除应当放在**事务提交成功之后**：如果事务回滚了，数据库中的数据没有变，缓存也应当保持不变。

在事务中用 [AfterCommit](../database/quick-start.md#after-commit) 登记删除，它会在提交之后执行：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    // 加锁读取、检查权限、更新文章……
    tx.AfterCommit(func(ctx context.Context) error {
        return a.articles.Forget(ctx, id.String())
    })
    return nil
})
```

删除失败时，框架会记录一条 Error 日志，但数据库的修改仍然是成功的。

> [!NOTE]
> 这样的缓存是**最终一致**的。在删除之前就已经开始的一次加载，可能会在删除之后才把它读到的旧值写回缓存，这个旧值会一直保留到过期。所以过期时间应当是有限的；要求数据即时一致的场景，请直接读取数据库，或者使用下一节的 tag。

<a id="tags"></a>
## 一次删除一批：tag

有时一次修改会影响很多缓存。例如文章列表按页缓存，发布一篇新文章会影响每一页，而缓存了哪几页是未知的。

这时可以给缓存加上 tag。`Remember` 最后的参数就是这个值的 tag：

```go
page, err := a.lists.Remember(ctx, strconv.Itoa(n), func(ctx context.Context) ([]Summary, error) {
    return listPublished(ctx, a.db, n)
}, "app:articles")
```

发布文章的事务提交之后，调用 `Invalidate`，带有这个 tag 的所有缓存都会一起失效：

```go
tx.AfterCommit(func(ctx context.Context) error {
    return a.lists.Invalidate(ctx, "app:articles")
})
```

tag 本质上是一个版本号。`Remember` 读取值的同时读取它的 tag 的版本，版本变了就重新加载；`Invalidate` 只是把版本加一，与有哪些键无关。

tag 是即时一致的：`Invalidate` 返回之后，`Remember` 只返回在它之后加载的值。代价是每次读取都要额外读取 tag 的版本，而且一个值带的 tag 越多，任何一个 tag 失效都会让它重新加载。

> [!NOTE]
> tag 不属于某一个 `Cache`。使用同一个 Memcached 的 `Cache` 之间可以共用 tag，所以请给 tag 加上应用的命名空间，例如 `app:articles`。

<a id="keys-and-serialization"></a>
## 缓存键

一个缓存键建议由四部分组成：命名空间、资源类型、格式版本和资源 ID，例如 `app:article:v1:<id>`。前三部分作为前缀传给 `cachex.New`，读取和删除时只需要传入 ID。每一部分都有它的用处：

- **命名空间**区分不同的应用和部署环境。多个环境共用一个 Memcached 时，它们的缓存各自独立。
- **格式版本**用于修改缓存内容的结构。把 `v1` 改为 `v2` 后，新代码只读新格式的值，旧的键过期后自然消失。
- **租户**：多租户应用的键还要带上租户，例如传入 `<tenant>:<id>`，见[缓存、广播与其他资源](../security/tenancy.md#other-resources)。

Memcached 的键最长 250 字节，只能使用可见字符。超出这些限制的键（例如很长的搜索条件）由 `cachex` 自动换成 SHA-256 摘要。值以 JSON 编码保存。

<a id="deployment"></a>
## 部署

部署时有几件事需要留意。

**值的大小。** Memcached 默认单个值最大 1 MB，更大的值会写入失败。这时 `cachex` 记录日志后照常返回加载的结果，相当于没有缓存。如果你要缓存很大的列表，可以只缓存 ID，或者调大 Memcached 的 `-I` 参数。

**过期时间。** Memcached 按秒计时，`cachex` 会把过期时间向上取整到秒。

**多个节点。** `memcache.New` 传入多个地址时，键按哈希取模分配到各个节点。增减一个节点时，大部分键都会换到别的节点上，相当于一次大面积失效。需要扩缩容时，请改用 `cachex.NewRing` 提供的一致性哈希：

```go
ring, err := cachex.NewRing(cfg.Cache.Servers...)
if err != nil {
    return nil, err
}
mc := memcache.NewFromSelector(ring)
```

这样增加一个节点时，只有约 1/(N+1) 的键会换节点；去掉一个节点时，也只影响它自己的键。节点在环上的位置只由地址字符串决定，所以**所有实例的地址写法必须一致**，顺序则无关紧要。

> [!NOTE]
> 某个节点不可用时，它负责的键会读写超时，`cachex` 照常从数据库加载，这些键仍然固定在原来的节点上。

**内存。** 内存满时，Memcached 会淘汰最久没用的数据。这正是缓存需要的行为，保持默认配置即可。

<a id="testing"></a>
## 测试

测试缓存逻辑时，请连接真实的 Memcached。和[数据库测试](../testing/database.md#database-tests)一样，通过环境变量取得服务器的地址，并让每个测试使用随机的前缀，这样并行运行的测试相互隔离：

```go
func newTestCache(t *testing.T) *cachex.Cache[*article.Article] {
    t.Helper()
    addr := os.Getenv("MEMCACHED_TEST_ADDR")
    if addr == "" {
        t.Fatal("MEMCACHED_TEST_ADDR is not set")
    }
    mc := memcache.New(addr)
    t.Cleanup(func() { mc.Close() })
    return cachex.New[*article.Article](mc, "test:"+rand.Text()+":article:", time.Minute,
        cachex.Missing(article.ErrNotFound.New))
}
```

合并加载、过期时间的抖动和出错时的降级已经由框架测试过了。你的测试应当覆盖应用自己的规则，例如：

- 第一次读取查询数据库并写入缓存，第二次读取命中缓存；
- 发布文章之后，缓存被删除，再次读取得到新的状态；
- 缓存中有别人的草稿时，读取仍然返回 `404`；
- 不存在的文章返回 `404`。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/cachex`。

### New

```go
func New[T any](mc Client, prefix string, ttl time.Duration, opts ...Option) *Cache[T]
```

- **参数**
    - `mc`：Memcached 客户端，通常是 `*memcache.Client`
    - `prefix`：加在每个缓存键前面的前缀
    - `ttl`：过期时间，写入时随机增加至多 10%
    - `opts`：可选的设置，例如 `Missing`
- **返回**：一种值的缓存

创建一种值的缓存。`mc` 为 `nil` 时 panic。

### Missing

```go
func Missing(newErr func() error) Option
```

- **参数**
    - `newErr`：创建“不存在”错误的函数，通常是错误模板的 `New` 方法
- **返回**：传给 `New` 的设置

加载函数返回的错误与 `newErr()` 匹配时，这个结果也会被缓存约一分钟。

### Cache.Remember

```go
func (c *Cache[T]) Remember(ctx context.Context, key string, load func(ctx context.Context) (T, error), tags ...string) (T, error)
```

- **参数**
    - `key`：缓存键，不含前缀
    - `load`：缓存中没有时调用的加载函数
    - `tags`：这个值的 tag
- **返回**：缓存或加载得到的值；加载函数返回的错误

读取缓存，没有时调用 `load` 并写回。缓存出错时直接调用 `load`，见[缓存出错时](#when-cache-fails)。

```go
item, err := articles.Remember(ctx, id.String(), loadArticle)
```

### Cache.Forget

```go
func (c *Cache[T]) Forget(ctx context.Context, key string) error
```

- **参数**
    - `key`：缓存键，不含前缀
- **返回**：删除失败时的错误

删除一个缓存。

### Cache.Invalidate

```go
func (c *Cache[T]) Invalidate(ctx context.Context, tags ...string) error
```

- **参数**
    - `tags`：要失效的 tag
- **返回**：失败时的错误

让带有这些 tag 的所有值失效，见[一次删除一批：tag](#tags)。

### Client

`cachex` 用到的 Memcached 操作。`*memcache.Client` 实现了这个接口。

### NewRing

```go
func NewRing(servers ...string) (*Ring, error)
```

- **参数**
    - `servers`：Memcached 服务器的地址
- **返回**：一致性哈希的节点选择器；地址为空、无法解析或重复时返回错误

把返回值传给 `memcache.NewFromSelector`，见[部署](#deployment)。
