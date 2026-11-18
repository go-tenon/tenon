# NATS

NATS 在 Tenon 中负责**消息的传递**：[队列](../background/queue.md)的任务保存在 JetStream 中；[广播](broadcasting.md)经由 NATS 的发布订阅把消息送到每个实例；[实时频道](realtime.md)的在线成员保存在 JetStream 的键值存储（KV）中。锁和限流这类低频的协调数据在 PostgreSQL 中，见[原子锁](locks.md#postgres-store)和[限流](rate-limiting.md#postgres-limiter)。

Tenon 使用官方的 [nats.go](https://github.com/nats-io/nats.go)，用 `natsx.Connect` 按框架的约定创建连接。每个进程一条连接，队列、广播和在线成员共用它；调用另一个账号中应用的 RPC 时另建一条，见[连接另一个账号或另一套 NATS](#other-accounts)。

阅读前需要了解：[依赖装配](../concepts/dependencies.md)。

- [部署](#deploying)
- [连接 NATS](#connecting)
- [在其他组件中使用](#using-with-components)
- [连接另一个账号或另一套 NATS](#other-accounts)
- [测试](#testing)
- [常见问题](#troubleshooting)

<a id="deploying"></a>
## 部署

部署 NATS 2.15 或更新的版本，启动时加上 `-js` 开启 JetStream，见[队列的准备](../background/queue.md#preparing)。广播只用发布订阅，在线成员的 KV bucket 保存在服务器内存中，它们都不需要额外的设置。

<a id="connecting"></a>
## 连接 NATS

在应用配置中加入 `nats` 段：

```go
type Config struct {
    // ...
    NATS natsx.Config `yaml:"nats"`
}
```

```yaml
nats:
  url: ${NATS_URL:-nats://127.0.0.1:4222} # 集群的多个地址用逗号分隔
  credentials: ""                         # NATS 凭据文件（.creds）的路径，服务器使用 JWT 认证时设置
```

用户名和密码可以写在地址中（`nats://user:password@host:4222`）。在入口打开共用资源的地方（见[依赖装配](../concepts/dependencies.md#running-parts)）创建连接，并登记到 [`cleanup.Stack`](../concepts/dependencies.md#closing-resources)：

```go
nc, err := natsx.Connect(cfg.NATS, "blog", nil)
if err != nil {
    return nil, err
}
stack.MustAdd("nats", func(context.Context) error {
    nc.Close()
    return nil
})
```

第二个参数是连接的名字，出现在 NATS 的监控中；第三个参数是日志器，`nil` 时使用 `slog.Default()`。`natsx.Connect` 立即连接，NATS 不可达时返回错误，进程在启动时就失败。连接的选项由框架决定：

- **一直重连**：nats.go 默认重连 60 次后放弃，连接就此关闭，广播从此收不到消息。
- **每 10 秒探测一次连接**：nats.go 默认 2 分钟，网络静默中断时要几分钟才能发现，期间其他实例广播的消息都丢了。
- **断开、重连和异步错误记录在日志中**，带有 `component=nats`。

连接可以并发使用，组件都不关闭它，由 `stack` 在所有部分停止之后关闭。

就绪检查中加上 NATS，连接正在重连时检查失败：

```go
{Name: "nats", Ping: natsx.Ping(a.nc)}
```

<a id="using-with-components"></a>
## 在其他组件中使用

把同一个 `nc` 传给需要它的组件：

- **队列**：`queue.NewWorker(db, nc, cfg.Queue, logger)`、`queue.NewAdmin(nc, cfg.Queue)`，见[队列](../background/queue.md#running-workers)；
- **广播**：`natsbroadcast.New(nc, prefix)`，见[广播](broadcasting.md#backends)；
- **在线成员**：`natspresence.New(ctx, nc, opts)`，见[实时频道](realtime.md#presence-stores)。

多个应用或环境共用一套 NATS 时，用广播的 subject 前缀、在线成员的 bucket 名和队列的 stream 名区分它们。

<a id="other-accounts"></a>
## 连接另一个账号或另一套 NATS

NATS 的[账号](https://docs.nats.io/running-a-nats-service/configuration/securing_nats/accounts)把 subject 隔离开：一条连接只能收发它所属账号里的消息。调用另一个应用的 [RPC 端点](../rpc/endpoints.md)，而对方在另一个账号中、或者在另一套 NATS 上时，用对方账号中的用户再建一条连接。

在应用配置中为这条连接加一段，例如商城调用博客应用：

```go
type Config struct {
    // ...
    NATS     natsx.Config `yaml:"nats"`
    BlogNATS natsx.Config `yaml:"blog_nats"` // 连到博客应用的账号，调用它的 RPC
}
```

```yaml
blog_nats:
  url: ${BLOG_NATS_URL:-nats://127.0.0.1:4222} # nats://用户名:密码@地址:4222，用户属于博客应用的账号
```

在打开共用资源的地方，像应用自己的连接一样创建它，并登记关闭：

```go
blogNC, err := natsx.Connect(cfg.BlogNATS, "shop", nil)
if err != nil {
    return nil, err
}
stack.MustAdd("blog-nats", func(context.Context) error {
    blogNC.Close()
    return nil
})
```

调用博客应用的端点时传入这条连接，`GetInput`、`ArticleDTO`、`id` 和 `token` 见[调用端点](../rpc/endpoints.md#calling)：

```go
item, err := natsrpc.Call[ArticleDTO](ctx, blogNC, "blog.v1.articles.get", GetInput{ID: id}, natsrpc.Token(token))
```

队列、广播和在线成员继续使用应用自己的连接 `nc`。`natsx.Connect` 在对方的 NATS 不可达时返回错误，进程启动失败。

<a id="testing"></a>
## 测试

测试业务代码时不需要 NATS：广播用 `broadcasttest.NewBackend()`，在线成员用 `realtimetest.NewPresence()`，都是内存替身，见[广播的测试](broadcasting.md#testing)和[实时频道](realtime.md#presence-stores)。

需要真实的 NATS 时，和[数据库测试](../testing/database.md#database-tests)一样，通过环境变量 `NATS_TEST_URL` 取得测试服务器的地址，`queuetest.Conn(t)` 用它建立连接，测试结束时关闭；每个测试使用随机的 subject 前缀和 bucket 名，测试之间互不干扰，可以并行运行。

<a id="troubleshooting"></a>
## 常见问题

**广播一段时间后再也收不到消息**

连接被关闭了：重连次数用完，或者被提前 `Close`。用 `natsx.Connect` 创建连接，它一直重连；连接由 `stack` 在进程退出时关闭，组件不关闭它，见[连接 NATS](#connecting)。

**发布者所在的实例收不到自己发布的消息**

连接不是用 `natsx.Connect` 创建的，并且使用了 `nats.NoEcho()`。广播需要回显，改用 `natsx.Connect`。

**创建在线成员存储时报错 JetStream 不可用**

NATS 启动时没有加 `-js`，见[部署](#deploying)。
