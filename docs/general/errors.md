# 错误处理

业务代码只返回错误。入口把错误转换为 HTTP 状态码、RPC 错误或命令的退出码，生成错误响应，并记录日志。

包：`errorx`。

| 失败 | 例子 | 写法 |
| --- | --- | --- |
| 输入格式错误 | 标题为空、slug 含有空格 | 在输入类型上写校验规则，框架在处理器之前返回 `422`，见[校验](../http/requests.md) |
| 可预期的业务结果 | 文章不存在、slug 已被使用 | 用 `errorx.Define` 定义业务错误，返回它的 `New` 或 `Wrap` |
| 意料之外的故障 | 数据库连不上、代码有 bug | 用 `errors.Wrap` 补充上下文后返回，客户端只收到 `500` |

代码中的 `errors` 是 [cockroachdb/errors](https://github.com/cockroachdb/errors)，与标准库的 `errors` 兼容，`errors.Wrap` 记录堆栈。

<a id="example"></a>
## 示例

修改文章的业务动作：只有作者能改，已发布文章的 slug 不能改，slug 不能与其他文章重复。`a.db` 是入口打开的 `*bun.DB`，`Changes` 是请求体，标题和 slug 的格式由校验规则检查。

```go
var (
    ErrNotFound   = errorx.Define(errorx.NotFound, "article.not_found", "文章不存在")
    ErrForbidden  = errorx.Define(errorx.PermissionDenied, "article.forbidden", "不能修改别人的文章")
    ErrSlugLocked = errorx.Define(errorx.InvalidArgument, "article.slug_locked", "已发布文章的 slug 不能修改")
    ErrSlugTaken  = errorx.Define(errorx.Conflict, "article.slug_taken", "slug“{{.slug}}”已被使用")
)

func (a *Update) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID, c Changes) (*Article, error) {
    item := new(Article)
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        err := tx.NewSelect().Model(item).Where("id = ?", id).For("UPDATE").Scan(ctx)
        if errors.Is(err, sql.ErrNoRows) {
            return ErrNotFound.New() // 可预期的结果
        }
        if err != nil {
            return errors.Wrap(err, "load article") // 意料之外的故障
        }
        if item.AuthorID != subject.ID {
            return ErrForbidden.New()
        }
        if item.Status == StatusPublished && c.Slug != item.Slug {
            return ErrSlugLocked.New()
        }
        item.Title, item.Slug = c.Title, c.Slug
        _, err = tx.NewUpdate().Model(item).Column("title", "slug", "updated_at").WherePK().Exec(ctx)
        if database.IsUniqueViolation(err, "articles_slug_key") {
            return ErrSlugTaken.Wrap(err, errorx.Public("slug", c.Slug)) // 可预期，保留底层原因
        }
        if err != nil {
            return errors.Wrap(err, "update article")
        }
        return nil
    })
    if err != nil {
        return nil, err
    }
    return item, nil
}
```

<a id="defining-errors"></a>
## 用 `errorx.Define` 定义业务错误

要查询数据或依赖业务状态才能判断的失败（文章不存在、slug 已被使用）定义为业务错误，错误模板保存为包级变量：

```go
var ErrSlugTaken = errorx.Define(errorx.Conflict, "article.slug_taken", "slug“{{.slug}}”已被使用")
```

- 第一个参数是分类，决定 HTTP 状态码和命令的退出码，见[选择错误分类](#error-kinds)。
- 第二个参数是错误码，客户端按它判断发生了什么。写成 `资源.原因`，全部小写，单词用下划线连接。
- 第三个参数（可选）是源语言的默认提示模板，支持 `{{.slug}}` 占位符。单语言项目直接写在这里即可，无需另建翻译文件；多语言项目在对应语言的翻译文件中用同一个错误码覆盖它，见[本地化](localization.md#translation-files)。
- 错误码在整个应用中唯一：`errors.Is` 和客户端都按错误码区分错误。

<a id="returning-errors"></a>
## 返回错误：`New`、`Wrap` 和 `errors.Wrap`

业务动作的 `Handle` 返回 `ErrNotFound.New()`；由底层错误引起的可预期失败（例如唯一约束冲突）返回 `ErrSlugTaken.Wrap(err)`，底层错误留在错误链中供排查；意料之外的故障返回 `errors.Wrap(err, "load article")`。完整的写法见[示例](#example)。

入口原样返回 `Handle` 的错误，不自己写状态码。HTTP 处理器的写法（`in` 是 Huma 解码的输入，`ArticleOutput` 是响应结构）：

```go
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*ArticleOutput, error) {
    subject, _ := identity.FromContext(ctx)
    item, err := a.Handle(ctx, subject, in.ID, in.Body)
    if err != nil {
        return nil, err
    }
    return &ArticleOutput{Body: toDTO(item)}, nil
}
```

- 查不到数据时返回业务错误：直接返回 `sql.ErrNoRows` 得到的是 `500`。
- 再包装业务错误时用 `errors.Wrap` 或 `fmt.Errorf` 的 `%w`：`errorx` 沿错误链找到业务错误；`%v` 会丢掉它，响应变成 `500`。
- 处理器直接返回 Huma 的错误（例如 `huma.Error404NotFound`）时原样输出，不带 `code`。需要稳定的错误码时用 `errorx`。

<a id="error-kinds"></a>
## 选择错误分类

分类描述失败的性质，与入口无关。业务代码只选择分类，各入口再把它转换为自己的形式。

<a id="status-mapping"></a>
### 分类对应的状态码和退出码

| 分类 | 什么时候使用 | 例子 | HTTP 状态码 | 退出码 |
| --- | --- | --- | --- | --- |
| `errorx.InvalidArgument` | 输入格式正确，但违反业务规则 | 修改已发布文章的 slug | 422 | 1 |
| `errorx.Unauthenticated` | 不知道调用者是谁 | 缺少或无效的 token | 401 | 1 |
| `errorx.PermissionDenied` | 知道是谁，但无权执行 | 修改别人的文章 | 403 | 1 |
| `errorx.NotFound` | 资源不存在 | 文章已被删除 | 404 | 1 |
| `errorx.Conflict` | 与当前状态冲突 | slug 已被其他文章使用 | 409 | 1 |
| `errorx.ResourceExhausted` | 配额或频率超出限制，等待后重试可能成功 | 登录尝试过于频繁，见[限流](../libraries/rate-limiting.md) | 429 | 1 |
| `errorx.Unavailable` | 依赖暂时不可用，稍后重试可能成功 | 图片存储服务超时 | 503 | 4 |
| `errorx.Internal` | 意料之外的内部错误 | 数据出现了不一致 | 500 | 5 |

- 不是业务错误的错误一律按 `Internal` 处理。
- `Unavailable` 和 `Internal` 是系统故障，`New` 和 `Wrap` 在调用处记录堆栈；其余分类是可预期的结果，不记录堆栈。
- 入口区分这两类时用 `errorx.KindOf(err).Systemic()`，不借用 HTTP 状态码。
- 退出码的常量见[退出码](commands.md#exit-codes)。

<a id="entry-mapping"></a>
### 其他入口怎样呈现业务错误

各入口只看分类，业务代码不用为入口做任何事：

- [MCP 工具](../ai/mcp.md)：业务错误返回错误码和翻译后的提示；系统故障只返回通用的提示和请求 ID。
- [实时频道](../libraries/realtime.md)：订阅授权返回系统故障时客户端稍后重新订阅，其他错误拒绝订阅。
- [队列任务](../background/queue.md#failures-and-retries)：系统故障和 `ResourceExhausted` 稍后重试，其他业务错误直接转入失败任务表。
- [命令](commands.md#error-messages)：显示翻译后的提示和错误码。
- 自己写的入口：[`i18n.ErrorMessage`](localization.md#outside-http) 取得与 `detail` 相同的译文，[`logging.RequestID`](logging.md#request-correlation) 取得请求 ID。

<a id="wrapping-errors"></a>
## 用 `errors.Wrap` 处理底层错误

- 底层错误代表一种可预期的结果时，用错误模板的 `Wrap` 转换为业务错误，例如唯一约束冲突转换为 `ErrSlugTaken.Wrap(err)`。业务错误不记录堆栈，错误码已经说明了发生什么。
- 其他底层错误在刚进入你的代码时用 `errors.Wrap(err, "load article")` 包装，之后每经过一层可以再补充上下文。数据库驱动和标准库的原始错误只有一行文本，错误日志要靠这里记下的堆栈才看得到失败的位置；`fmt.Errorf` 不记录堆栈。

<a id="public-parameters"></a>
## 用 `errorx.Public` 在提示中带上参数

`errorx.Public` 为错误附加参数，参数是翻译模板的数据：

```go
return ErrSlugTaken.Wrap(err, errorx.Public("slug", c.Slug))
```

```yaml
# locales/zh-CN.yaml
article.slug_taken: "slug“{{.slug}}”已被使用"
```

- 响应的 `detail` 是 `slug“hello-world”已被使用`，`params` 是 `{"slug": "hello-world"}`。
- 参数会展示给调用者，只附加可以公开的信息；值要能序列化为 JSON，否则响应省略 `params` 并记录警告。
- 名字区分大小写，没有同名参数时 `{{.slug}}` 原样出现在提示中。

<a id="retry-after"></a>
## 用 `errorx.RetryAfter` 建议等待时间

等待一段时间后就会消失的失败，例如[限流](../libraries/rate-limiting.md)，用 `errorx.RetryAfter` 附加建议的等待时间。HTTP 响应带上 `Retry-After` 头（秒，向上取整），队列任务按它重试：

```go
return ErrTooFrequent.New(errorx.RetryAfter(30 * time.Second))
```

<a id="field-errors"></a>
## 用 `errorx.Field` 指出出错的字段

业务错误属于某一个字段时，例如“选了 Anthropic 协议却没有填 Anthropic 兼容的地址”，用 `errorx.Field` 指出这个字段，表单把提示显示在这个输入框下面。路径相对于业务动作的输入，用点连接，数组下标写在方括号中：

```go
return ErrMissingURL.New(errorx.Field("base_urls.anthropic"))
```

- HTTP 和 [RPC](../rpc/protocol.md#errors) 的错误响应除了 `code` 和 `detail`，在 `errors` 中有这个字段的一项：`location` 是 `body.base_urls.anthropic`，`message` 与 `detail` 相同，`code` 是业务错误码。
- 前端的 `applyServerErrors` 把提示显示在字段下面；表单中没有这个字段时显示在表单上方。
- `errorx.FieldOf(err)` 取出字段。

<a id="inspecting-errors"></a>
## 判断错误：`errors.Is`、`errorx.CodeOf`、`errorx.KindOf`

`errors.Is` 按错误码判断错误是否来自某个错误模板，经过多层包装也成立：

```go
if errors.Is(err, article.ErrSlugTaken) {
    // 例如在 slug 后面加上序号再试一次
}
```

`errorx.CodeOf(err)` 和 `errorx.KindOf(err)` 沿错误链读取错误码和分类。不是业务错误时，`CodeOf` 返回空字符串，`KindOf` 返回 `Internal`。

<a id="response-format"></a>
## HTTP 响应格式

错误响应是 Problem Details（`application/problem+json`），字段和客户端的处理顺序见[客户端协议的错误](../http/protocol.md#errors)。对应用的影响：

- 业务错误带 `code`；附加了公开参数时带 `params`；用 `errorx.Field` 指出了字段时带 `errors`。
- 输入格式错误带 `errors`，没有 `code`，见[校验](../http/requests.md#validation-error-response)。
- `detail` 以错误码为消息 ID 翻译。当前语言缺少译文时用默认语言的译文，默认语言也没有时用状态码的描述（例如 `Not Found`），两种情况下访问日志都带上 `"untranslated":true`。
- 系统故障的 `detail` 只有状态码的描述，内部信息只留在日志中。

<a id="reporting-errors"></a>
## 错误日志

错误日志由入口记录一次，业务代码补充上下文后返回错误，不自己记录。

- HTTP 接口的访问日志每个请求一条，业务错误带 `code`。
- `5xx` 另有一条 `ERROR` 级别的错误日志，`error` 分组中是消息、错误码和含堆栈的错误链，见[错误日志](logging.md#error-logging)。
- 两条日志的 `request_id` 与响应头 `X-Request-ID` 相同。
- `httpx.AddAccessLogAttrs(ctx, attrs...)` 为访问日志追加字段。

<a id="cross-process-errors"></a>
## 跨进程传递错误

HTTP 接口和 RPC 的错误响应带着错误码、状态码和公开参数（[客户端协议](../http/protocol.md#errors)、[RPC 协议](../rpc/protocol.md#errors)）。Go 的客户端把它还原为 `errorx` 的错误，错误码、分类、参数和 `errors.Is` 的结果与服务端相同，例如 [`natsrpc.Call`](../rpc/endpoints.md#handling-errors)（`nc` 是 NATS 连接，`in` 是请求）：

```go
_, err := natsrpc.Call[ArticleDTO](ctx, nc, "blog.v1.articles.get", in)
errors.Is(err, article.ErrNotFound) // 服务端返回 article.not_found 时为 true
```

参数以 JSON 传输，还原后数值类型变为 `float64`。
