# NATS

用 `natsx.Connect` 按框架的约定连接 NATS。每个进程一条连接，[队列](../background/queue.md)（JetStream）、[广播](broadcasting.md)（发布订阅）和[实时频道](realtime.md)的在线成员（JetStream KV）共用它；锁和限流计数在 PostgreSQL 中。

包：`natsx`。

<a id="deploying"></a>
## 部署

NATS 2.15 或更新的版本，启动时加 `-js` 开启 JetStream，见[队列的准备](../background/queue.md#preparing)。没有 `-js` 时队列和在线成员存储报错 JetStream 不可用。

<a id="connecting"></a>
## 连接 NATS：`natsx.Connect`

在应用配置中加入 `nats` 段：

```go
type Config struct {
    // ...
    NATS natsx.Config `yaml:"nats"`
}
```

```yaml
nats:
  url: ${NATS_URL:-nats://127.0.0.1:4222} # 默认本机；集群的多个地址用逗号分隔；用户名密码可写在地址中 nats://user:password@host:4222
  credentials: ""                         # 凭据文件（.creds）的路径，服务器使用 JWT 认证时设置
```

在入口打开共用资源的地方（见[依赖装配](../concepts/dependencies.md#running-parts)）创建连接，登记到 [`cleanup.Stack`](../concepts/dependencies.md#closing-resources)：

```go
nc, err := natsx.Connect(cfg.NATS, "blog") // 连接名出现在 NATS 的监控中；日志默认写到 slog.Default()
if err != nil {
    return nil, err
}
stack.MustAdd("nats", func(context.Context) error {
    nc.Close()
    return nil
})
```

就绪检查中加上它，连接正在重连时检查失败：

```go
{Name: "nats", Ping: natsx.Ping(nc)}
```

- NATS 不可达时 `Connect` 返回错误，进程在启动时失败。
- 连接断开后一直重连，每 10 秒探测一次连接。断开、重连和异步错误记在 `component=nats` 的日志中。
- 组件都不关闭连接，由 `stack` 在所有部分停止之后关闭。
- 用 `natsx.Connect`，不用 `nats.Connect`：后者默认重连 60 次后放弃，之后广播再也收不到消息；也不能加 `nats.NoEcho()`，广播的发布者要收到自己的消息。

<a id="using-with-components"></a>
## 传给队列、广播和在线成员

把同一个 `nc` 传给组件：

- 队列：`queue.NewWorker(db, nc, cfg.Queue)`、`queue.NewAdmin(nc, cfg.Queue)`，见[队列](../background/queue.md#running-workers)；
- 广播：`natsbroadcast.New(nc, prefix)`，见[广播](broadcasting.md#backends)；
- 在线成员：`natspresence.New(ctx, nc, opts)`，见[实时频道](realtime.md#presence-stores)。

多个应用或环境共用一套 NATS 时，用广播的 subject 前缀、在线成员的 bucket 名和队列的 stream 名区分它们。

<a id="other-accounts"></a>
## 连接另一个账号或另一套 NATS

调用另一个应用的 [RPC 端点](../rpc/endpoints.md)，而对方在另一个[账号](https://docs.nats.io/running-a-nats-service/configuration/securing_nats/accounts)或另一套 NATS 上时，用对方账号中的用户再建一条连接。例如商城调用博客应用：

```go
type Config struct {
    // ...
    NATS     natsx.Config `yaml:"nats"`
    BlogNATS natsx.Config `yaml:"blog_nats"` // 博客应用账号中的用户，调用它的 RPC
}
```

```go
blogNC, err := natsx.Connect(cfg.BlogNATS, "shop")
if err != nil {
    return nil, err
}
stack.MustAdd("blog-nats", func(context.Context) error {
    blogNC.Close()
    return nil
})
```

调用时传入这条连接，`GetInput`、`ArticleDTO`、`id` 和 `token` 见[调用端点](../rpc/endpoints.md#calling)：

```go
item, err := natsrpc.Call[ArticleDTO](ctx, blogNC, "blog.v1.articles.get", GetInput{ID: id}, natsrpc.Token(token))
```

队列、广播和在线成员继续用应用自己的 `nc`。

<a id="testing"></a>
## 测试

业务代码的测试用内存替身，不需要 NATS：广播用 `broadcasttest.NewBackend()`（见[广播的测试](broadcasting.md#testing)），在线成员用 `realtimetest.NewPresence()`（见[实时频道](realtime.md#presence-stores)）。

需要真实的 NATS 时，`queuetest.Conn(t)` 用环境变量 `NATS_TEST_URL` 的地址建立连接，测试结束时关闭；没有设置时测试失败。`queuetest.Config(t)` 返回使用随机 stream 的队列配置。广播的 subject 前缀、在线成员的 bucket 名也取随机值，测试就可以并行运行。
