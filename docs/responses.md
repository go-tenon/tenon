# 响应

[文档首页](README.md) / HTTP 基础

HTTP 处理器返回输出结构体和 error。输出中的 `Body` 是 JSON 正文，其他字段可声明响应状态或响应头。

本章内容：

- [返回 JSON](#json)
- [设置状态与响应头](#status-headers)
- [空集合与时间](#collections)
- [游标分页](#pagination)
- [返回错误](#errors)

<a id="json"></a>

## 返回 JSON

定义面向客户端的 DTO，不直接返回数据库模型：

```go
type NoteDTO struct {
    ID      string `json:"id"`
    Title   string `json:"title"`
    OwnerID string `json:"owner_id"`
}

type NoteOutput struct {
    Body NoteDTO
}
```

处理器成功时返回 `&NoteOutput{Body: ...}, nil`。`Body` 是 Huma 的约定字段，不会成为 JSON 中的额外 `body` 层。Huma 默认还会为对象响应提供 `$schema` 字段和 Link 响应头，用于定位对应的 JSON Schema；本章后续 JSON 示例只展示业务字段。

有正文的普通成功响应默认是 200；创建资源时如需返回 201，应明确设置 Operation 的默认状态。

<a id="status-headers"></a>

## 设置状态与响应头

在路由声明中指定固定成功状态：

```go
op := huma.Operation{
    OperationID:   "note-create",
    Method:        http.MethodPost,
    Path:          "/notes",
    DefaultStatus: http.StatusCreated,
}
```

仍需为 `op` 绑定安全策略，再注册处理器。需要响应头时，将它声明在输出结构体中：

```go
type CreatedNoteOutput struct {
    Location string `header:"Location"`
    Body     NoteDTO
}
```

处理器设置 `Location` 为新资源路径，例如 `/notes/<id>`。需要每次请求动态选择状态时，可使用 Huma 原生输出字段 `Status int`。

<a id="collections"></a>

## 空集合与时间

列表为空时返回 `[]`，便于客户端直接遍历。Go 中可以初始化一个空 slice：

```go
items := make([]NoteDTO, 0)
```

不要让 nil slice 意外序列化为 `null`。时间字段使用 `time.Time` 或明确的 RFC3339 字符串；业务保存 UTC 时刻，展示时再应用指定时区。

<a id="pagination"></a>

## 游标分页

分页结构由应用定义。例如，列表正文可以包含 `items` 和可选的 `next`：

```go
type ListOutput struct {
    Body struct {
        Items []NoteDTO `json:"items"`
        Next  string    `json:"next,omitempty"`
    }
}
```

处理器用当前页填充 Items，并将下一页的位置编码到 Next。客户端把 Next 原样传给下一次请求的 before 参数；没有下一页时省略 next。

数据查询应使用确定且唯一的排序，例如 `created_at DESC, id DESC`。游标应携带版本、排序位置和查询约束；解码、校验与查询条件由业务实现。游标不是授权凭据，服务仍然需要根据当前主体筛选数据。

框架不内置分页执行器。查询条件沿用 Bun 原生 API，见[数据库](database.md)。

<a id="errors"></a>

## 返回错误

业务失败直接返回 error，由 `rest.Register` 的边界转换处理。不要把数据库错误或 `%+v` 堆栈放进输出结构体。

错误状态、稳定错误码和日志责任见[错误处理](errors.md)。

---

[上一篇：请求与校验](requests.md) · [下一篇：错误处理](errors.md)
