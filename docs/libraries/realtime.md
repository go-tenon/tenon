# 实时频道

用 WebSocket 实时频道做聊天、在线成员、“正在输入”这类双向的场景。`realtime` 在进程内嵌入 [Centrifuge](https://github.com/centrifugal/centrifuge)，客户端用 Centrifuge 的[官方 SDK](https://centrifugal.dev/docs/transports/client_sdk)，线路上的约定见[客户端协议](../http/protocol.md#realtime)。

包：`realtime`、`realtime/natspresence`、`realtime/realtimetest`。

- 频道就是[广播](broadcasting.md) Hub 的主题：业务代码照常用 `hub.PublishJSON` 发布，SSE 和 WebSocket 客户端都收到。只需单向推送时用[服务端推送](../http/sse.md)。
- 消息最多投递一次，没有历史：客户端重连后从应用的接口取回数据，见[断线与重连](#reconnecting)。

<a id="example-usage"></a>
## 示例

```go
rt, err := realtime.New(hub, realtime.Options{
    Authenticate: tokens.Validate,
    Accounts:     identity.Accounts{user.Type: roles.Resolve},
})
if err != nil {
    return err
}
rt.Channel("articles", realtime.Public())
router.Handle("/realtime", rt.Handler())

// 业务代码照常发布到 Hub，订阅了 articles 频道的客户端都会收到
err = hub.PublishJSON(ctx, "articles", ArticlePublished{ID: id})
```

`hub` 是入口创建的 `*broadcast.Hub`，`tokens` 是 `*apitoken.Store`，`roles.Resolve` 是[加载权限](../security/authorization.md#resolving-permissions)的 Resolver，`router` 是 HTTP 服务的 chi 路由。浏览器的订阅见[在浏览器中订阅](#subscribing)。

<a id="creating"></a>
## 在入口创建 `Realtime`

每个进程创建一个，在入口的 `Open` 中接着 Hub 创建，`stack` 是 `Open` 的 `*cleanup.Stack`：

```go
rt, err := realtime.New(hub, realtime.Options{ /* 见认证、在线成员 */ })
if err != nil {
    return nil, err
}
stack.MustAdd("realtime", rt.Shutdown) // 登记在 Hub 之后：先断开客户端，再关闭 Hub
```

选项不合法时 `New` panic。设置 `Options.MeterProvider` 后报告连接数和订阅数，见[可观测性](../general/telemetry.md#realtime)。

<a id="channels"></a>
## 用 `Channel` 登记公开频道

客户端只能订阅登记过规则的频道，Hub 上的其他主题只在服务端可见。`realtime.Public` 是公开频道，未登录的连接也能订阅：

```go
rt.Channel("articles", realtime.Public())
```

消息内容必须是 JSON。订阅时使用第一条匹配的规则。

<a id="private-channels"></a>
### 用 `Private` 授权私有频道

频道名可以带 `{name}` 占位符，匹配一段字母、数字、`_` 和 `-`。授权函数收到主体和占位符的值，返回能否订阅。`get` 是应用的业务动作 `article.Get`，能读取文章的人可以订阅它的频道：

```go
rt.Channel("article:{id}", realtime.Private(func(ctx context.Context, sub identity.Subject, p realtime.Params) (bool, error) {
    id, err := uuid.Parse(p["id"])
    if err != nil {
        return false, nil
    }
    _, err = get.Handle(ctx, sub, id)
    if errorx.KindOf(err) == errorx.NotFound {
        return false, nil
    }
    return err == nil, err
}))
```

- 只对登录的连接开放。返回 `false` 或业务错误表示拒绝；返回[系统故障](../general/errors.md#error-kinds)时 SDK 稍后重新订阅。
- 每 30 秒复查凭据时，已订阅的私有频道按重新加载的权限再授权一遍，不再允许的被退订。所以授权函数要快。
- 需要立即生效时（例如把用户移出会话），业务数据提交后调用 `rt.Unsubscribe(sub.Ref(), channel)`，它在所有实例上生效。

多租户应用的频道名带上租户，在授权函数中与 `sub.Tenant` 比较，见[缓存、广播与其他资源](../security/tenancy.md#other-resources)：

```go
rt.Channel("t:{tenant}:articles", realtime.Private(func(_ context.Context, sub identity.Subject, p realtime.Params) (bool, error) {
    return p["tenant"] == sub.Tenant && sub.Can("articles"), nil
}))
```

<a id="subscribing"></a>
## 在浏览器中订阅

页面安装 centrifuge-js（`bun add centrifuge`），连接 `Handler` 挂载的路径。同源的连接自动带上会话 cookie，不传 token：

```js
import { Centrifuge } from 'centrifuge';
const centrifuge = new Centrifuge(`wss://${location.host}/realtime`);

const articles = centrifuge.newSubscription('articles');
articles.on('publication', (ctx) => showNotice(ctx.data.title)); // ctx.data 是发布的 JSON
articles.on('unsubscribed', (ctx) => console.warn(ctx.code)); // 103 没有权限，102 没有这个频道
articles.subscribe();
centrifuge.connect();
```

React 页面收到消息后让查询失效，见 [React 客户端](../frontend/client-react.md)。

<a id="authentication"></a>
<a id="tokens"></a>
## 认证连接：`Authenticate` 与 `Accounts`

连接用 [API 令牌](../security/api-tokens.md)认证，`Authenticate` 通常是 `rest.RequireToken` 用的校验器。token 依次取 SDK 传入的（`new Centrifuge(url, { token })`）、`Authorization: Bearer` 请求头、[会话 cookie](../security/api-tokens.md#browser-sessions)；都没有的连接是匿名的，只能订阅公开频道。

- 权限由 `Accounts` 中这套账号的 Resolver 加载，与 [`rest.Options.Accounts`](../security/authorization.md#resolving-permissions) 相同。设置了 `Authenticate` 就必须设置 `Accounts`。
- token 无效、属于其他账号体系或 Resolver 拒绝主体时，连接以 3500 关闭，SDK 停止重连。连接期间每 30 秒重新校验，撤销 token、停用用户后连接结束。
- 会话 cookie 改了名字时，把同一个 `httpx.SessionCookie` 交给 `Options.SessionCookie`。
- 浏览器的连接只接受同源页面，其他来源加入 `Options.TrustedOrigins`（如 `https://app.example.com`）。带 `Authorization` 请求头的连接不检查来源，例如桌面端 `app://` 页面的宿主加上这个请求头即可。
- [多租户](../security/tenancy.md#identifying-tenants)应用设置 `Options.Tenant`，与 `rest.Options.Tenant` 相同：

```go
rt, err := realtime.New(hub, realtime.Options{
    Authenticate: tokens.Validate,
    Accounts:     identity.Accounts{user.Type: roles.Resolve},
    Tenant:       func(r *http.Request) string { return chi.URLParam(r, "tenant") },
})
router.Handle("/t/{tenant}/realtime", rt.Handler())
```

<a id="revocation"></a>
### 撤销后立即断开：`Credentials`

把 [`broadcast.Credentials`](../http/sse.md#immediate-revocation) 交给 `Options.Credentials`，退出登录、撤销 token 时照常调用 `Recheck*`，连接立即重新校验。改了用户的角色后调用 `RecheckSubject`，失去权限的私有频道随即退订。

<a id="presence"></a>
## 在线成员：`Presence` 与 `Members`

`realtime.Presence` 的频道记录订阅者，他们彼此可见。授权函数多返回一个值，是其他成员看到的资料；`users` 是应用的用户存储：

```go
rt.Channel("editing:{article}", realtime.Presence(func(ctx context.Context, sub identity.Subject, p realtime.Params) (any, bool, error) {
    u, err := users.Get(ctx, sub.ID)
    if err != nil {
        return nil, false, err
    }
    return map[string]string{"name": u.Name}, sub.Can("articles"), nil
}))
```

页面在 `subscribed` 中调用 `sub.presence()` 读取列表（每个连接一项，`user` 是主体的 Ref，例如 `user:42`），在 `join`、`leave` 事件中更新。

- 必须设置 [`Options.Presence`](#presence-stores)，否则 `Channel` panic。
- 事件按人发出：第一个连接加入时 `join`，最后一个连接离开时 `leave`；事件可能重复，按 `ctx.info.user` 处理。
- 离开后保留 10 秒（`Options.PresenceGrace`，最长 30 秒，负数不保留），刷新页面、闪断重连不产生事件。`rt.Unsubscribe` 立即发出 `leave`。
- 实例崩溃时没有 `leave`，记录一分钟内消失，页面要定期调用 `presence()` 刷新。

服务端用 `rt.Members` 查询，同一个主体的多个连接合并为一项：

```go
members, err := rt.Members(ctx, "editing:"+id.String())
// members[i].Subject（identity.Ref）、members[i].Info（JSON）、members[i].Connections
```

<a id="presence-stores"></a>
### 用 `natspresence` 保存在线成员

在线成员保存在所有实例共用的 [NATS](nats.md) JetStream KV 中。单实例也要设置：[不停机升级](../installation/operations.md#zero-downtime-upgrades)时新旧进程要看到同一份成员。`nc` 是[入口中创建的 NATS 连接](nats.md#connecting)：

```go
presence, err := natspresence.New(ctx, nc, natspresence.Options{Bucket: "app_presence"})
if err != nil {
    return nil, err
}
rt, err := realtime.New(hub, realtime.Options{Presence: presence})
```

- `Bucket` 区分应用和环境，同一个应用的实例必须相同。NATS 集群中把 `Replicas` 设为 3。
- 测试中用 `realtimetest.NewPresence()` 的内存存储，同一个交给多个 `Realtime` 就模拟了多个实例。

<a id="client-publish"></a>
## 客户端发布：`ClientPublish`

“正在输入”这类提示由客户端直接发布，`canEdit` 是应用的授权函数：

```go
rt.Channel("typing:{article}", realtime.Private(canEdit).ClientPublish())
```

- 只有已订阅的客户端可以发布，消息只发给 WebSocket 客户端，`hub.Subscribe` 收不到。
- 接收方只信任 `ctx.info`（发布者，由服务器填写），`ctx.data` 按不可信的数据处理；要保存的内容通过接口提交。

<a id="reconnecting"></a>
## 断线与重连

SDK 自动重连并重新订阅，断线期间的消息不补发。`subscribed` 在首次订阅和每次重连后都触发，在其中从应用的接口取回最新数据，例如 `articles.on('subscribed', reload)`。Hub 的后端可能丢了消息时（例如与 NATS 断开过），本实例的客户端被断开重连，走同一条路径。

<a id="deployment"></a>
## 部署

- 所有实例使用相同的 Hub 后端和 `Options.Presence`，连接可以落在任意实例上。
- 反向代理要转发 WebSocket 升级请求，空闲超时大于 25 秒的心跳间隔。
- 升级框架时 Centrifuge 的版本有变化，查看它的发布说明是否要求所有实例同时升级。

<a id="separate-process"></a>
### 用 `serve realtime` 单独部署实时连接

`/realtime` 挂在 HTTP 服务上时，每次升级都断开全部连接。接口发布频繁、连接很多时，给 `App` 加一个只承接实时连接的部分，升级接口时它保持在线。`a.rt` 是 `Open` 中创建的 `*realtime.Realtime`，`a.cfg.RealtimeAddr` 是新增的监听地址，例如 `:8081`：

```go
// Realtime 只承接实时频道的连接，在 RealtimeAddr 上监听。
func (a *App) Realtime(d *command.Daemon) (func(context.Context) error, error) {
    router := chi.NewRouter()
    router.Use(httpx.Defaults(a.cfg.HTTP)...)
    router.Handle("/realtime", a.rt.Handler())
    ln, err := d.Listen("tcp", a.cfg.RealtimeAddr)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return httpx.Serve(ctx, a.cfg.HTTP, ln, router)
    }, nil
}
```

在 `Parts` 中登记 `{Name: "realtime", Start: a.Realtime, Standalone: true}`：不带参数的 `serve` 不运行它，`serve realtime` 只运行它。

- 反向代理把 `/realtime` 转给这个进程，其余请求转给 HTTP 服务。带租户的路径（`/t/{tenant}/realtime`）在这里用同样的路径。
- 只改了接口时只[升级](../installation/operations.md#zero-downtime-upgrades) HTTP 服务；改了频道规则、授权函数或框架时再升级 `serve realtime`。
