# 错误处理

- [简介](#introduction)
- [定义业务错误](#defining-errors)
    - [错误分类](#error-kinds)
- [返回错误](#returning-errors)
    - [包装底层错误](#wrapping-errors)
    - [堆栈与错误文本](#stack-traces)
    - [附加公开参数](#public-parameters)
- [判断错误](#inspecting-errors)
    - [跨进程传递](#cross-process-errors)
- [错误响应](#error-responses)
    - [状态码映射](#status-mapping)
    - [响应格式](#response-format)
    - [原生 Huma 错误](#huma-errors)
- [错误日志](#reporting-errors)

<a id="introduction"></a>
## 简介

当你开始一个新的 mygoravel 项目时，错误处理已经为你配置好了。业务代码只需要返回错误，mygoravel 会负责选择 HTTP 状态码、生成统一格式的错误响应，并记录服务端错误的日志。

mygoravel 的业务错误由两个要素组成：

- **错误码**：一个稳定的字符串，例如 `note.not_found`。客户端根据它来区分不同的失败原因，它不会随语言或文案变化；
- **错误分类**：例如“未找到”、“冲突”、“无权限”，用于决定 HTTP 状态码。

业务包只关心错误码和分类，完全不需要知道 HTTP 的存在。这样，同一个业务服务既可以被 HTTP 接口调用，也可以被命令行或测试直接调用。

<a id="defining-errors"></a>
## 定义业务错误

使用 `errorx.Define` 定义一个错误**模板**，通常作为业务包的包级变量：

```go
package note

import "github.com/ryan053857/mygoravel/errorx"

var (
    ErrNotFound = errorx.Define(errorx.NotFound, "note.not_found")
    ErrConflict = errorx.Define(errorx.Conflict, "note.conflict")
)
```

每次失败时，调用模板的 `New` 方法创建一个具体的错误：

```go
func (s *Service) Find(ctx context.Context, id uuid.UUID) (*Note, error) {
    // ……
    return nil, ErrNotFound.New()
}
```

错误码推荐使用 `资源.原因` 的形式，全部小写，用下划线连接单词。错误码在整个应用中必须唯一，因为 `errors.Is` 是按错误码判断两个错误是否相同的。

<a id="error-kinds"></a>
### 错误分类

分类描述的是失败的**性质**，与 HTTP、命令行、队列等调用方式无关。`errorx` 包提供了以下分类：

| 分类 | 含义 | 调用方原样重试是否有意义 |
| --- | --- | --- |
| `errorx.InvalidArgument` | 输入不符合业务规则 | 否，需要修正输入 |
| `errorx.Unauthenticated` | 调用者身份未知 | 否，需要先认证 |
| `errorx.PermissionDenied` | 调用者无权执行该操作 | 否 |
| `errorx.NotFound` | 资源不存在 | 否 |
| `errorx.Conflict` | 与当前状态冲突，例如重复创建 | 否，需要先解决冲突 |
| `errorx.Unavailable` | 依赖暂时不可用 | 是，可以稍后重试 |
| `errorx.Internal` | 内部错误 | 通常没有意义，需要排查 |

每种调用方式在自己的入口处把分类转换为对应的结果，例如 HTTP 接口转换为[状态码](#status-mapping)，命令行转换为[退出码](commands.md#exit-codes)。业务代码只需选择最贴切的分类。

<a id="returning-errors"></a>
## 返回错误

<a id="wrapping-errors"></a>
### 包装底层错误

很多业务错误由底层错误引起，例如数据库的唯一约束冲突。使用模板的 `Wrap` 方法可以返回业务错误，同时保留原始原因供日志排查：

```go
if database.IsUniqueViolation(err, "notes_owner_title_key") {
    return ErrConflict.Wrap(err)
}
```

包装后的错误遵循 `cockroachdb/errors` 的约定：`err.Error()` 返回 `note.conflict: <原始错误消息>`。客户端只会看到错误码和翻译后的 `detail`，原始错误消息只出现在服务端日志中。

对于其他无法归类的错误，使用 `github.com/cockroachdb/errors` 的 `errors.Wrap` 添加上下文后继续向上返回：

```go
if err != nil {
    return errors.Wrap(err, "create note")
}
```

未分类的错误最终会被当作内部错误处理，返回 `500`。

<a id="stack-traces"></a>
### 堆栈与错误文本

mygoravel 遵循一条生产环境中常用的原则：**把堆栈留给未知的系统故障，把简洁的错误文本留给可预期的业务结果。**

- **可预期的业务结果**：分类为 `InvalidArgument`、`Unauthenticated`、`PermissionDenied`、`NotFound`、`Conflict` 的错误不记录堆栈。它们是正常流程的一部分，错误码加上逐层包装的上下文已经足以定位问题，堆栈只会增加噪音和开销。
- **系统故障**：分类为 `Unavailable`、`Internal` 的错误，`New` 和 `Wrap` 会在调用处记录堆栈。

是否记录堆栈取决于**分类**，而不是错误是否由 `errorx` 创建。例如 `errorx.Define(errorx.Unavailable, "note.db")` 虽然是一个业务模板，表示的却是系统故障，因此会带上堆栈。

未分类的底层错误同样需要堆栈。数据库驱动、标准库返回的原始错误本身不带堆栈，因此请在它们**刚进入你的代码**时用 `errors.Wrap` 或 `errors.WithStack` 包装，堆栈就记录在这一层。如果一路只用 `fmt.Errorf` 向上返回，最终的错误日志中就只有文本，没有位置信息。

两类错误在日志中的去向也不同，见[错误日志](#reporting-errors)。

<a id="public-parameters"></a>
### 附加公开参数

有时你希望错误携带一些额外的信息，例如哪个字段冲突了。可以在创建错误时通过 `errorx.Public` 附加参数：

```go
return ErrConflict.New(errorx.Public("field", "title"))
```

之所以叫“公开”参数，是因为它们经过你的确认，可以展示给调用者。启用[本地化](localization.md#translating-errors)后，它们会自动作为翻译模板的数据，例如 `{{.field}}`。在其他地方也可以用 `errorx.PublicOf(err)` 读取。

> [!NOTE]
> 公开参数不会作为独立字段出现在错误响应中，只会通过翻译模板体现在 `detail` 里。

<a id="inspecting-errors"></a>
## 判断错误

业务错误完全兼容 Go 标准的错误处理方式。使用 `errors.Is` 判断错误是否来自某个模板，即使它已被多层包装：

```go
if errors.Is(err, note.ErrNotFound) {
    // 处理笔记不存在的情况
}
```

也可以直接从错误链中读取错误码和分类：

```go
code := errorx.CodeOf(err) // "note.not_found"，非业务错误时为空字符串
kind := errorx.KindOf(err) // errorx.NotFound，非业务错误时为 errorx.Internal
```

<a id="cross-process-errors"></a>
### 跨进程传递

`errorx` 已经为 `cockroachdb/errors` 注册了编解码器。业务错误经过 `errors.EncodeError` 编码、在另一个进程中用 `errors.DecodeError` 还原后，错误码、分类、公开参数和 `errors.Is` 的判断结果都保持不变。这适用于通过 gRPC、消息队列等方式在服务之间传递错误的场景：

```go
encoded := errors.EncodeError(ctx, err) // 可序列化的 protobuf 消息
// ……传输到另一个进程……
decoded := errors.DecodeError(ctx, encoded)
errors.Is(decoded, note.ErrNotFound) // true
```

公开参数以 JSON 形式传输，因此还原后的数值类型会变为 `float64`。

<a id="error-responses"></a>
## 错误响应

<a id="status-mapping"></a>
### 状态码映射

通过 `rest.Register` 注册的处理器返回错误时，mygoravel 会根据[错误分类](#error-kinds)选择 HTTP 状态码：

| 分类 | HTTP 状态 |
| --- | --- |
| `errorx.InvalidArgument` | 422 Unprocessable Entity |
| `errorx.Unauthenticated` | 401 Unauthorized |
| `errorx.PermissionDenied` | 403 Forbidden |
| `errorx.NotFound` | 404 Not Found |
| `errorx.Conflict` | 409 Conflict |
| `errorx.Unavailable` | 503 Service Unavailable |
| `errorx.Internal` 及非业务错误 | 500 Internal Server Error |

这个映射只存在于 `httpx/rest` 包中，业务包不需要关心。

<a id="response-format"></a>
### 响应格式

所有错误响应都直接沿用 [Huma](https://huma.rocks) 的错误结构，也就是 [Problem Details](https://www.rfc-editor.org/rfc/rfc9457) 格式，`Content-Type` 为 `application/problem+json`。mygoravel 只在此基础上为业务错误增加了一个 `code` 字段：

```json
{
  "title": "Not Found",
  "status": 404,
  "detail": "Note not found",
  "code": "note.not_found"
}
```

| 字段 | 说明 |
| --- | --- |
| `status` | HTTP 状态码 |
| `title` | 状态码的标准描述 |
| `detail` | 面向用户的提示文案 |
| `code` | 稳定的业务错误码，客户端应根据它编写分支逻辑。**只有业务错误包含** |
| `errors` | 字段级的错误明细，见[验证](validation.md#validation-error-response) |
| `type`、`instance` | Huma 定义的 RFC 9457 标准字段，mygoravel 默认不填写 |

响应中是否带有 `code`，可以用来区分错误的来源：

- **有 `code`**：业务错误，由 `errorx` 定义，客户端按 `code` 处理；
- **没有 `code`**：协议层或输入层的错误，例如路由不存在、请求体验证失败、服务端内部错误，客户端按 `status` 处理。

需要定位某个请求时，使用响应头 `X-Request-ID` 中的请求 ID，它与这个请求的所有日志相对应。

`detail` 的内容取决于是否启用了[本地化](localization.md)：

- 启用本地化时，以错误码为消息 ID 翻译，例如上面的 `Note not found` 来自应用提供的英文翻译。找不到翻译时，使用 HTTP 状态描述；
- 未启用本地化时，使用 HTTP 状态描述，例如 `Not Found`。

处理器返回的错误既不是业务错误、也不是 Huma 错误时，例如数据库驱动的原始错误，客户端只会收到一个通用的 `500`，`detail` 为 `unexpected error occurred`。原始错误信息只会进入[错误日志](#reporting-errors)。

> [!WARNING]
> `detail` 是展示文案，可能随语言和版本变化。客户端请不要根据 `detail` 做逻辑判断，而应使用 `code`。

<a id="huma-errors"></a>
### 原生 Huma 错误

如果你只是想返回一个纯 HTTP 层面的错误，也可以直接使用 Huma 内置的错误函数，例如 `huma.Error404NotFound("note does not exist")`。即使被包装过，它也会原样输出：状态码和 `detail` 都会保留，但不带 `code`，也不会被翻译。

你也可以返回自己实现了 `huma.StatusError` 接口的错误类型，Huma 会按照这个类型本身的结构序列化它。需要稳定的错误码或本地化文案时，请使用 `errorx` 定义的错误。

<a id="reporting-errors"></a>
## 错误日志

mygoravel 遵循“**在处理决定的地方记录一次**”的原则：业务代码只负责添加上下文并返回错误，不需要自己记录日志。

当 API 配置了日志器时，`rest.Register` 会为每个 `5xx` 错误记录一条诊断日志，包含错误码和完整的错误链。错误链以 `%+v` 格式输出，包括每一层包装的上下文、原始错误消息和调用处堆栈。与此同时，访问日志会记录每个请求的结果。两条日志带有相同的 `request_id`，可以关联查看。日志的配置方式见[日志](logging.md)。

`4xx` 错误属于正常的业务结果，不会产生额外的错误日志。业务错误的错误码会作为 `code` 字段写入这个请求的访问日志，例如：

```json
{"level":"INFO","msg":"http request","method":"POST","status":409,"code":"note.conflict","request_id":"9F2K…"}
```

需要为访问日志追加其他字段时，可以在处理过程中调用 `httpx.AddAccessLogAttrs(ctx, attrs...)`。
