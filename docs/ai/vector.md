# 向量检索

`vector` 按语义检索：用嵌入模型把文字（或图片）算成向量存进 [Qdrant](https://qdrant.tech)，提问时找出向量最接近的文档。用法与[全文搜索](../features/search.md)相同：业务动作在修改数据的事务中调用 `Sync`，队列任务重新读取数据、计算向量后写入 Qdrant。

包：`vector`、`vector/vectortest`。

- 数据以数据库为准，Qdrant 中只是由它算出来的副本，丢失后[重建索引](#reindexing)即可，只是要再付一次计算向量的费用。

<a id="example-usage"></a>
## 示例

```go
var Chunks = vector.Define[ChunkDoc]("attachment-chunks")

// 修改数据的事务中发起同步，提交后由 worker 计算向量并写入 Qdrant
err := Chunks.Sync(ctx, tx, fileID.String())

hits, err := Chunks.Search(ctx, vectors, vector.Query{Input: vector.Text("如何退款"), Limit: 5})
```

`tx` 是修改数据的事务，`vectors` 是入口创建的 `*vector.Store`（见[连接 Qdrant](#connecting)），`ChunkDoc` 见[声明索引](#defining-indexes)。

<a id="installation"></a>
## 安装

```bash
go get github.com/qdrant/go-client@v1.20.1
```

把 `vector.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)。同步任务由[队列](../background/queue.md)执行，重建的进度记在[后台操作](../features/operations.md)中，所以同时发布 `queue.Migrations()` 和 `operation.Migrations()`，再执行 `migrate up`。

<a id="connecting"></a>
## 用 `vector.New` 连接 Qdrant

Qdrant 客户端直接用官方的 [go-client](https://github.com/qdrant/go-client)，走 gRPC 端口 6334。在[入口](../concepts/dependencies.md#assembling-the-application)的 `Open` 中创建客户端和 `vector.Store`，登记关闭：

```go
qc, err := qdrant.NewClient(&qdrant.Config{
    Host: cfg.Qdrant.Host, Port: cfg.Qdrant.Port, APIKey: cfg.Qdrant.APIKey, UseTLS: cfg.Qdrant.TLS,
})
if err != nil {
    return nil, err
}
stack.MustAdd("qdrant", func(context.Context) error { return qc.Close() })

vectors := vector.New(db, qc, vector.Model{
    Name:     cfg.Embedding.Model,
    Dims:     1024,
    Embedder: openai.New(cfg.Embedding, openai.WithBatchSize(10)),
})
```

- `cfg.Qdrant` 是应用自己定义的配置结构，`cfg.Embedding` 是 `ai.ModelConfig`，`db` 是 `*bun.DB`，`stack` 是入口的关闭栈。
- `Dims` 必须与模型输出的维度一致，否则写入时报错。
- `Embedder` 是 [ai.Embedder](chat.md#media)；分段多时用 `openai.WithBatchSize` 分批请求。
- 多个应用共用一个 Qdrant 时，用 `vector.WithPrefix("blog_")` 给 collection 名称加前缀。

<a id="defining-indexes"></a>
## 用 `vector.Define` 声明索引

[文档转换与切分](documents.md#processing-uploads)把附件的分段存进 `attachment_chunks` 表（模型 `AttachmentChunk`）。业务包的 `vector.go` 声明索引的文档类型、索引和读取文档的函数：

```go
// ChunkDoc 是一个分段在向量索引中的文档，只放回答问题需要的字段。
type ChunkDoc struct {
    ArticleID uuid.UUID `json:"article_id" vector:"filter"`
    FileID    uuid.UUID `json:"file_id" vector:"filter"`
    Text      string    `json:"text"`
    Pages     []int     `json:"pages"`
    Context   string    `json:"-" vector:"embed"` // 带标题路径的正文，只用来计算向量
}

// Chunks 是附件分段的向量索引，key 是附件 ID。
var Chunks = vector.Define[ChunkDoc]("attachment-chunks")

// LoadChunks 读取附件当前的分段；附件删除后返回空，索引中的文档随之删除。
func LoadChunks(ctx context.Context, tx bun.IDB, key string) ([]ChunkDoc, error) {
    // 用 tx 按 position 读出 file_id = key 的 AttachmentChunk，逐个转换为 ChunkDoc；
    // key 不是合法的 UUID 时返回 queue.Permanent(err)，任务不再重试
}
```

| 标签 | 用途 | 字段类型 |
| --- | --- | --- |
| `embed` | 计算向量的内容，有且只有一个；只用来计算时写 `json:"-"` | `string`，或 `vector.Content`（见[图片](#images)） |
| `filter` | 过滤用的值，见[过滤](#filtering) | 字符串、布尔、整数、浮点数、`time.Time`、实现 `encoding.TextMarshaler` 的类型（如 `uuid.UUID`），以及它们的切片和指针 |
| `tenant` | 文档所在的租户，见[多租户](#tenancy) | `string` |

- 一个 key 可以对应多个文档：这里以附件 ID 为 key，每个分段一个文档。
- 文档整体编码为 JSON 存在 Qdrant 中，检索结果直接解码出 `ChunkDoc`。
- 标签写错时 `Define` panic，启动时就能发现。
- 重建索引还要一个 `ScanFunc`，见[重建索引](#reindexing)。

<a id="syncing"></a>
## 用 `Sync` 同步、`Handle` 处理同步任务

修改数据的事务中调用 `Sync`，例如处理附件的任务替换分段之后：

```go
return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    // ……删除附件原有的分段，写入新的
    return Chunks.Sync(ctx, tx, job.FileID.String())
})
```

worker 注册同步任务和重建任务的处理函数，加在 [worker 的装配](../background/queue.md#running-workers)中，`a.vectors` 是入口创建的 `*vector.Store`：

```go
attachment.Chunks.Handle(w, a.vectors, attachment.LoadChunks, attachment.ScanChunkKeys)
```

- 任务只带 key，每次在同步事务中用 `LoadFunc` 重新读取，重复或乱序执行的结果都一样；返回空时从索引中删除这个 key 的文档，所以删除附件也调用 `Sync`。
- 同一个 key 的同步依次执行，锁一直持有到写完 Qdrant，计算向量期间同步事务也保持打开。所以 `LoadFunc` 只读取数据，转换、切分文件这类耗时的处理放在之前的任务中完成。
- 计算向量的内容为空、引用的图片不存在、模型不接受这种内容时，任务直接转入[失败任务表](../background/queue.md#failed-jobs)；模型服务出错时按队列的规则重试。
- Qdrant 的 collection 在第一次写入时自动创建，并给 `filter` 字段建索引；之后新增的 `filter` 字段在进程启动后第一次同步时补建。

<a id="reusing-vectors"></a>
### 沿用向量

同步时计算向量的内容与这个 key 已有的文档相同的，沿用原来的向量，只把新的内容交给模型。改了附件中的一句话，只有那一段重新计算；图片按文件 ID 比较。

<a id="refreshing"></a>
### 用 `Refresh` 立即同步

`Refresh` 直接同步并返回，用于测试和需要马上能查到的场合，`vectors` 是 `*vector.Store`：

```go
err := attachment.Chunks.Refresh(ctx, vectors, attachment.LoadChunks, fileID.String())
```

<a id="reindexing"></a>
## 重建索引：`search reindex`

换了模型、改了计算向量的文本（例如 `Context` 的拼法）、加上或去掉 `Hybrid`，或者有数据绕过 `Sync` 被修改过，要重建索引。重建用 `ScanFunc` 按稳定的顺序列出 `after` 之后的最多 `limit` 个 key，返回空时结束，写法与[全文搜索的 `ScanFunc`](../features/search.md#reindexing)相同：

```go
// ScanChunkKeys 按 file_id 的顺序列出 after 之后有分段的附件，重建索引时使用。
func ScanChunkKeys(ctx context.Context, db bun.IDB, after string, limit int) ([]string, error)
```

重建命令与[全文搜索](../features/search.md#reindexing)共用 `search/searchcmd`，把向量索引一起传入，在 [`command.Bind`](../general/commands.md#bind-commands) 中声明：

```go
searchcmd.New(a.db, article.SearchIndex, attachment.Chunks)
```

部署新版本之后执行 `./bin/blog search reindex attachment-chunks`，命令每秒显示进度直到完成，`--detach` 发起后立即返回。

- 重建写入索引的新版本（新的 collection），完成之前查询仍用原来的版本；期间同步同时写入两个版本，完成后切换。
- 新版本的每个文档都要重新计算向量，重建前估算费用。
- 同一个索引再次发起重建时，正在进行的重建被取代，它的操作变为失败，错误码 `vector.reindex_superseded`。
- 重建是一个[后台操作](../features/operations.md)，类型是 `vector.reindex.attachment-chunks`；在管理后台发起时调用 `attachment.Chunks.Reindex(ctx, tx, ops, sub)`。

换模型的步骤见[换模型](#changing-models)。

<a id="searching"></a>
## 用 `Search` 检索

`Search` 用建立索引的模型计算查询的向量，返回最接近的文档，按相似度从高到低排列。例如查找一篇文章的附件中与问题最相关的分段：

```go
hits, err := Chunks.Search(ctx, a.vectors, vector.Query{
    Input:  vector.Text(question),
    Filter: vector.Eq("article_id", articleID),
    Limit:  20,
})
```

`a.vectors` 是入口创建的 `*vector.Store`。

- 每条结果 `Hit` 有 `Key`、解码后的 `Doc` 和 `Score`（余弦相似度，越大越接近）。
- `Limit` 默认 10。`MinScore` 去掉相似度太低的结果，阈值因模型而异，先看实际的分数再定。

<a id="filtering"></a>
### 过滤

过滤条件按 JSON 名称引用 `filter` 字段，写法与[全文搜索的过滤](../features/search.md#filtering-and-sorting)相同：

```go
vector.And(
    vector.Eq("article_id", articleID),
    vector.Not(vector.In("file_id", hidden...)),
)
```

- `Eq`、`In` 适用于所有类型；`Gt`、`Gte`、`Lt`、`Lte` 只适用于数字和时间。
- 多值字段（切片）只要有一个值满足条件就算满足。
- 字段不存在、值的类型不对时 `Search` 返回错误。
- Qdrant 在检索的同时过滤，返回满足条件的文档中最接近的 `Limit` 条。

<a id="tenancy"></a>
### 多租户

文档类型有 `tenant` 字段时，检索总是限定在 `Query.Tenant` 中，传入 `sub.Tenant`：

```go
hits, err := Chunks.Search(ctx, a.vectors, vector.Query{Input: vector.Text(question), Tenant: sub.Tenant})
```

`sub` 是当前的 `identity.Subject`。`Tenant` 为空表示平台本身，只查询租户为空的文档；没有 `tenant` 字段的索引传入租户时返回错误。Qdrant 按租户分别存放数据、建 HNSW 图，查询一个租户的速度只取决于它自己的数据量。

<a id="hybrid-search"></a>
### 用 `vector.Hybrid` 混合检索

型号、编号、人名这类问题，关键词检索比语义检索准。把索引声明为混合索引，语义和关键词两路同时检索，在 Qdrant 中按排名用 RRF 融合：

```go
var Chunks = vector.Define[ChunkDoc]("attachment-chunks", vector.Hybrid(search.Bigram))
```

- 同步时 `embed` 字段的文本还用[分词器](../features/search.md#tokenizers)切分，算出 BM25 的稀疏向量；IDF 按整个索引统计，有租户字段时所有租户一起统计。
- 中文用 `search.Bigram`；需要按词语切分时换成 `search/gse`。
- `Hit.Score` 是 RRF 分数，只能比较同一次查询的结果；`MinScore` 只过滤语义那一路的候选。
- 加上或去掉 `Hybrid`、换分词器之后要[重建索引](#reindexing)，完成之前原来的版本照常使用。

<a id="reranking"></a>
### 用 `ai.RerankBy` 重排

多取一些候选，再用[重排模型](chat.md#media)排出最相关的几段交给模型：

```go
hits, err = ai.RerankBy(ctx, a.reranker, ai.Text(question), hits, func(h vector.Hit[ChunkDoc]) ai.Content {
    return ai.Text(h.Doc.Text)
}, 5)
```

`hits` 是[检索](#searching)得到的结果，`a.reranker` 是 `ai.Reranker`。

<a id="images"></a>
## 图片和视频

用多模态嵌入模型给图片算向量，文字和图片在同一个空间中比较，可以用文字搜图片。模型见[图片和视频向量](chat.md#image-embeddings)。`Model.Inputs` 声明模型接受图片，`WithFiles` 让 `Store` 从[文件存储](../libraries/filesystem.md)读取图片：

```go
vectors := vector.New(db, qc, vector.Model{
    Name:     cfg.Embedding.Model, // qwen3-vl-embedding
    Dims:     1024,
    Embedder: dashscope.New(cfg.Embedding, dashscope.WithDimensions(1024)),
    Inputs:   []ai.Modality{ai.ModalityText, ai.ModalityImage},
}, vector.WithFiles(files))
```

`files` 是 `*filesystem.Service`。`embed` 字段用 `vector.Content`，图片用 `FilePart` 引用文件 ID，例如商品的标题一个文档、每张图片一个文档：

```go
type ProductDoc struct {
    Title  string         `json:"title"`
    Kind   string         `json:"kind" vector:"filter"` // title 或 image
    Tenant string         `json:"-" vector:"tenant"`
    Embed  vector.Content `json:"-" vector:"embed"`
}

var Products = vector.Define[ProductDoc]("products")

// 在 LoadProducts 中，p 是读出的商品
docs := []ProductDoc{{Title: p.Title, Kind: "title", Tenant: p.Tenant, Embed: vector.Text(p.Title)}}
for _, img := range p.ImageIDs {
    docs = append(docs, ProductDoc{Title: p.Title, Kind: "image", Tenant: p.Tenant, Embed: vector.Content{vector.FilePart(img)}})
}
```

- `vector.Content{vector.TextPart(p.Title), vector.FilePart(p.CoverID)}` 把文字和图片融合为一个向量，只有支持融合的模型可用。
- 视频的写法相同，`Model.Inputs` 加上 `ai.ModalityVideo`。
- 文件超过 `vector.MaxFileSize`（50 MiB）、已删除，或模型的 `Inputs` 没有这种内容时，同步任务转入失败任务表。
- 混合索引中只有图片的文档没有关键词，只参与语义那一路。

一个 key 有多个文档时，`GroupByKey` 让每个 key 只返回最接近的一个：

```go
hits, err := Products.Search(ctx, a.vectors, vector.Query{
    Input: vector.Text("红色连衣裙"), Tenant: sub.Tenant, GroupByKey: true, Limit: 20, // 20 个商品
})
```

以文搜图的相似度普遍偏低（常在 0.5 左右），不设 `MinScore`，按排名取前几个。

<a id="image-queries"></a>
### 以图搜图

查询也可以是图片：用户上传的图片用 `MediaPart`，文件存储中已有的用 `FilePart`：

```go
hits, err := Products.Search(ctx, a.vectors, vector.Query{
    Input:      vector.Content{vector.MediaPart(ai.Media{Data: data, MIME: "image/jpeg"})},
    GroupByKey: true,
})
```

`data` 是图片的字节。查询内容同样按 `Model.Inputs` 检查，只接受文字的模型收到图片时 `Search` 直接返回错误。AI 助手中用户发来的照片，用 `chat.FindAttachment` 取出再检索，见[在工具中使用附件](chat.md#attachments-in-tools)。

<a id="changing-models"></a>
## 用 `WithPrevious` 换模型

索引的每个版本记录建立它的模型名称和维度，写入和查询这个版本总用这个模型；`Store` 中没有这个模型时同步和检索都返回错误。名称或维度变了都算换模型，例如用 `openai.WithDimensions` 从 3072 维改为 1024 维。换模型分三步：

1. 配置改为新模型，旧模型用 `vector.WithPrevious` 传入，部署。这时查询和同步仍用旧模型建立的版本：

    ```go
    vectors := vector.New(db, qc,
        vector.Model{Name: cfg.Embedding.Model, Dims: 1536, Embedder: openai.New(cfg.Embedding)},
        vector.WithPrevious(vector.Model{Name: cfg.OldEmbedding.Model, Dims: 1024, Embedder: openai.New(cfg.OldEmbedding)}),
    )
    ```

2. 执行 `search reindex attachment-chunks`。新版本用新模型建立，重建期间的修改同时用两个模型计算。
3. 重建完成后去掉 `WithPrevious` 和旧模型的配置，再部署。

`vector.Doctor(store, 索引名...)` 检查每个索引的版本是否由配置的模型建立，换了模型却没有传入旧模型时失败，见[自检](../installation/doctor.md#framework-checks)。

<a id="testing"></a>
## 测试：`vectortest.New`

`vectortest.New` 创建连接测试 Qdrant 的 `Store`，地址来自环境变量 `QDRANT_TEST_ADDR`，每次使用随机的 collection 前缀，测试结束时删除。计算向量用 [aitest.Embedder](chat.md#testing)：

```go
func TestSearchChunks(t *testing.T) {
    ctx := context.Background()
    db := dbtest.Open(t, migrations.FS)
    vectors := vectortest.New(t, db, vector.Model{Name: "test", Dims: 64, Embedder: aitest.NewEmbedder(64)})
    // ……写入文章、附件和分段；这里的分段没有标题，计算向量的文本就是 Text
    if err := attachment.Chunks.Refresh(ctx, vectors, attachment.LoadChunks, fileID.String()); err != nil {
        t.Fatal(err)
    }
    hits, err := attachment.Chunks.Search(ctx, vectors, vector.Query{Input: vector.Text("验收后 30 日内结清")})
    if err != nil || len(hits) == 0 || hits[0].Doc.Text != "验收后 30 日内结清" {
        t.Fatalf("hits = %+v, err = %v", hits, err)
    }
}
```

- `aitest.Embedder` 给相同的内容相同的向量，不同的内容几乎正交：测得了同步、过滤和租户，测不了语义检索的效果。查询要与计算向量的文本完全相同：分段有标题时向量来自 `Context`（标题路径加正文），查询也用它。
- 测试以文搜图时，`vectortest.New` 的最后一个参数传 `vector.WithFiles`（文件服务或实现 `vector.Files` 的内存替身），再用 `aitest.Embedder` 的 `Same(ai.Text("红色连衣裙"), ai.Content{ai.MediaPart(redDress)})` 让描述和图片得到相同的向量，`redDress` 是与文件服务读出的内容相同的 `ai.Media`。
