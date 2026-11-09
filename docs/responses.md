# 响应

- [创建响应](#creating-responses)
    - [JSON 响应](#json-responses)
    - [状态码](#status-codes)
    - [响应头](#headers)
- [集合与时间](#collections-and-times)
- [分页](#pagination)
- [返回错误](#returning-errors)

<a id="creating-responses"></a>
## 创建响应

<a id="json-responses"></a>
### JSON 响应

和[请求](requests.md)一样，响应也由一个 Go 结构体描述，称为**输出类型**。输出类型中名为 `Body` 的字段就是 JSON 响应体：

```go
type ArticleDTO struct {
    ID          string     `json:"id"`
    Title       string     `json:"title"`
    Slug        string     `json:"slug"`
    AuthorID    string     `json:"author_id"`
    Status      string     `json:"status"`
    PublishedAt *time.Time `json:"published_at,omitempty"`
}

type ArticleOutput struct {
    Body ArticleDTO
}
```

处理器成功时返回输出和 `nil` 错误：

```go
return &ArticleOutput{Body: toDTO(item)}, nil
```

`Body` 中的字段会被编码为 JSON 返回给客户端：

```json
{
  "id": "0f8c2f0e-…",
  "title": "Hello World",
  "slug": "hello-world",
  "author_id": "alice",
  "status": "published",
  "published_at": "2026-11-10T08:30:00Z"
}
```

草稿还没有发布时间，`published_at` 字段会被省略。

> [!TIP]
> 建议为响应定义专门的 DTO 结构，而不是直接返回数据库模型。这样你可以自由地调整数据库结构，而不会意外地改变 API，也不会把内部字段暴露给客户端。

<a id="status-codes"></a>
### 状态码

有响应体的成功响应默认返回 `200`。如果需要返回固定的其他状态码，例如创建资源时的 `201`，可以在路由声明中设置 `DefaultStatus`：

```go
op := huma.Operation{
    OperationID:   "article-create",
    Method:        http.MethodPost,
    Path:          "/articles",
    DefaultStatus: http.StatusCreated,
}
```

如果状态码需要在每次请求时动态决定，可以在输出类型中声明一个 `Status int` 字段，这是 Huma 的原生能力：

```go
type UpsertArticleOutput struct {
    Status int
    Body   ArticleDTO
}
```

<a id="headers"></a>
### 响应头

在输出类型中使用 `header` 标签声明响应头：

```go
type CreatedArticleOutput struct {
    Location string `header:"Location"`
    Body     ArticleDTO
}
```

处理器中设置字段的值即可：

```go
return &CreatedArticleOutput{
    Location: "/articles/" + item.ID.String(),
    Body:     toDTO(item),
}, nil
```

<a id="collections-and-times"></a>
## 集合与时间

列表为空时，应当返回 `[]` 而不是 `null`，这样客户端可以直接遍历而无需判空。在 Go 中，`nil` slice 会被编码为 `null`，所以请初始化一个空 slice：

```go
items := make([]ArticleDTO, 0)
```

时间字段使用 `time.Time`，它会被编码为 RFC 3339 格式的字符串，例如 `"2026-11-10T08:30:00Z"`。建议在业务和数据库中统一使用 UTC 时间，只在展示时转换为用户所在的时区。

<a id="pagination"></a>
## 分页

mygoravel 不规定分页的结构，你可以根据业务自行设计。对于数据量大、写入频繁的列表，推荐使用**游标分页**。下面是一个包含当前页数据和下一页游标的响应：

```go
type ListArticlesOutput struct {
    Body struct {
        Items []ArticleDTO `json:"items"`
        Next  string       `json:"next,omitempty"`
    }
}
```

客户端拿到 `next` 后，把它原样作为下一次请求的 `before` 参数传回；没有下一页时，响应中不包含 `next`：

```json
{
  "items": [{"id": "…", "title": "Hello World", "slug": "hello-world", "author_id": "alice", "status": "published"}],
  "next": "djE6MjAyNi0xMS0xMFQwODozMDowMFo6MGY4YzJmMGU"
}
```

实现游标分页时，有几点值得注意：

- 查询必须使用**确定且唯一**的排序，例如 `ORDER BY published_at DESC, id DESC`，否则翻页时可能出现重复或遗漏；
- 游标中应包含排序位置，最好再加上版本号，以便将来调整格式；
- 先按当前用户筛选数据，再应用游标条件，避免越权读取。

分页查询本身使用 Bun 的原生 API 实现，见[数据库](database.md)。

<a id="returning-errors"></a>
## 返回错误

业务失败时，处理器直接返回 `error` 即可，不需要在输出类型中设计错误字段：

```go
item, err := articles.Find(ctx, in.ID)
if err != nil {
    return nil, err
}
```

`rest.Register` 会把错误转换为统一格式的错误响应，并选择合适的状态码。如何定义业务错误、它们如何映射到 HTTP 状态码，请参阅[错误处理](errors.md)。
