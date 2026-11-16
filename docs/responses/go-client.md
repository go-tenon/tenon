# Go 客户端

除了 TypeScript，`client` 命令还能生成 Go 客户端：其他 Go 服务、命令行工具和集成测试用它调用你的 API。和 TypeScript 客户端一样，函数按 OperationID 分组，类型与服务端一致，业务错误可以用 `errors.Is` 判断。

阅读前需要了解：[客户端代码](clients.md)；[错误处理](../general/errors.md)。

- [生成客户端](#generating-go-client)
- [调用接口](#calling-go-client)
- [错误](#go-client-errors)
- [认证](#go-client-auth)
- [事件流](#go-client-events)
- [上传文件](#go-client-uploads)
- [复制的类型](#copied-types)
- [常见问题](#troubleshooting)

用 Go 写的调用方，例如 [mygo](https://github.com/egoist/mygo) 写的桌面和移动 App、其他 Go 服务、命令行工具和端到端测试，使用 `clientgen` 直接从路由声明生成的客户端：

```go
a, err := c.Article.Update(ctx, id, article.Changes{Title: "Hello", Slug: "hello"})
if errors.Is(err, article.ErrSlugTaken) {
    // ...
}
```

接口还是同一套 JSON API，Go 客户端只是又一个调用方。和 OpenAPI 生成的代码相比，类型与服务端同名、同包（例如 `article.Changes`），业务错误码是可以 `errors.Is` 的常量。

<a id="generating-go-client"></a>
## 生成客户端

`client` 命令由 [`httpcmd.Commands`](../installation/tooling.md#app-side) 加入命令行。在 `tenon.json` 中按 `BasePath` 写上输出目录，`tenon generate` 和 `tenon dev` 就会一起生成这个 API 的 Go 客户端：

```json
{ "apis": { "/api": { "go": "../api" } } }
```

也可以在服务端的源码目录中直接运行，`--out` 是输出目录：

```bash
go run ./cmd/api client --out ../api
```

类型声明和错误码从源码复制，所以命令要在源码目录中运行；它用 `nil` 依赖构造应用的路由，离线运行。输出目录通常是 [go.work](https://go.dev/ref/mod#workspaces) 中的另一个模块，调用方只依赖它，App 保持精简：

```text
blog/
├── go.work
├── server/          服务端
├── api/             生成的客户端，go.mod 只需要 github.com/google/uuid 这类小库
│   ├── client.go    Client 和 Options
│   ├── article.go   Client.Article 的方法
│   ├── article/     服务端 internal/article 中接口用到的类型和错误码
│   ├── page/        框架的分页类型
│   └── wire/        发送请求、刷新凭据、解码错误的运行时
└── app/             调用方，例如 mygo App
```

目录名就是根包的包名。重新生成时，只删除上次生成、这次已经移除的文件，你自己放进去的文件保持不变。把生成的代码提交到仓库，CI 中重新生成后用 `git diff --exit-code` 检查是否忘了更新，与 [openapi.json](clients.md#checking-changes) 的做法相同。

返回原始字节或非 SSE 流的接口、`RawBody` 不是 `huma.MultipartFormFiles` 的接口不生成，命令把它们列在标准错误中。

<a id="calling-go-client"></a>
## 调用接口

```go
c := api.New(api.Options{
    BaseURL:    "https://api.example.com",
    Auth:       tokens,                           // 见下文“认证”
    AppVersion: "macos/1.4.2",                    // 见中间件的“App 最低版本”
    Language:   func() string { return "zh-CN" }, // 错误的 Detail 按它翻译
})

created, err := c.Article.Create(ctx, article.Changes{Title: "Hello", Slug: "hello"})
list, err := c.Article.List(ctx, article.ListInput{Status: article.StatusPublished})
for _, a := range list.Items { // page.Offset[article.ArticleDTO]
    // ...
}
```

方法按以下规则生成：

| 服务端 | 客户端 |
| --- | --- |
| operationId `article-publish` | `c.Article.Publish`；只有一段的 `ping` 是 `c.Ping` |
| 路径参数 | 按在路径中出现的顺序作为参数 |
| 只有 `Body` | `Body` 的类型作为最后一个参数 |
| 有查询参数或请求头 | 输入类型去掉路径参数后作为最后一个参数 |
| 输出只有 `Body` | 返回 `Body` 的类型 |
| 输出有响应头或 `Status` | 返回输出类型，例如 `created.Location`、`created.Body` |

`Body` 是匿名结构体时，生成的类型以输入或输出类型的名字加 `Body` 命名，例如 `RefreshInput` 的请求体是 `RefreshInputBody`。查询参数和请求头为零值时不发送，服务端使用默认值，所以 `article.ListInput{}` 取第一页、每页 20 条。

<a id="go-client-errors"></a>
## 错误

非 2xx 的响应返回 `*wire.Error`：`Status` 是状态码，`Code` 是业务错误码，`Detail` 是可以展示给用户的说明，`Fields` 是 `422` 时每个字段的问题（[翻译后的提示和规则 `code`](../http/validation.md#validation-error-response)），`Params` 是业务错误的[公开参数](../general/errors.md#public-parameters)，`RequestID` 用来报告问题，`RetryAfter` 是 `Retry-After` 响应头建议的等待时间。

服务端用 [`errorx.Define`](../general/errors.md#defining-errors) 定义的错误码，在对应的包中生成为同名常量，用 `errors.Is` 判断：

```go
switch {
case errors.Is(err, article.ErrSlugTaken):
    form.SetError("slug", "已被使用")
case errors.Is(err, identity.ErrUnauthenticated):
    showLogin()
case errors.Is(err, wire.ErrUpgradeRequired):
    showUpgrade()
}
```

框架自带的错误码同样会生成，例如 `identity.ErrUnauthenticated`、`ratelimit.ErrExceeded`、`page.ErrInvalidCursor`。

<a id="go-client-auth"></a>
## 认证

`Options.Auth` 提供受保护接口的 Bearer token；公开的接口直接请求。不透明的 [API token](../security/api-tokens.md) 用 `wire.StaticToken(token)`。[JWT 模式](../security/jwt.md#jwt)实现 `wire.Auth` 的两个方法，下面是把 refresh token 放在内存里的客户端写法（浏览器后台更常见的是 HttpOnly cookie，见[单账号后台](../security/admin-example.md#auth-api)）：

```go
type tokens struct {
    mu      sync.Mutex
    access  string
    refresh string
    public  *api.Client // 只调用公开的刷新接口
}

func (t *tokens) Token(context.Context) (string, error) {
    t.mu.Lock()
    defer t.mu.Unlock()
    return t.access, nil
}

func (t *tokens) Refresh(ctx context.Context) error {
    t.mu.Lock()
    refresh := t.refresh
    t.mu.Unlock()
    s, err := t.public.Auth.Refresh(ctx, authapi.RefreshInputBody{RefreshToken: refresh})
    if err != nil {
        return err
    }
    t.mu.Lock()
    defer t.mu.Unlock()
    t.access = s.AccessToken
    if s.RefreshToken != "" {
        t.refresh = s.RefreshToken
    }
    return nil // 同时保存到系统的钥匙串，例如 mygo 的 Secrets
}
```

受保护的接口返回 `401` 时，客户端调用一次 `Refresh`，再用新 token 重试一次。几个请求同时收到 `401` 时只刷新一次；`Refresh` 返回错误时，调用方收到原来的 `401`，这时让用户重新登录。

<a id="go-client-events"></a>
## 事件流

用 [`rest.RegisterSSE`](sse.md) 登记的接口返回事件的迭代器。每个接口生成一个事件结构体，每种事件一个指针字段，一次只有一个不为 `nil`；默认的 `message` 事件是 `Message` 字段：

```go
for ev, err := range c.Article.Events(ctx, article.EventsInput{}) {
    if err != nil {
        return err // 建立连接前的错误（例如 401）也从这里返回
    }
    switch {
    case ev.Changed != nil:
        refresh(*ev.Changed)
    case ev.Deleted != nil:
        remove(*ev.Deleted)
    }
}
```

服务端结束响应、收到 `data: [DONE]`、`ctx` 取消或者你跳出循环时，迭代结束。服务端新增的、客户端还不认识的事件被跳过，旧版本的 App 照常工作。连接中断时由调用方决定是否重连：需要接着上次的位置继续时，把最后一个事件的 `ID` 用 `wire.WithLastEventID` 放进 `ctx`，请求会带上 `Last-Event-ID`：

```go
events := c.Article.Events(wire.WithLastEventID(ctx, lastID), article.EventsInput{})
```

<a id="go-client-uploads"></a>
## 上传文件

`RawBody` 为 `huma.MultipartFormFiles[T]` 的接口（见[服务端上传接口](../libraries/file-uploads.md#rest-upload)），表单 `T` 作为最后一个参数；`huma.FormFile` 字段是 `wire.File`，其余字段原样保留。表单是匿名结构体时，类型以输入类型的名字加 `Form` 命名：

```go
f, err := os.Open("cover.png")
if err != nil {
    return err
}
defer f.Close()
uploaded, err := c.File.Upload(ctx, file.UploadInputForm{
    File: wire.File{Name: "cover.png", ContentType: "image/png", Body: f},
})
```

请求体边读边发送，内存占用与文件大小无关。`Body` 为 `nil` 的文件跳过。受保护的接口返回 `401` 时，文件内容实现了 `io.Seeker`（例如 `*os.File`、`bytes.Reader`）就在刷新凭据后从头重传；否则只刷新凭据，调用方收到 `401`，再调用一次即可。

<a id="copied-types"></a>
## 复制的类型

接口用到的类型复制到客户端：字段、tag、注释、泛型参数和同一个包中这个类型的常量都会保留，方法不复制，`json:"-"` 和未导出的字段去掉。只有应用模块和 Tenon 的类型会复制；标准库和 `github.com/google/uuid` 的类型直接导入。

| 情况 | 结果 |
| --- | --- |
| 类型实现了 `MarshalJSON`、`UnmarshalText` 等自定义编码 | 生成失败：复制字段得不到相同的 JSON。把它放进客户端可以导入的包，或者改用普通字段 |
| 用到了其他第三方包的类型 | 生成失败。确认那个包适合在客户端使用后，用 `--import 包路径` 允许导入，子包一并允许 |
| 两个包同名，例如应用的 `settings` 和框架的 `settings` | 生成失败。用 `--rename 包路径=新名字` 给其中一个改名 |

<a id="troubleshooting"></a>
## 常见问题

**`errors.Is(err, article.ErrSlugTaken)` 判断不出来**

检查导入的 `article` 是生成的客户端中的包，而不是服务端的包：生成的客户端在自己的包中复制了错误码常量，见[复制的类型](#copied-types)。

**所有受保护的接口都返回 401**

没有设置 `Options.Auth`，或者 `Token` 返回了空字符串，见[认证](#go-client-auth)。

**服务端加了字段，客户端读不到**

客户端是生成的代码，服务端改了接口后要重新运行 `client` 命令生成，见[生成客户端](#generating-go-client)。
