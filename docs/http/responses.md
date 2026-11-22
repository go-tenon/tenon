# 响应

接口的输出类型是一个 Go 结构体：`Body` 字段是 JSON 响应体，带 `header` 标签的字段是响应头，`Status` 字段是状态码。列表接口用 `page` 包的分页结构。

包：`httpx/rest`、`page`、`period`。

<a id="creating-responses"></a>
## 示例

```go
type ArticleDTO struct {
    ID          string     `json:"id"`
    Title       string     `json:"title"`
    PublishedAt *time.Time `json:"published_at,omitempty" doc:"发布时间，草稿没有这个字段"`
}

type ArticleOutput struct {
    Body ArticleDTO
}

// HTTP 处理 GET /articles/{id}。
func (a *Get) HTTP(ctx context.Context, in *GetInput) (*ArticleOutput, error) {
    subject, _ := identity.FromContext(ctx)
    item, err := a.Handle(ctx, subject, in.ID)
    if err != nil {
        return nil, err
    }
    return &ArticleOutput{Body: toDTO(item)}, nil
}
```

响应体是 `{"id": "…", "title": "Hello World", "published_at": "2026-11-10T08:30:00Z"}`，草稿省略 `published_at`。

- 响应体用 DTO，不直接用数据库模型，模型的内部字段留在服务端。DTO、转换函数 `toDTO` 和 schema 名的规则见[共享响应结构](routing.md#shared-responses)。
- `doc`、`example`、`enum` 标签只写入接口文档，见[说明与示例](openapi.md#describing-apis)。

<a id="status-codes"></a>
## 状态码：`DefaultStatus` 和 `Status`

有响应体的成功响应默认是 `200`。固定的其他状态码写在 `huma.Operation` 的 `DefaultStatus` 中，例如创建资源返回 `201`：

```go
op := huma.Operation{
    OperationID:   "article-create",
    Method:        http.MethodPost,
    Path:          "/articles",
    DefaultStatus: http.StatusCreated,
}
```

每次请求决定状态码时，在输出类型中声明 `Status int` 字段：

```go
type UpsertArticleOutput struct {
    Status int
    Body   ArticleDTO
}
```

<a id="headers"></a>
## 响应头

带 `header` 标签的字段写入响应头：

```go
type CreatedArticleOutput struct {
    Location string `header:"Location"`
    Body     ArticleDTO
}

return &CreatedArticleOutput{
    Location: "/articles/" + item.ID.String(),
    Body:     toDTO(item),
}, nil
```

<a id="collections-and-times"></a>
## 空列表与时间

- 响应中 nil 的切片编码为 `[]`、nil 的 map 编码为 `{}`，接口文档中的数组和对象不可为空，处理器不用把 nil 换成空切片。
- `time.Time` 编码为 RFC 3339 字符串，例如 `"2026-11-10T08:30:00Z"`。业务和数据库统一用 UTC，只在展示时转换为用户的时区。

<a id="pagination"></a>
## 分页：`page` 包

| | 固定分页 | 游标分页 |
| --- | --- | --- |
| 请求参数 | `page`、`per_page` | `cursor`、`per_page` |
| 响应体 | `items`、`page`、`per_page`、`total` | `items`、`next_cursor` |
| 适用场景 | 需要页码和总数，例如后台表格 | 信息流、数据量大或写入频繁的列表 |

- `per_page` 默认 20、最大 100，超出范围返回 `422`。
- 没有数据时 `items` 是 `[]`。
- 客户端一侧的约定见[客户端协议](protocol.md#pagination)。

<a id="offset-pagination"></a>
### 固定分页：`page.OffsetParams` 和 `page.Offset`

把 `page.OffsetParams` 嵌入输入类型，用 `page.Offset[T]` 作为响应体：

```go
type ListArticlesInput struct {
    page.OffsetParams
    Author string `query:"author"`
}

type ListArticlesOutput struct {
    Body page.Offset[ArticleDTO]
}
```

`in.Limit()`、`in.Offset()` 对应 SQL 的 `LIMIT`、`OFFSET`，Bun 的 `ScanAndCount` 一次读出当前页和总数。`db` 是 `*bun.DB`，`toDTOs` 把模型的切片转换为 DTO：

```go
var rows []Article
total, err := db.NewSelect().Model(&rows).
    Where("author_id = ?", in.Author).
    OrderExpr("created_at DESC, id DESC").
    Limit(in.Limit()).
    Offset(in.Offset()).
    ScanAndCount(ctx)
if err != nil {
    return nil, errors.Wrap(err, "list articles")
}
return &ListArticlesOutput{Body: page.NewOffset(toDTOs(rows), in.OffsetParams, total)}, nil
```

`/articles?page=2&per_page=20` 返回 `{"items": [...], "page": 2, "per_page": 20, "total": 41}`。`LastPage()` 返回最后一页的页码，`Links(n)` 返回分页导航中要显示的页码。

<a id="cursor-pagination"></a>
### 游标分页：`page.CursorParams` 和 `page.Cursor`

游标分页从上一页的最后一条继续读取，翻页期间有新数据写入时结果仍然连续。把 `page.CursorParams` 嵌入输入类型，用 `page.Cursor[T]` 作为响应体：

```go
type ListPublishedInput struct {
    page.CursorParams
}

type ListPublishedOutput struct {
    Body page.Cursor[ArticleDTO]
}
```

游标中保存排序位置，结构由应用定义。按发布时间倒序列出文章，`db` 是 `*bun.DB`：

```go
type articleKey struct {
    At time.Time `json:"at"`
    ID uuid.UUID `json:"id"`
}

var rows []Article
q := db.NewSelect().Model(&rows).
    Where("status = 'published'").
    OrderExpr("published_at DESC, id DESC").
    Limit(in.Limit()) // 比 per_page 多一条，用来判断是否还有下一页

var after articleKey
if ok, err := in.After(&after); err != nil {
    return nil, err // 游标无法解析：422 page.invalid_cursor
} else if ok {
    q.Where("(published_at, id) < (?, ?)", after.At, after.ID)
}
if err := q.Scan(ctx); err != nil {
    return nil, errors.Wrap(err, "list published")
}

// NewCursor 截掉多读的一条，把当前页最后一条的排序位置编码为 next_cursor。
body := page.NewCursor(toDTOs(rows), in.CursorParams, func(a ArticleDTO) any {
    return articleKey{*a.PublishedAt, a.ID}
})
return &ListPublishedOutput{Body: body}, nil
```

- 客户端把 `next_cursor` 原样作为下一次请求的 `cursor`；最后一页的响应省略 `next_cursor`。
- `key` 返回的值与 `After` 解码的结构相同。

> [!WARNING]
> 排序必须确定且唯一，例如 `ORDER BY published_at DESC, id DESC`，`WHERE` 中的比较方向与排序一致。只按 `published_at` 排序时，同一时间发布的文章在翻页时会重复或遗漏。

<a id="filtering-and-sorting"></a>
### 筛选与排序

筛选和排序参数与分页参数写在同一个结构体中，它直接作为输入类型：

```go
type ListQuery struct {
    Q      string `query:"q"`
    Status string `query:"status" enum:"draft,published"`
    Sort   string `query:"sort" default:"-created" enum:"-created,created,title,-title"`
    page.OffsetParams
}

// orders 是 Sort 每个取值对应的排序。
var orders = map[string]string{
    "created":  "created_at, id",
    "-created": "created_at DESC, id DESC",
    "title":    "title, id",
    "-title":   "title DESC, id DESC",
}

// Handle 返回符合条件的一页文章和总数。
func (a *List) Handle(ctx context.Context, subject identity.Subject, q ListQuery) ([]Article, int64, error) {
    var items []Article
    sel := a.db.NewSelect().Model(&items).
        OrderExpr(orders[q.Sort]).
        Limit(q.Limit()).
        Offset(q.Offset())
    if q.Status != "" {
        sel.Where("status = ?", q.Status)
    }
    if q.Q != "" {
        sel.Where("title LIKE ?", "%"+q.Q+"%")
    }
    total, err := sel.ScanAndCount(ctx)
    if err != nil {
        return nil, 0, errors.Wrap(err, "list articles")
    }
    return items, total, nil
}
```

`HTTP` 方法收到 `*ListQuery`，调用 `Handle`，返回 `page.NewOffset(toDTOs(items), in.OffsetParams, total)`。`/articles?status=draft&sort=title&page=2` 得到按标题排列的第二页草稿。

- `sort` 的值是列名，加 `-` 表示降序，与 `@go-tenon/ui` 的表格点击列头时写入地址的值相同。不在 `enum` 中的值返回 `422`；空值等同于没有这个参数，取 `default`。
- SQL 中只出现 `orders` 里写好的表达式，`enum` 已经保证能查到。增加排序方式时 `enum` 和 `orders` 一起改。
- 每种排序以 `id` 结尾：标题相同的文章顺序不固定，翻页时会重复或遗漏。
- 按调用者限定范围的条件写在同一个查询中，见[列表查询中的授权](../security/authorization.md#scoping-queries)。需要分词和相关度排序时用[搜索](../features/search.md)。
- 游标分页的排序固定写在业务动作中，不提供 `sort` 参数：换一种排序，原来的游标就失效了。

<a id="period"></a>
## 时间窗口与分桶：`period` 包

报表和统计接口嵌入 `period.Params`（`from`、`to`、`tz`、`granularity`），用 `period.Resolve` 按时区换算 UTC 半开区间 `[Start, End)` 和等长上一对比期 `[PrevStart, PrevEnd)`，用 `period.FillSeries` 按连续时间桶补零：

```go
type ReportInput struct {
    period.Params
    TeamID string `query:"team_id"`
}

w, err := period.Resolve(in.Params, period.Options{DefaultZone: "Asia/Shanghai"})
if err != nil {
    return nil, err // 非法时区、日期倒挂、超过跨度上限时返回 422
}
// SQL 使用 WHERE created_at >= w.Start AND created_at < w.End（不用 NOW()）
series := period.FillSeries(w, rows, func(p Point) string { return p.Date }, func(d string) Point { return Point{Date: d} })
```

- 跨度不超过 62 天且未指定粒度时默认 `day`，超过时自动切换 `week`（按周一起始，与 PostgreSQL `date_trunc('week', ...)` 对齐）。
- 响应体嵌入 `w.Meta()`（`period.Meta`）向前端回显实际生效的日期范围、时区、粒度与对比期。

<a id="returning-errors"></a>
## 返回错误

输出类型只描述成功的结果。失败时处理器返回 `nil` 和错误，`rest.Register` 把它转换为错误响应并选择状态码；业务错误的定义和状态码见[错误处理](../general/errors.md#returning-errors)。
