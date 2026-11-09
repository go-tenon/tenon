# 错误处理

- [简介](#introduction)
- [定义业务错误](#defining-errors)
    - [错误分类](#error-kinds)
- [返回错误](#returning-errors)
    - [包装底层错误](#wrapping-errors)
    - [附加公开参数](#public-parameters)
- [判断错误](#inspecting-errors)
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

使用 `fault.Define` 定义一个错误**模板**，通常作为业务包的包级变量：

```go
package note

import "github.com/ryan053857/mygoravel/fault"

var (
    ErrNotFound = fault.Define(fault.NotFound, "note.not_found")
    ErrConflict = fault.Define(fault.Conflict, "note.conflict")
)
```

每次失败时，调用模板的 `New` 方法创建一个具体的错误。`New` 会在调用处记录堆栈，方便排查问题：

```go
func (s *Service) Find(ctx context.Context, id uuid.UUID) (*Note, error) {
    // ……
    return nil, ErrNotFound.New()
}
```

错误码推荐使用 `资源.原因` 的形式，全部小写，用下划线连接单词。

<a id="error-kinds"></a>
### 错误分类

`fault` 包提供了以下分类：

| 分类 | 含义 | HTTP 状态 |
| --- | --- | --- |
| `fault.InvalidArgument` | 输入不符合业务规则 | 422 |
| `fault.Unauthenticated` | 调用者身份未知 | 401 |
| `fault.PermissionDenied` | 调用者无权执行该操作 | 403 |
| `fault.NotFound` | 资源不存在 | 404 |
| `fault.Conflict` | 与当前状态冲突，例如重复创建 | 409 |
| `fault.Unavailable` | 依赖暂时不可用，可以稍后重试 | 503 |
| `fault.Internal` | 内部错误 | 500 |

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

对于其他无法归类的错误，可以使用 `github.com/cockroachdb/errors` 添加上下文后继续向上返回：

```go
if err != nil {
    return errors.Wrap(err, "create note")
}
```

未分类的错误最终会被当作内部错误处理，返回 `500`。

<a id="public-parameters"></a>
### 附加公开参数

有时你希望错误携带一些额外的信息，例如哪个字段冲突了。可以在创建错误时通过 `fault.Public` 附加参数：

```go
return ErrConflict.New(fault.Public("field", "title"))
```

之所以叫“公开”参数，是因为它们经过你的确认，可以展示给调用者。之后可以用 `fault.PublicOf(err)` 读取，例如在翻译错误消息时作为模板数据使用。

> [!NOTE]
> 公开参数**不会**自动出现在错误响应中。是否展示、如何展示，由你在自定义的响应转换或翻译逻辑中决定。

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
code := fault.CodeOf(err) // "note.not_found"，非业务错误时为空字符串
kind := fault.KindOf(err) // fault.NotFound，非业务错误时为 fault.Internal
```

<a id="error-responses"></a>
## 错误响应

<a id="status-mapping"></a>
### 状态码映射

通过 `rest.Register` 注册的处理器返回错误时，mygoravel 会根据[错误分类](#error-kinds)选择 HTTP 状态码。这个映射只存在于 `httpx/rest` 包中，业务包不需要关心。

<a id="response-format"></a>
### 响应格式

所有错误响应都使用 [Problem Details](https://www.rfc-editor.org/rfc/rfc9457) 格式，`Content-Type` 为 `application/problem+json`：

```json
{
  "status": 404,
  "title": "Not Found",
  "detail": "Note not found",
  "code": "note.not_found",
  "request_id": "<请求 ID>"
}
```

| 字段 | 说明 |
| --- | --- |
| `status` | HTTP 状态码 |
| `title` | 状态码的标准描述 |
| `detail` | 面向用户的提示文案 |
| `code` | 稳定的错误码，客户端应根据它编写分支逻辑 |
| `request_id` | 请求 ID，用于在服务端日志中查找对应记录 |
| `errors` | 仅验证错误包含，见[验证](validation.md#validation-error-response) |

`detail` 的内容取决于是否启用了[本地化](localization.md)：

- 启用本地化时，以错误码为消息 ID 翻译，例如上面的 `Note not found` 来自应用提供的英文翻译。找不到翻译时，使用 HTTP 状态描述；
- 未启用本地化时，业务错误的 `detail` 为错误码本身。

对于 `500` 等服务端错误，`detail` 始终是通用提示，不会泄露任何内部信息。

> [!WARNING]
> `detail` 是展示文案，可能随语言和版本变化。客户端请不要根据 `detail` 做逻辑判断，而应使用 `code`。

<a id="huma-errors"></a>
### 原生 Huma 错误

如果你只是想返回一个纯 HTTP 层面的错误，也可以使用 Huma 内置的错误函数，例如 `huma.Error404NotFound`。即使被包装过，它的状态码也会被保留。为了避免泄露内部信息，这类错误的文案会被替换为通用提示，因此在业务代码中更推荐使用 `fault` 定义的错误。

<a id="reporting-errors"></a>
## 错误日志

mygoravel 遵循“**在处理决定的地方记录一次**”的原则：业务代码只负责添加上下文并返回错误，不需要自己记录日志。

当 API 配置了日志器时，`rest.Register` 会为每个 `5xx` 错误记录一条诊断日志，包含错误码和可报告的堆栈。与此同时，访问日志会记录每个请求的结果。两条日志带有相同的 `request_id`，可以关联查看。日志的配置方式见[日志](logging.md)。

`4xx` 错误属于正常的业务结果，只体现在访问日志中，不会产生额外的错误日志。
