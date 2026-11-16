# NATS

NATS 在 Tenon 中负责**消息的传递**：[队列](../background/queue.md)的任务保存在 JetStream 中；[广播](broadcasting.md)经由 NATS 的发布订阅把消息送到每个实例；[实时频道](realtime.md)的在线成员保存在 JetStream 的键值存储（KV）中。锁和限流这类低频的协调数据在 PostgreSQL 中，见[原子锁](locks.md#postgres-store)和[限流](rate-limiting.md#postgres-limiter)。

Tenon 不封装 NATS 客户端，使用官方的 [nats.go](https://github.com/nats-io/nats.go)。队列的 worker 按[队列配置](../background/queue.md#configuration)自己连接；广播和在线成员使用应用在入口创建的连接，全应用共用一个。

阅读前需要了解：[依赖装配](../concepts/dependencies.md)。

- [部署](#deploying)
- [连接 NATS](#connecting)
- [在其他组件中使用](#using-with-components)
- [测试](#testing)
- [常见问题](#troubleshooting)

<a id="deploying"></a>
## 部署

部署 NATS 2.15 或更新的版本，启动时加上 `-js` 开启 JetStream，见[队列的准备](../background/queue.md#preparing)。广播只用发布订阅，在线成员的 KV bucket 保存在服务器内存中，它们都不需要额外的设置。

<a id="connecting"></a>
## 连接 NATS

在入口打开共用资源的地方（见[依赖装配](../concepts/dependencies.md#running-parts)），用队列配置中的地址和凭据创建连接，并登记到 [`cleanup.Stack`](../concepts/dependencies.md#closing-resources)：

```go
opts := []nats.Option{
    nats.Name("blog"),
    nats.MaxReconnects(-1),             // 一直重连
    nats.PingInterval(10 * time.Second), // 尽快发现中断的连接
}
if cfg.Queue.Credentials != "" {
    opts = append(opts, nats.UserCredentials(cfg.Queue.Credentials))
}
nc, err := nats.Connect(cfg.Queue.URL, opts...)
if err != nil {
    return nil, err
}
stack.MustAdd("nats", func(context.Context) error {
    nc.Close()
    return nil
})
```

- **`MaxReconnects(-1)`**：nats.go 默认重连 60 次后放弃，连接就此关闭，广播从此收不到消息。让它一直重连。
- **`PingInterval`**：默认每 2 分钟探测一次，网络静默中断时要几分钟才能发现，期间其他实例广播的消息都丢了。缩短间隔，更早发现、更早让客户端重连补发。
- **不要使用 `nats.NoEcho()`**：广播需要发布者所在的实例也收到自己发布的消息。

`nats.Connect` 会立即连接，NATS 不可达时返回错误。连接可以并发使用，整个应用共享这一个 `nc`。

<a id="using-with-components"></a>
## 在其他组件中使用

把同一个 `nc` 传给需要它的组件：

- **广播**：`natsbroadcast.New(nc, prefix)`，见[广播](broadcasting.md#backends)；
- **在线成员**：`natspresence.New(ctx, nc, opts)`，见[实时频道](realtime.md#presence-stores)。

多个应用或环境共用一套 NATS 时，用广播的 subject 前缀、在线成员的 bucket 名和队列的 stream 名区分它们。

<a id="testing"></a>
## 测试

测试业务代码时不需要 NATS：广播用 `broadcasttest.NewBackend()`，在线成员用 `realtimetest.NewPresence()`，都是内存替身，见[广播的测试](broadcasting.md#testing)和[实时频道](realtime.md#presence-stores)。

需要真实的 NATS 时，和[数据库测试](../testing/database.md#database-tests)一样，通过环境变量 `NATS_TEST_URL` 取得测试服务器的地址；每个测试使用随机的 subject 前缀和 bucket 名，测试之间互不干扰，可以并行运行。

<a id="troubleshooting"></a>
## 常见问题

**广播一段时间后再也收不到消息**

连接的重连次数用完后被关闭了。创建连接时设置 `nats.MaxReconnects(-1)`，见[连接 NATS](#connecting)。

**发布者所在的实例收不到自己发布的消息**

连接使用了 `nats.NoEcho()`。广播需要回显，去掉这个选项。

**创建在线成员存储时报错 JetStream 不可用**

NATS 启动时没有加 `-js`，见[部署](#deploying)。
