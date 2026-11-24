# 请求与校验

接口的输入类型是一个 Go 结构体：字段的标签说明值从哪里来、要满足什么校验规则。框架在调用处理器之前解析请求并校验，处理器拿到的是已经填好、校验通过的结构体；校验失败返回 `422`。

包：`httpx/rest`、`contract`、`enum`。

- 标签上的规则描述格式（必填、长度、范围、正则），只在 HTTP、RPC、MCP 这些入口检查；需要查数据才能判断的规则写在 `Handle` 中，见[业务规则校验](#business-validation)。
- 校验规则写入 OpenAPI 文档，生成的前端表单使用同一份规则。

<a id="introduction"></a>
## 示例

一个输入类型同时读取路径参数、查询参数、请求头和 JSON 请求体：

```go
type UpdateInput struct {
    ID      uuid.UUID `path:"id"`
    DryRun  bool      `query:"dry_run"`
    Version string    `header:"X-App-Version"`
    Body    struct {
        Title string `json:"title" minLength:"1" maxLength:"200"`
    }
}

func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*ArticleOutput, error) {
    // in.ID 已经是 uuid.UUID，in.Body.Title 已经通过校验
}
```

`PUT /articles/{id}?dry_run=true` 到达处理器时，`in.ID` 已经解析为 `uuid.UUID`，`in.DryRun` 是 `true`。ID 不是合法的 UUID 或者标题为空时，请求在处理器之前返回 `422`。

<a id="input-types"></a>
## 定义输入类型

| 标签或字段 | 来源 | 默认 |
| --- | --- | --- |
| `path:"id"` | 路径参数 `/articles/{id}` | 总是必填 |
| `query:"limit"` | 查询参数 `?limit=50` | 可选 |
| `header:"X-Client-Version"` | 请求头 | 可选 |
| `cookie:"name"` | cookie | 可选 |
| 字段名 `Body` | JSON 请求体 | 其中的字段必填 |

标签由 Huma 定义，完整列表见 [Huma 文档](https://huma.rocks/features/request-inputs/)。`doc`、`example` 只写入接口文档，见[说明与示例](openapi.md#describing-apis)。

<a id="path-parameters"></a>
### 路径参数

```go
type GetInput struct {
    ID uuid.UUID `path:"id"`
}
```

标签中的名字与路径中 `{}` 里的名字相同，区分大小写。值无法转换为字段类型（例如不是合法的 UUID）时返回 `422`。

<a id="query-parameters"></a>
### 查询参数

```go
type SearchArticlesInput struct {
    Q     string `query:"q"`
    Limit int    `query:"limit" default:"20" minimum:"1" maximum:"100"`
}
```

`?limit=50` 时 `in.Limit` 是 50，省略时是默认值 20，`?limit=500` 返回 `422`。名字区分大小写，写错时总是得到默认值。

- 需要对超出范围的值做截断处理时，去掉 `maximum`，在业务动作中截断，并在接口说明中写明。
- 列表的分页参数嵌入 `page.OffsetParams` 或 `page.CursorParams`，见[分页](responses.md#pagination)。

<a id="headers"></a>
### 请求头

```go
type RequestInput struct {
    ClientVersion string `header:"X-Client-Version"`
}
```

`Authorization` 头由 `rest.RequireToken` 读取，处理器用 `identity.FromContext(ctx)` 取出调用者，见[认证](../security/authentication.md)。

<a id="json-body"></a>
### 校验请求体：`Body` 字段

输入类型中名为 `Body` 的字段是 JSON 请求体，校验规则写在它的字段上。请求体通常是模块共用的结构体，创建、修改接口和[表格导入](../features/import-export.md)都用它，规则只写一次，见[数据对象](../concepts/actions.md#data-objects)：

```go
type Changes struct {
    Title   string `json:"title" minLength:"1" maxLength:"200"`
    Slug    string `json:"slug" maxLength:"100" pattern:"^[a-z0-9]+(-[a-z0-9]+)*$" patternDescription:"小写字母、数字和连字符"`
    Content string `json:"content"`
}

type CreateInput struct {
    Body Changes
}

type UpdateInput struct {
    ID   uuid.UUID `path:"id"`
    Body Changes
}
```

标题为空、slug 含有空格或大写字母时返回 `422`。处理器把 `in.Body` 整个交给 `Handle`。请求体中的字段默认必填，见[必填与可选字段](#required-and-optional-fields)。

<a id="available-validation-rules"></a>
## 校验规则的标签

| 标签 | 适用类型 | 规则 |
| --- | --- | --- |
| `minLength`、`maxLength` | 字符串 | 最小、最大长度，按字符计数 |
| `pattern` | 字符串 | 必须匹配的正则表达式；加 `patternDescription` 时提示为“必须是”加描述 |
| `format` | 字符串 | 预定义格式，例如 `email`、`uri`、`uuid`、`date-time`、`date` |
| `enum` | 字符串、数字 | 允许的取值，逗号分隔，例如 `enum:"draft,published"`；需要显示文字时用[枚举类型](#enums) |
| `minimum`、`maximum` | 数字 | 最小值、最大值，包含边界 |
| `exclusiveMinimum`、`exclusiveMaximum` | 数字 | 最小值、最大值，不包含边界 |
| `multipleOf` | 数字 | 必须是这个数的倍数 |
| `minItems`、`maxItems` | 数组 | 最少、最多元素个数 |
| `uniqueItems` | 数组 | 元素各不相同 |
| `default` | 任意 | 客户端省略时的值 |
| `required` | 任意 | 改变默认的必填规则，见[必填与可选字段](#required-and-optional-fields) |

完整的列表见 [Huma 文档](https://huma.rocks/features/request-validation/)。

<a id="required-and-optional-fields"></a>
## 必填与可选字段

- 请求体中的字段默认必填。`json` 标签加 `omitempty` 或者加 `required:"false"` 后可选。
- 查询参数、请求头默认可选，加 `required:"true"` 后必填。
- 路径参数总是必填。

```go
type PatchInput struct {
    ID   uuid.UUID `path:"id"`
    Body struct {
        Title   string  `json:"title" minLength:"1" maxLength:"200"`
        Excerpt *string `json:"excerpt,omitempty" maxLength:"500"`
    }
}
```

- 可选字段用指针区分“没有传”（`nil`）和“传了空字符串”（指向 `""`）。
- `omitempty` 只让“不传”合法。传了空字符串时仍要满足 `minLength:"1"`；空字符串也合法时去掉 `minLength`。
- 数组和对象不可为空：响应中 nil 的切片编码为 `[]`、nil 的 map 编码为 `{}`；请求中写成 `null` 的数组校验失败。要接受 `null` 的数组加 `nullable:"true"`。

<a id="validation-error-response"></a>
## 校验错误响应

请求体不是合法的 JSON 时返回 `400`，字段校验失败时返回 `422`，两者都带 `errors` 数组，每项是一个字段的问题。请求语言是中文、请求体为 `{"title": "", "slug": "Hello World", "content": "……"}` 时：

```json
{
  "title": "Unprocessable Entity",
  "status": 422,
  "detail": "提交的内容有误",
  "errors": [
    {"message": "必须是小写字母、数字和连字符", "location": "body.slug", "value": "Hello World", "code": "validation.pattern_description"},
    {"message": "不能为空", "location": "body.title", "value": "", "code": "validation.filled"}
  ]
}
```

| 字段 | 内容 |
| --- | --- |
| `message` | 给用户看的提示，按请求的语言翻译 |
| `location` | 出错的位置，例如 `body.title`、`query.limit`；缺少必填字段时是这个字段的位置 |
| `value` | 服务端收到的值 |
| `code` | 违反的规则，不随语言变化，见[校验提示](#validation-messages) |

- 客户端按 `location` 把 `message` 显示在对应的表单字段旁边，需要自己的文案时按 `code` 处理，见[客户端协议](protocol.md#errors)。
- 需要认证的接口先认证再校验：没有凭据、请求体也不合法的请求得到 `401`。

<a id="validation-messages"></a>
### 校验提示和 `code`

提示按请求协商的语言翻译，框架内置中文和英文，`code` 是翻译用的消息 ID：

| `code` | 规则 | 中文提示 |
| --- | --- | --- |
| `validation.required` | 缺少必填字段或参数 | 必填 |
| `validation.filled` | `minLength:"1"` | 不能为空 |
| `validation.min_length`、`validation.max_length` | `minLength`、`maxLength` | 至少 N 个字符、最多 N 个字符 |
| `validation.minimum`、`validation.maximum` | `minimum`、`maximum` | 不能小于 N、不能大于 N |
| `validation.pattern_description` | `pattern` 加 `patternDescription` | 必须是 + 描述 |
| `validation.pattern` | `pattern` | 格式不正确 |
| `validation.email`、`validation.url` | `format:"email"`、`format:"uri"` | 邮箱格式不正确、网址格式不正确 |
| `validation.one_of` | `enum` 或枚举类型 | 不是可选的值 |
| `validation.integer`、`validation.number` | 数字字段收到了其他值 | 必须是整数、必须是数字 |
| `validation.type` | 字段的 JSON 类型不对，例如字符串字段传了 `1` | 类型不正确 |
| `validation.unexpected` | 请求体中有未声明的字段，通常是字段名拼错 | 不支持这个字段 |

- 完整的列表在框架的 `i18n/locales/zh-CN.yaml` 中，以 `validation.` 开头。应用的翻译文件写同名消息即可覆盖，见[本地化](../general/localization.md)。
- 没有安装 `httpx.Locale` 中间件或请求的语言是英文时，提示是英文，`code` 照常返回。
- 无法识别的消息（例如 `oneOf` 等组合规则）没有 `code`，`message` 是 Huma 的英文原文。
- [表格导入](../features/import-export.md)的提示与接口相同。

<a id="enums"></a>
## 带显示文字的枚举：`enum.New`

状态、类型这类只接受几个固定值、界面上要显示成文字的字段，用 `enum` 包声明，取值、校验和显示文字只写一次：

```go
package article

import (
    "github.com/danielgtaylor/huma/v2"

    "github.com/go-tenon/tenon/contract"
    "github.com/go-tenon/tenon/enum"
)

// Status 是文章的状态。
type Status string

const (
    Draft     Status = "draft"
    Published Status = "published"
)

// Statuses 是文章的全部状态，源语言默认显示文字写在定义处；多语言在翻译文件中用 article.status.<状态> 覆盖。
var Statuses = enum.New("article.status",
    enum.Item(Draft, "草稿"),
    enum.Item(Published, "已发布"),
)

// Schema 让 OpenAPI 中的 Status 引用命名类型 ArticleStatus。
func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }
```

多语言应用在附加语言的[翻译文件](../general/localization.md#translation-files)中按 `<前缀>.<取值>` 补充译文（单语言项目无需另建翻译文件）：

```yaml
# locales/en.yaml
article.status.draft: Draft
article.status.published: Published
```

- 字段类型是 `Status` 时，请求体和查询参数只接受声明的取值，其他值返回 `422` `validation.one_of`，不用再写 `enum` 标签。[MCP 工具](../ai/mcp.md)和 [RPC](../rpc/endpoints.md) 的参数同样只接受这些取值。
- `contract.EnumSchema` 把它登记为 OpenAPI 的命名类型（`article.status` 是 `ArticleStatus`），带全部取值、`x-tenon-enum` 和默认显示文字 `x-tenon-enum-labels`。生成的客户端得到取值的联合类型和各语言的显示文字，见[客户端的枚举](../frontend/client-react.md#ts-enums)。
- 类型名只由前缀决定。前缀相同、取值不同的两个枚举在注册路由时 panic，例如“全部角色”和“可以分配的角色”分别用前缀 `role` 和 `assignable_role`，显示文字各写一份。
- `Statuses.Label(ctx, v)` 按请求的语言返回显示文字，翻译文件中没有时回退到定义处的默认显示文字，都没有时返回取值本身；`Statuses.Values()` 按声明的顺序返回全部取值；`Statuses.Valid(v)` 判断取值是否合法。

<a id="business-validation"></a>
## 业务规则校验：在 `Handle` 中返回业务错误

“slug 全站唯一”“已发布文章的 slug 不能改”这类要查询数据或依赖业务状态的规则，在[业务动作](../concepts/actions.md)的 `Handle` 中检查，返回[业务错误](../general/errors.md#defining-errors)：

```go
var ErrSlugLocked = errorx.Define(errorx.InvalidArgument, "article.slug_locked")

func (a *Update) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID, c Changes) (*Article, error) {
    // ……读取文章 item
    if item.Status == StatusPublished && c.Slug != item.Slug {
        return nil, ErrSlugLocked.New()
    }
    // ……
}
```

- `errorx.InvalidArgument` 返回 `422`，响应带 `code: "article.slug_locked"`，客户端按它给出提示。完整的例子见[错误处理](../general/errors.md#example)。
- 命令行、队列和测试直接调用 `Handle`，不经过标签上的规则。必须成立的约束（例如标题必填）在 `Handle` 中再检查一次。

<a id="request-id"></a>
## 读取请求 ID：`logging.RequestID`

[默认中间件](middleware.md#default-middleware)中的 `httpx.AssignRequestID` 为每个请求确定一个 ID：请求头 `X-Request-ID` 带有合法的值（1–128 个可打印 ASCII 字符，不含空格）时沿用，否则生成新的。业务代码从 context 读取：

```go
requestID := logging.RequestID(ctx) // 没有安装 AssignRequestID 时是空字符串
```

同一个 ID 写在响应头 `X-Request-ID` 和这个请求的全部日志中，这个请求发布的队列任务读到的也是它，见[调用关联](../general/logging.md#request-correlation)。读取哪个请求头可以配置，见[请求 ID](middleware.md#request-id)。

<a id="limits"></a>
## 请求体大小与超时

<a id="body-size"></a>
### 请求体大小：`http.body_limit`

默认中间件中的 `httpx.BodyLimit` 把请求体限制在 1 MiB，超出时返回 `413`：

```yaml
http:
  body_limit:
    max_bytes: 8388608 # 8 MiB，默认 1048576
```

接收大请求体的接口还要把 `huma.Operation` 的 `MaxBodyBytes` 设为相同的上限，两者都足够大才能通过。只有少数接口需要更大的上限时，[自行组装中间件](middleware.md#custom-stack)，只为这些接口放宽 `BodyLimit`。

<a id="timeouts"></a>
### 请求超时：`http.timeout`

`httpx.Timeout` 为请求的 context 设置超时，默认 30 秒。它不在默认中间件中，按路由组安装，SSE 等流式路由不设超时，见[请求超时](middleware.md#request-timeout)：

```yaml
http:
  timeout:
    duration: 10s # 默认 30s
```

超时后请求的 context 被取消。把处理器收到的 `ctx` 传给数据库查询和外部调用，超时才能生效。服务器的读写超时（`read_header_timeout`、`read_timeout`、`write_timeout`、`idle_timeout`）见 [HTTP 配置](middleware.md#http-configuration)。
