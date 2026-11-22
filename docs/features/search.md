# 全文搜索

`search` 在应用自己的 PostgreSQL 中提供全文搜索：按关键词查找，带过滤、排序、分页、分面统计和命中词高亮。中文在应用中分词，只用到 PostgreSQL 自带的全文检索，托管数据库上同样可用。

包：`search`、`search/gse`、`search/searchcmd`。

- 索引不跟随模型的保存自动同步。业务动作在修改数据的事务中调用 `Sync`，同步作为[队列](../background/queue.md)任务和数据一起提交，事务回滚时同步一起取消。

<a id="example-usage"></a>
## 示例

```go
type SearchDoc struct {
    ID       uuid.UUID `json:"id"`
    Title    string    `json:"title" search:"text,boost"`
    Content  string    `json:"content" search:"text"`
    AuthorID string    `json:"author_id" search:"filter"`
}

var SearchIndex = search.Define[SearchDoc]("articles")

// 修改文章的事务中发起同步，提交后由 worker 更新索引
err := SearchIndex.Sync(ctx, tx, item.ID.String())

res, err := SearchIndex.Search(ctx, db, search.Query{
    Text:   "数据库 迁移",
    Filter: search.Eq("author_id", "alice"),
})
```

`tx` 是修改文章的事务，`db` 是入口打开的 `*bun.DB`。

<a id="installation"></a>
## 安装

1. 把 `search.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)。同步任务由队列执行，重建的进度记在[后台操作](operations.md)中，所以同时发布 `queue.Migrations()` 和 `operation.Migrations()`，再执行 `migrate up`。
2. 在 worker 中用 `Handle` 注册同步和重建任务，见[同步](#syncing)。
3. 在命令中加入 `searchcmd.New`，见[重建索引](#reindexing)。

<a id="defining-indexes"></a>
## 用 `search.Define` 声明索引

索引的文档是一个结构体，字段的 `search` 标签说明它的用途。索引放在业务包的 `search.go` 中：

```go
type SearchDoc struct {
    ID          uuid.UUID `json:"id"`
    Slug        string    `json:"slug"` // 没有标签：只用于展示
    Title       string    `json:"title" search:"text,boost"`
    Content     string    `json:"content" search:"text"`
    AuthorID    string    `json:"author_id" search:"filter"`
    PublishedAt time.Time `json:"published_at" search:"filter"`
}

var SearchIndex = search.Define[SearchDoc]("articles")
```

| 标签 | 用途 | 字段类型 |
| --- | --- | --- |
| `text` | 参与全文检索的文本，至少有一个；`text,boost` 匹配时权重更高，例如标题 | `string`、`[]string` |
| `filter` | 过滤、排序和分面用的值 | 字符串、布尔、整数、浮点数、`time.Time`、实现 `encoding.TextMarshaler` 的类型（如 `uuid.UUID`），以及它们的切片（多个值）和指针（`nil` 为没有值） |
| `tenant` | 文档所在的租户，见[多租户](#tenancy) | `string` |

- 查询、高亮和分面按字段的 JSON 名称引用字段，例如 `author_id`。
- 文档整体以 JSON 保存在索引中，搜索结果直接解码出 `SearchDoc`，所以只放展示结果需要的字段。
- 索引名（小写字母、数字、`_`、`.`、`-`，最长 63 字节）或标签不合法时 `Define` panic。

<a id="syncing"></a>
## 用 `Sync` 同步，用 `Handle` 处理同步任务

修改文章的业务动作在事务的最后调用 `Sync`，删除文章同样调用它。`a.db` 是 `*bun.DB`：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    // ……读取、检查并更新文章
    return SearchIndex.Sync(ctx, tx, item.ID.String())
})
```

`Sync` 发布一个带着 key 的队列任务，事务提交后才执行。任务用 `LoadFunc` 读取 key 当前的文档，替换索引中这个 key 原有的文档，返回空时从索引中删除：

