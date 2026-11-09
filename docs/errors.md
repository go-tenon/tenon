# 错误处理

[文档首页](README.md) / HTTP 基础

业务错误由稳定的错误码和分类组成。业务服务返回错误，HTTP 边界决定状态码、对外文案和诊断日志。

本章内容：

- [定义业务错误](#domain-errors)
- [包装与判断](#wrapping)
- [HTTP 状态映射](#http-status)
- [错误响应](#problem)
- [公开参数](#public-data)
- [记录错误与翻译](#reporting)

<a id="domain-errors"></a>

## 定义业务错误

使用 `fault.Define` 创建可复用的模板，每次失败时调用 `New`：

```go
var ErrNoteNotFound = fault.Define(fault.NotFound, "note.not_found")

func FindNote(id string) error {
    return ErrNoteNotFound.New()
}
```

这里的 `fault` 来自 `github.com/ryan053857/mygoravel/fault`。模板通常定义在业务包内；`New` 在具体失败处记录堆栈。

<a id="wrapping"></a>

## 包装与判断

需要保留底层原因时，使用模板的 `Wrap`：

```go
if database.IsUniqueViolation(err, "notes_owner_title_key") {
    return ErrNoteConflict.Wrap(err)
}
```

上例假定 `ErrNoteConflict` 定义为 Conflict 分类。其他错误可以用 `github.com/cockroachdb/errors` 增加操作上下文：

```go
return errors.Wrap(err, "create note")
```

错误链支持标准库的 `errors.Is`、`errors.As` 和 `fmt.Errorf` 的 `%w`：

```go
if errors.Is(err, ErrNoteNotFound) {
    // 根据稳定模板处理。
}
```

使用 `errors.Is` 匹配模板，使用 `fault.CodeOf`、`fault.KindOf` 从包装链读取业务语义。

<a id="http-status"></a>

## HTTP 状态映射

通过 `rest.Register` 返回的业务错误按下表转换：

| fault.Kind | HTTP 状态 |
| --- | --- |
| InvalidArgument | 422 |
| Unauthenticated | 401 |
| PermissionDenied | 403 |
| NotFound | 404 |
| Conflict | 409 |
| Unavailable | 503 |
| Internal、未分类错误 | 500 |

HTTP 映射位于 `httpx/rest`，业务包只使用 fault 分类。纯 HTTP 错误可以使用 Huma 的 StatusError；包装过的 `huma.Error404NotFound` 保留 404。原生 Huma 错误文案经 Transformer 转换为通用提示。

<a id="problem"></a>

## 错误响应

定义 `note.not_found` 错误并提供对应英文翻译后，可以得到类似正文：

```json
{
  "status": 404,
  "title": "Not Found",
  "detail": "Note not found",
  "code": "note.not_found",
  "request_id": "<请求标识>"
}
```

Content-Type 为 `application/problem+json`。字段校验失败时还会有 `errors` 数组，见[请求校验](requests.md#validation-errors)。同一模型用于 OpenAPI schema。

客户端根据 code 编写分支，用 request_id 联系服务日志；detail 是展示文案，可能随语言改变。未知错误使用通用提示。

<a id="public-data"></a>

## 公开参数

`fault.Public` 附加经过业务确认可公开的元数据，`fault.PublicOf` 读取这些数据。应用通过自定义输出模型或翻译逻辑显式呈现它们。

公开参数与 Wrap 消息使用已审核的业务标识和操作说明。

<a id="reporting"></a>

## 记录错误与翻译

业务层返回错误，`rest.Register` 在有 Logger 时记录一次 5xx 诊断。访问日志记录每次请求的结果。

默认诊断记录稳定 code 和可报告堆栈。`%+v` 会展开错误链中的详细原因，输出范围由调用边界控制。

启用语言目录后，错误码可作为消息 ID。内置目录包含常见英文和中文错误；自定义错误的翻译方式见[本地化](localization.md)。

---

[上一篇：响应](responses.md) · [下一篇：日志](logging.md)
