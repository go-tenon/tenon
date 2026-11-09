# 日志

[文档首页](README.md) / HTTP 基础

mygoravel 使用标准库 `log/slog`。应用显式传递 `*slog.Logger`，各个 API 实例可以使用不同的输出和级别。

本章内容：

- [构建日志器](#logger)
- [记录结构化字段](#structured)
- [关联请求](#context)
- [错误日志责任](#responsibility)
- [敏感字段](#secrets)

<a id="logger"></a>

## 构建日志器

```go
logger := logging.New(logging.Options{
    Writer: os.Stderr,
    Format: logging.FormatJSON,
    Level:  slog.LevelInfo,
    Attrs: []slog.Attr{
        slog.String("service", "notes"),
        slog.String("version", "0.1.0"),
    },
})
```

需要标准库 `os`、`log/slog` 和框架 `logging` 包。默认格式是 JSON、级别是 Info；省略 Writer 时输出到 stdout。API 进程通常显式使用 stderr，把 stdout 留给命令结果。

本地阅读日志时可以使用 `logging.FormatText`。`AddSource: true` 可加入来源位置。框架不替换进程的 `slog.Default`。

<a id="structured"></a>

## 记录结构化字段

```go
logger.InfoContext(ctx, "note created",
    "note_id", noteID,
    "owner_id", ownerID,
)
```

`ctx`、`noteID` 和 `ownerID` 来自当前业务操作。字段名保持稳定，便于检索和聚合。不要将完整请求体、Authorization 或带凭据的连接串放入日志。

<a id="context"></a>

## 关联请求

将日志器传给 API：

```go
api := rest.New(rest.Options{
    Middleware: httpx.MiddlewareOptions{Logger: logger},
})
```

默认中间件生成 request_id。访问日志记录请求方法、状态、耗时和客户端地址，错误边界记录 5xx 诊断；二者带有相同的 request_id。

用 `logging.New` 构建的日志器会读取 context 中的关联字段。业务代码使用收到的请求 ctx 调用 `InfoContext` 或 `ErrorContext`，即可延续这条关联。

也可以添加自己的上下文字段：

```go
ctx = logging.WithAttrs(ctx, slog.String("component", "note"))
```

传入原生 slog.Logger 也可以作为 API 边界日志器；如果还希望该原始日志器在业务层自动读取这些 context 字段，应使用 `logging.NewContextHandler` 包装它的 Handler。

<a id="responsibility"></a>

## 错误日志责任

业务层补充上下文后返回错误，处理决定所在的边界记录失败。不要在服务、处理器和 middleware 中连续打印同一个 error。

默认 REST 5xx 诊断记录 code 和安全堆栈，不输出原始 cause；访问日志也不包含 URL 查询参数或正文。应用自己的日志仍须遵循相同的敏感值处理约定。

<a id="secrets"></a>

## 敏感字段

`config.Secret` 会在 slog 输出中脱敏。不要在日志中调用 `Reveal()`，也不要先转换为普通字符串再记录。错误的 `%+v` 格式化不具备自动脱敏能力。

---

[上一篇：错误处理](errors.md) · [下一篇：认证与授权](authentication.md)