```go
// LoadSearchDocs 读取文章当前的状态：已发布的文章返回它的文档，草稿和已删除的文章返回空。
func LoadSearchDocs(ctx context.Context, tx bun.IDB, key string) ([]SearchDoc, error) {
    id, err := uuid.Parse(key)
    if err != nil {
        return nil, queue.Permanent(err) // 不合法的 key 不重试
    }
    var items []Article
    err = tx.NewSelect().Model(&items).Where("id = ? AND status = ?", id, StatusPublished).Scan(ctx)
    if err != nil {
        return nil, errors.Wrap(err, "load article")
    }
    docs := make([]SearchDoc, len(items))
    for i, item := range items {
        docs[i] = SearchDoc{ID: item.ID, Title: item.Title, Content: item.Content /* …… */}
    }
    return docs, nil
}
```

在 [worker 的装配](../background/queue.md#running-workers)中注册同步和重建任务，`w` 是 `*queue.Worker`，`ScanSearchKeys` 见[重建索引](#reindexing)：

```go
article.SearchIndex.Handle(w, a.db, article.LoadSearchDocs, article.ScanSearchKeys)
```

- 同一个 key 的同步依次执行，`LoadFunc` 执行期间持有这个 key 的锁，所以它只读取数据，耗时的处理放在之前的任务中。
- 一个文档最多索引前 `search.MaxTerms`（16383）个词，之后的文字搜不到；更长的内容按[一个 key 多个文档](#multiple-documents)切分。
- 文档无法建索引时（例如过滤字段的值编码后超过 191 字节），任务直接转入[失败任务](../background/queue.md#failed-jobs)。
- 修改了 `SearchDoc` 的字段或[分词器](#tokenizers)之后要[重建索引](#reindexing)。

<a id="multiple-documents"></a>
### 一个 key 多个文档

`LoadFunc` 返回切片，一个 key 可以对应多个文档。例如[文档转换与切分](../ai/documents.md#processing-uploads)把附件切成多个分段，以附件 ID 为 key、每个分段一个文档：搜索命中具体的分段，`Hit.Key` 是附件 ID。重新切分后同步，附件原有的分段被整体替换。

<a id="refreshing"></a>
### 用 `Refresh` 立即同步

`Refresh` 直接同步并返回，每个 key 一个事务，用于测试和需要马上能搜到的场合。`db` 是 `*bun.DB`：

```go
err := article.SearchIndex.Refresh(ctx, db, article.LoadSearchDocs, item.ID.String())
```

<a id="reindexing"></a>
## 重建索引：`search reindex`

修改了 `SearchDoc` 的字段或[分词器](#tokenizers)，或者有数据绕过 `Sync` 被修改过，要重建索引。重建用 `ScanFunc` 按稳定的顺序列出 `after` 之后可能有文档的 key，返回空时结束：

```go
// ScanSearchKeys 按 ID 的顺序列出 after 之后的已发布文章，重建索引时使用。
func ScanSearchKeys(ctx context.Context, db bun.IDB, after string, limit int) ([]string, error) {
    q := db.NewSelect().Model((*Article)(nil)).ColumnExpr("id::text").
        Where("status = ?", StatusPublished).OrderExpr("id").Limit(int64(limit))
    if after != "" {
        q = q.Where("id > ?::uuid", after)
    }
    var keys []string
    return keys, errors.Wrap(q.Scan(ctx, &keys), "scan articles")
}
```

`search/searchcmd` 提供重建命令，在 [`command.Bind`](../general/commands.md#bind-commands) 中声明，`a.db` 是 `*bun.DB`：

```go
searchcmd.New(a.db, article.SearchIndex)
```

部署新版本之后执行：

```bash
./bin/blog search reindex articles           # 由 worker 重建，命令每秒显示进度，Ctrl-C 只停止查看
./bin/blog search reindex articles --detach  # 发起后立即返回
```

重建失败时命令以非 0 状态退出。

<a id="reindex-versions"></a>
### 重建期间的查询

重建写入索引的一个新版本，完成之前查询仍使用原来的版本，用户始终搜到完整的索引：

- 重建期间的同步同时写入新旧两个版本。重建每批处理 500 个 key，保存一次进度，任务重试时从上次的位置继续。
- 新加的过滤字段在原来的版本中没有值。新加过滤字段时分两次部署：先部署只修改 `SearchDoc` 的版本并重建，再部署用到这个字段的查询。
- 再次发起重建时，正在进行的重建失败，错误码是 `search.reindex_superseded`。
- 无法建索引的 key 计为失败，其他 key 照常处理。

重建是一个[后台操作](operations.md)，类型是 `search.reindex.<索引名>`。在管理后台发起时调用 `Reindex`，操作属于发起的管理员。`tx` 是事务，`ops` 是 `*operation.Service`，`sub` 是当前主体：

```go
op, err := article.SearchIndex.Reindex(ctx, tx, ops, sub)
```

<a id="searching"></a>
## 用 `Search` 搜索

搜索接口的业务动作放在 `search.go` 中。`Result` 嵌入了 [page.Offset](../http/responses.md#offset-pagination)，直接作为响应体；`a.db` 是 `*bun.DB`：

```go
type SearchInput struct {
    Q string `query:"q" maxLength:"100"`
    page.OffsetParams
}

type SearchOutput struct {
    Body *search.Result[SearchDoc]
}

func (a *Search) HTTP(ctx context.Context, in *SearchInput) (*SearchOutput, error) {
    res, err := SearchIndex.Search(ctx, a.db, search.Query{Text: in.Q, Page: in.OffsetParams})
    if err != nil {
        return nil, err
    }
    return &SearchOutput{Body: res}, nil
}
```

```json
{
  "items": [{"key": "0193…", "doc": {"id": "0193…", "title": "全文搜索的设计"}, "score": 0.61,
             "highlights": {"title": [{"text": "全文"}, {"text": "搜索", "mark": true}, {"text": "的设计"}]}}],
  "page": 1, "per_page": 20, "total": 1
}
```

- 查询文本切分出的每个词都要出现在文档中，最后一个词按前缀匹配，输入到一半（`datab`、`搜`）也能搜到。
- 查询文本为空时列出符合条件的所有文档，按 key 排序；有查询文本时按相关度排序，`text,boost` 字段命中的在前。
- `Score` 只用于比较同一次查询的结果。

<a id="filtering-and-sorting"></a>
### 过滤与排序：`Filter`、`Sort`

`Filter` 用 `Eq`、`In`、`InQuery`、`KeyInQuery`、`Gt`、`Gte`、`Lt`、`Lte` 比较过滤条件，用 `And`、`Or`、`Not` 组合。`q` 是 `search.Query`，`authors` 是 `[]string`，`visible` 是返回一列文本的 `*bun.SelectQuery`：

```go
q.Filter = search.And(
    search.In("author_id", authors...),
    search.InQuery("conversation_id", visible),
    search.Gte("published_at", time.Now().AddDate(0, -1, 0)),
)
q.Sort = []search.Sort{search.Relevance(), search.Desc("published_at")}
```

- 值按字段的类型比较，数字和时间按大小比较。
- `InQuery` 和 `KeyInQuery` 把同库业务表的动态权限或筛选子查询直接并入搜索；`InQuery` 仅用于字符串过滤字段。
- 切片字段只要有一个值满足条件就算满足，`Not(Eq("tags", "go"))` 表示没有一个标签是 `go`。
- `Sort` 只用于单值的 `filter` 字段，可以有多个，没有值的文档排在最后；用 `search.Relevance()` 指定相关度在排序中的位置，未指定时有查询文本的排在 `Sort` 之后，无查询文本时自动忽略。

<a id="collapsing"></a>
### 折叠结果：`Collapse`

一个父对象有多个索引文档（例如会话的多条消息、附件的多个分段）时，设置 `Query.Collapse` 按每组保留排在最前的一条文档，组内命中的总文档数记在 `Hit.GroupCount` 中，`Total` 和分面按折叠后的分组统计。`db` 是 `*bun.DB`：

```go
res, err := MessageIndex.Search(ctx, db, search.Query{
    Text:     "退款",
    Collapse: "conversation_id", // 或传 search.ByKey 按文档 key 折叠
    Sort:     []search.Sort{search.Relevance(), search.Desc("created_at")},
})
```

- `Collapse` 接受 `search.ByKey`（按 `doc_key` 折叠）或任意单值 `filter` 字段名；该字段为空的文档各自独立成组。

<a id="facets"></a>
### 分面：`Facets`

`Facets` 统计结果中每个值的文档数，用于“按标签筛选（Go 12、Rust 3）”这样的界面。`in.Q` 是查询文本：

```go
res, err := SearchIndex.Search(ctx, db, search.Query{Text: in.Q, Facets: []string{"tags"}})
// res.Facets["tags"] 是 []search.FacetValue{{Value: "go", Count: 12}, {Value: "rust", Count: 3}, ...}
```

统计范围是满足全部查询条件的结果，按文档数从多到少，每个字段最多返回 `Query.FacetLimit` 个值（默认 20）。

<a id="highlighting"></a>
### 高亮：`Hit.Highlights`、`Index.Highlight`

有查询文本时，`Hit.Highlights` 按字段的 JSON 名称给出命中的文本字段的摘要，摘要按命中与否分成若干段（`[{"text": "…", "mark": true}, …]`），页面中把 `mark` 的部分放进 `<mark>`。字段不超过 `search.SnippetLength`（160）个字符时摘要是全文，否则从第一个命中的词前面不远处截取，首尾加省略号。

要在界面中用同一个分词器高亮其他文本（例如详情页中的原文或联系人字段），直接调用 `Index.Highlight`：

```go
snippet, ok := SearchIndex.Highlight(in.Q, text)
```

<a id="tenancy"></a>
### 多租户：`search:"tenant"`

文档有 `tenant` 字段时，查询只返回 `Query.Tenant` 中的文档，`""` 为平台本身，与[多租户的数据隔离](../security/tenancy.md#data-isolation)相同。`sub` 是当前主体：

```go
type TicketDoc struct {
    ID     uuid.UUID `json:"id"`
    Tenant string    `json:"tenant" search:"tenant"`
    Title  string    `json:"title" search:"text,boost"`
}

res, err := TicketIndex.Search(ctx, db, search.Query{Text: in.Q, Tenant: sub.Tenant})
```

忘记传入租户时查到的是平台本身的文档，各租户的数据始终隔离。没有 `tenant` 字段的索引传入租户时返回错误。

<a id="tokenizers"></a>
## 中文分词：`search.Bigram`、`search/gse`

默认的 `search.Bigram` 把中日韩文字按相邻的两个字切分，“全文搜索”切分为“全文”“文搜”“搜索”；其他文字按连续的字母和数字切分，转为小写和半角。查询“文搜”也会命中“全文搜索”，召回多、精度一般。

按词语切分时使用 `search/gse`，它基于 [gse](https://github.com/go-ego/gse) 的中文词典：

```go
var SearchIndex = search.Define[SearchDoc]("articles", search.WithTokenizer(gse.Tokenizer))
```

建索引时它还输出长词中的短词（“搜索引擎”还输出“搜索”“引擎”），查询短词也能命中。代价是程序增大约 25 MB，第一次分词时加载词典约 0.5 秒，常驻内存约 300 MB。

其他分词方式实现 `search.Tokenizer`，用 `search.Split(text, cjk)` 处理字母、数字和标点，只提供切分中日韩文字的 `cjk` 函数。

> [!IMPORTANT]
> 建索引和查询使用同一个分词器。更换分词器之后要[重建索引](#reindexing)，否则旧文档搜不全。
