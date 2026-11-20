# 导入与导出

用 Excel（XLSX）或 CSV 批量导入数据，把列表导出成表格。几千行以内在一次请求中处理；更大的文件在队列中后台导入、导出，用户看到进度。

包：`sheet`、`sheet/imports`、`sheet/exports`、`form`（按字段的校验标签解码一行文本）。

- 导入的每一行解码为接口用的同一个结构体，按它的 Huma 标签校验，提示与[接口的校验提示](../http/requests.md#validation-messages)相同。
- 导出在查询条件中限定发起人能看到的范围，与[列表查询](../security/authorization.md#scoping-queries)相同；后台导出同样按发起人的权限查询。

<a id="example-usage"></a>
## 示例

逐行读取上传的 Excel 文件，`file` 是上传文件的 `io.Reader`：

```go
type ArticleRow struct {
    Title string `json:"title" sheet:"标题" minLength:"1"`
    Slug  string `json:"slug" sheet:"Slug"`
}

r, err := sheet.NewReader(file, sheet.XLSX)
if err != nil {
    return err
}
defer r.Close()
rows, err := sheet.Decode[ArticleRow](ctx, r)
if err != nil {
    return err
}
for rec, err := range rows {
    if err != nil {
        return err // 文件中途无法读取
    }
    if !rec.Valid() {
        continue // rec.Errors 以字段名为键，是这一行的校验提示
    }
    // rec.Data 是校验过的一行，rec.Line 是 Excel 中的行号
}
```

<a id="columns"></a>
## 用 `sheet` 标签对应表格的列

导入的每一行是一次“创建文章”，直接解码为创建接口的 `article.Changes`。`sheet` 标签写出这一列在表格中的名称：

```go
type Changes struct {
    Title   string `json:"title" sheet:"标题" minLength:"1" maxLength:"200"`
    Slug    string `json:"slug" sheet:"Slug" maxLength:"100" pattern:"^[a-z0-9]+(-[a-z0-9]+)*$"`
    Content string `json:"content" sheet:"正文,内容"`
}
```

- 表头与字段的 `json` 标签或 `sheet` 标签中的任意一个名称相同，这一列就对应这个字段。比较时忽略大小写、空白、连字符和下划线，`Published At`、`published_at` 都对应 `json:"published_at"`。
- `sheet` 标签用逗号写多个名称，第一个是[模板](#templates)的列名；`sheet:"-"` 的字段不从表格读取。
- 表格中多余的列被忽略。必填字段（没有 `omitempty`）找不到对应的列时，`Decode` 返回 `sheet.ErrMissingColumns`，提示中列出缺少的列名，整个文件被拒绝。

<a id="import-action"></a>
## 编写导入动作：逐行创建

`import.go` 复用[创建文章](../concepts/actions.md#direct-calls)的 `Create`，每一行单独提交：

```go
// Import 从表格批量创建文章。
type Import struct {
    create *Create
}

// ImportResult 是导入的结果：创建了多少篇文章，以及没有导入的行。
type ImportResult struct {
    Created  int             `json:"created"`
    Failures []ImportFailure `json:"failures"`
}

// ImportFailure 是没有导入的一行，Errors 以字段名为键。
type ImportFailure struct {
    Line   int               `json:"line"`
    Errors map[string]string `json:"errors"`
}

// Handle 逐行创建文章：一行失败时其他行照常导入，文件无法读取或数据库出错时返回错误。
func (a *Import) Handle(ctx context.Context, subject identity.Subject, r *sheet.Reader) (ImportResult, error) {
    result := ImportResult{Failures: []ImportFailure{}}
    records, err := sheet.Decode[Changes](ctx, r)
    if err != nil {
        return result, err
    }
    for rec, err := range records {
        if err != nil {
            return result, err
        }
        if rec.Valid() {
            if err := a.row(ctx, subject, &rec); err != nil {
                return result, err
            }
        }
        if !rec.Valid() {
            result.Failures = append(result.Failures, ImportFailure{Line: rec.Line, Errors: rec.Errors})
            continue
        }
        result.Created++
    }
    return result, nil
}

// row 用一行创建文章。slug 已被占用只是这一行的问题，标在 slug 字段上；其他错误原样返回。
func (a *Import) row(ctx context.Context, subject identity.Subject, rec *sheet.Record[Changes]) error {
    _, err := a.create.Handle(ctx, subject, rec.Data)
    if errors.Is(err, ErrSlugTaken) {
        rec.SetError("slug", i18n.T(ctx, ErrSlugTaken.Code(), nil))
        return nil
    }
    return err
}
```

- 只属于这一行的业务错误用 `rec.SetError` 标在字段上，接着处理下一行；没有权限、数据库不可用这类对每一行都一样的错误直接结束导入。[后台导入](#processing-imports)也用这个 `row`。
- 成功的行就此保存，用户改正失败的行后重新导入。要求全部成功才写入时，先遍历一次只做校验，再在一个事务中写入。

<a id="importing"></a>
## 上传表格导入的接口

`Import` 的 `HTTP` 方法接收 `multipart/form-data` 上传的文件，`sheet.FormatOf` 按扩展名得到格式，不是 `.csv` 或 `.xlsx` 时返回 `sheet.ErrFormat`：

```go
type ImportInput struct {
    RawBody huma.MultipartFormFiles[struct {
        File huma.FormFile `form:"file" required:"true"`
    }]
}

// HTTP 处理 POST /articles/import。
func (a *Import) HTTP(ctx context.Context, in *ImportInput) (*ImportOutput, error) { // ImportOutput.Body 是 ImportResult
    subject, _ := identity.FromContext(ctx)
    file := in.RawBody.Data().File
    format, err := sheet.FormatOf(file.Filename)
    if err != nil {
        return nil, err
    }
    r, err := sheet.NewReader(file, format)
    if err != nil {
        return nil, err
    }
    defer r.Close()
    result, err := a.Handle(ctx, subject, r)
    if err != nil {
        return nil, err
    }
    return &ImportOutput{Body: result}, nil
}
```

`routes.go` 中用 `rest.Register(api, op, NewImport(NewCreate(db)).HTTP)` 注册，见[路由](../http/routing.md)。响应：

```json
{"created": 41, "failures": [{"line": 7, "errors": {"title": "不能为空"}}, {"line": 12, "errors": {"slug": "slug 已被占用"}}]}
```

命令行入口用 `os.Open` 打开文件、`sheet.FormatOf(path)` 取得格式，再调用同一个 `Handle`，见[命令行](../concepts/actions.md#as-commands)。

<a id="templates"></a>
## 用 `sheet.Template` 下载导入模板

`sheet.Template` 按 `sheet` 标签写出表头，传入的值写成示例行，填好的模板可以原样导入。`download` 写出附件响应，见[导出接口](#exporting)：

```go
type TemplateInput struct {
    Format sheet.Format `query:"format" enum:"csv,xlsx" default:"xlsx"`
}

// Template 处理 GET /articles/import/template。
func (a *Import) Template(ctx context.Context, in *TemplateInput) (*huma.StreamResponse, error) {
    return download("articles-template", in.Format, func(w *sheet.Writer) error {
        return sheet.Template(w, Changes{Title: "第一篇文章", Slug: "hello-world", Content: "正文"})
    }), nil
}
```

<a id="reading-rules"></a>
## 读取规则：XLSX、CSV 与编码

`sheet.NewReader` 读出第一个非空行作为表头，之后跳过空行，单元格的值去掉首尾空白。

- **XLSX** 读取第一个工作表，单元格的值是 Excel 中显示的文本：日期按单元格设置的格式显示，布尔值是 `TRUE`、`FALSE`。日期导入后格式不对时，把日期列设为字段能解析的格式，例如 `2026-11-16`。
- XLSX 整个读入内存，所以上传接口要[限制请求体大小](../http/requests.md#body-size)，用完调用 `Close`。
- **CSV** 去掉开头的 BOM，按 UTF-8 解码，失败时按 GB18030 解码（Excel 中文版另存的 CSV）。其他编码的文件先另存为 UTF-8。分隔符按表头一行在逗号、分号和制表符中选出现最多的一个。
- 数字字段的空单元格视为未填写，布尔字段接受 `true`、`false`、`1`、`0`（不区分大小写），切片字段的单元格按逗号（包括中文逗号）拆分。

<a id="export-action"></a>
## 编写导出动作：`sheet.Columns`

`sheet.Column` 描述导出的一列：`Header` 是表头，`Value` 取出单元格的值，`Key` 用于让用户选择导出哪些列。`export.go`：

```go
var exportColumns = sheet.Columns[Article]{
    {Key: "title", Header: "标题", Value: func(a Article) any { return a.Title }},
    {Key: "status", Header: "状态", Value: func(a Article) any { return a.Status }},
    {Key: "published_at", Header: "发布时间", Value: func(a Article) any { return a.PublishedAt }},
}

// Export 把文章列表导出为表格。
type Export struct {
    db *bun.DB
}

// query 返回 subject 能看到的文章，后台导出也用它统计行数。
func (a *Export) query(subject identity.Subject) *bun.SelectQuery {
    q := a.db.NewSelect().Model((*Article)(nil)).OrderExpr("created_at DESC")
    return scopeArticles(q, subject) // 按 subject 的权限加查询条件，与列表接口共用
}

// Handle 把 subject 能看到的文章按 cols 写入 w。
func (a *Export) Handle(ctx context.Context, subject identity.Subject, w sheet.RowWriter, cols sheet.Columns[Article]) error {
    rows, err := a.query(subject).Rows(ctx)
    if err != nil {
        return errors.Wrap(err, "export articles")
    }
    defer rows.Close()
    if err := w.Write(cols.Header()...); err != nil {
        return err
    }
    for rows.Next() {
        var item Article
        if err := a.db.ScanRow(ctx, rows, &item); err != nil {
            return errors.Wrap(err, "scan article")
        }
        if err := w.Write(cols.Row(item)...); err != nil {
            return err
        }
    }
    return errors.Wrap(rows.Err(), "export articles")
}
```

- `scopeArticles` 是模块中列表接口也用的范围条件，写法见[列表查询中的授权](../security/authorization.md#scoping-queries)。
- `Handle` 接收 `sheet.RowWriter`：同步导出传入 `*sheet.Writer`，[后台导出](#processing-exports)传入记录进度的 `*exports.Writer`，查询和写表的代码只有一份。
- `Rows` 用游标逐行读取，`Writer` 边读边写，内存占用与表的大小无关。

<a id="exporting"></a>
## 下载表格的导出接口

`GET /articles/export?format=csv&columns=title,published_at` 按 `columns` 的顺序导出这两列，`columns` 为空时导出全部列。`a` 是[导出动作](#export-action)的 `*Export`：

```go
type ExportInput struct {
    Format  sheet.Format `query:"format" enum:"csv,xlsx" default:"xlsx"`
    Columns []string     `query:"columns" doc:"要导出的列，默认全部"`
}

// HTTP 处理 GET /articles/export。
func (a *Export) HTTP(ctx context.Context, in *ExportInput) (*huma.StreamResponse, error) {
    subject, _ := identity.FromContext(ctx)
    cols, err := exportColumns.Select(in.Columns) // 不存在的列返回 sheet.ErrUnknownColumn
    if err != nil {
        return nil, err
    }
    return download("articles", in.Format, func(w *sheet.Writer) error {
        return a.Handle(ctx, subject, w, cols)
    }), nil
}

// download 以附件返回表格，文件名是 name 加上日期和扩展名。
func download(name string, format sheet.Format, write func(w *sheet.Writer) error) *huma.StreamResponse {
    return &huma.StreamResponse{Body: func(hctx huma.Context) {
        filename := name + "-" + time.Now().Format("20060102") + "." + string(format)
        hctx.SetHeader("Content-Type", format.ContentType())
        hctx.SetHeader("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
        w := sheet.NewWriter(hctx.BodyWriter(), format)
        if err := errors.CombineErrors(write(w), w.Close()); err != nil {
            slog.ErrorContext(hctx.Context(), "export failed", "error", err)
        }
    }}
}
```

> [!WARNING]
> 响应开始写出之后无法再返回错误状态码：导出中途出错时客户端收到不完整的文件，错误只记录到日志。能提前检查的参数（例如用 `Select` 检查列名）在返回 `StreamResponse` 之前检查。

前端下载这类接口的写法见[下载文件](../http/clients.md#downloading-files)。

<a id="cell-values"></a>
## 单元格的值与格式

`Value` 返回的值按类型写入单元格：

| 值 | 写出 |
| --- | --- |
| `nil`、nil 指针、零值 `time.Time` | 空单元格 |
| 整数、浮点数、`bool` | XLSX 中保持数值和布尔类型，可以求和、筛选 |
| `time.Time` | 按它自己的时区显示为 `2006-01-02 15:04:05`，XLSX 中是日期时间单元格 |
| `fmt.Stringer`，例如 `uuid.UUID` | `String()` 的结果 |
| 切片 | 各项以逗号连接 |
| 指针 | 指向的值 |

- 数据库读出的时间通常是 UTC，要显示用户的当地时间时在 `Value` 中转换，例如 `a.PublishedAt.In(loc)`。
- 枚举值要翻译成文字时在 `Value` 中用 `i18n.T`，此时把 `exportColumns` 改为接收 `ctx` 的函数。
- CSV 以 UTF-8 BOM 开头，Excel 打开时中文正常。以 `=`、`+`、`-`、`@` 开头的非数字文本前面加一个 `'`，防止 Excel 当作公式执行；`+86 138…` 这样的电话号码在 CSV 中因此带上 `'`。XLSX 中的文本原样写出，表头加粗并冻结。

<a id="background-imports"></a>
## 后台导入大文件：`sheet/imports`

几千行以上的表格放到[队列](../background/queue.md)中逐行导入：上传后马上返回导入的 ID，前端用它读取进度；每 100 行保存一次进度，重试时从断点继续；没有导入的行保存下来，可以下载、改正后重新导入；结束时通知发起人。一次导入是一个[后台操作](operations.md)。

装配：

1. 把 `operation.Migrations()`、`imports.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，同时需要队列和[文件存储](../libraries/filesystem.md)的迁移。`imports` 表记录上传的文件和处理到的行号，`import_failures` 表记录没有导入的行。
2. 在入口创建 `importer := imports.New(db, ops, files, languages)`：`ops` 是 `operation.New(db)`，`files` 是 `*filesystem.Service`，`languages` 是应用的[语言目录](../general/localization.md)，用于按发起请求的语言翻译失败行的提示。

导入的记录和失败的行由[后台操作的清理](operations.md#cleanup)一起删除。

<a id="starting-imports"></a>
## 登记后台导入：`importer.Start`

前端先用[上传接口](../libraries/filesystem.md)上传表格，再把文件 ID 交给导入接口。`queue_import.go`：

```go
// ImportJob 是后台导入文章的任务，payload 是导入的 ID。
var ImportJob = queue.Topic[uuid.UUID]("articles.import")

// QueueImport 在后台导入文章，rows 是同步导入的 Import。
type QueueImport struct {
    db       *bun.DB
    hub      *broadcast.Hub
    importer *imports.Service
    rows     *Import
}

// Handle 登记 subject 上传的文件 fileID 的导入，并发布处理它的任务。
func (a *QueueImport) Handle(ctx context.Context, subject identity.Subject, fileID uuid.UUID) (*imports.Import, error) {
    var imp *imports.Import
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        var err error
        if imp, err = a.importer.Start(ctx, tx, subject, "articles.import", fileID); err != nil {
            return err
        }
        return ImportJob.Publish(ctx, tx, imp.ID)
    })
    return imp, err
}

// ImportProgress 是返回给前端的导入，ErrorMessage 是失败原因的译文。
type ImportProgress struct {
    *imports.Import
    ErrorMessage string `json:"error_message,omitempty"`
}
```

- `Start` [确认上传的文件](../libraries/filesystem.md#confirming-files)，文件不是 `.csv` 或 `.xlsx` 时返回 `sheet.ErrFormat`；它记下请求的语言，返回 `pending` 状态的导入。
- 导入记录和任务在同一个事务中写入，回滚时一起撤销。
- `HTTP` 方法（`POST /articles/imports`，请求体 `{"file_id": "…"}`）调用 `Handle`，返回 `ImportProgress`，`DefaultStatus` 设为 `http.StatusAccepted`。

<a id="processing-imports"></a>
## 处理后台导入：`imports.Process`

worker 注册任务，见[启动 worker](../background/queue.md#running-workers)：

```go
article.ImportJob.Handle(w, article.NewQueueImport(a.db, a.hub, a.importer, article.NewImport(article.NewCreate(a.db))).Job)
```

```go
// Job 处理后台导入的任务，每一行交给同步导入的 row。
func (a *QueueImport) Job(ctx context.Context, id uuid.UUID) error {
    return imports.Process(ctx, a.importer, id, func(ctx context.Context, imp *imports.Import, rec *sheet.Record[Changes]) error {
        return a.rows.row(ctx, imp.Owner(), rec)
    }, a.saved)
}
```

`Process` 第一次执行时检查表头、统计行数，然后逐行解码，校验通过的行交给回调。每一行的结果：

| 情况 | 结果 |
| --- | --- |
| 校验失败，或者回调用 `SetError` 标出错误 | 记为没有导入的行 |
| 回调返回带错误码的业务错误 | 记为没有导入的行，提示是错误码的译文 |
| 回调返回其他错误，例如数据库不可用 | 保存进度后返回错误，队列重试时从这一行继续 |

- 缺少必需的列、文件损坏等整个文件的问题使导入变为 `failed`，`Error` 是错误码；最后一次重试仍然出错时同样 `failed`，`Error` 是 `operation.failed`。导入结束时上传的文件被删除。
- `imp.Owner()` 只有 ID、类型和租户：`Create` 需要权限时按 ID 重新加载；多租户应用中它已带着租户，见[队列任务](../security/tenancy.md#tenant-jobs)。
- `a.saved` 在每次保存进度的事务中调用（变为 `running`、读出表头、每 100 行、结束），写法与[后台操作的 `saved`](operations.md#progress) 相同：提交后广播，结束时发通知。

> [!WARNING]
> worker 在两次保存之间崩溃时，这些行在重试时再次交给回调，所以回调必须能重复执行。示例中的 `row` 遇到已被占用的 slug 记为失败，每篇文章只创建一次；没有唯一字段的数据先按业务字段查找，存在时更新。

<a id="progress-and-failures"></a>
## 导入进度与下载失败的行

进度接口 `GET /articles/imports/{id}/progress` 的写法与[后台操作的进度接口](operations.md#progress)相同，把 `ops.Get` 换成 `a.importer.Get`、推送 `ImportProgress`，用 `rest.RegisterSSE(api, op, map[string]any{"progress": ImportProgress{}}, queueImport.Progress)` 注册。事件中除了操作的字段，还有 `file_id` 和 `filename`：

```text
event: progress
data: {"id":"0193e7a2-…","kind":"articles.import","status":"running","total":52000,"processed":18300,"failed":12,"file_id":"0193e7a1-…","filename":"articles.xlsx",…}
```

`processed` 包括没有导入的 `failed` 行。下载没有导入的行用下面的接口，`ImportFailuresInput` 有路径参数 `id` 和查询参数 `format`，`download` 见[导出接口](#exporting)：

```go
// Failures 处理 GET /articles/imports/{id}/failures。
func (a *QueueImport) Failures(ctx context.Context, in *ImportFailuresInput) (*huma.StreamResponse, error) {
    subject, _ := identity.FromContext(ctx)
    imp, err := a.importer.Get(ctx, subject, in.ID) // 只有发起人能读到，其他人 404
    if err != nil {
        return nil, err
    }
    return download("articles-failures", in.Format, func(w *sheet.Writer) error {
        return a.importer.WriteFailures(ctx, imp, w)
    }), nil
}
```

`WriteFailures` 写出原来的表头加一列“错误”，每一行是原始单元格和这一行的错误，例如 `标题: 不能为空`。用户在这个文件中改正后直接重新导入，多出的错误列被忽略。页面上直接显示失败的行时用 `a.importer.Failures(ctx, imp, limit)`。

<a id="import-button"></a>
## 页面中的导入按钮：`ImportButton`

`@go-tenon/ui` 的 `ImportButton` 打开对话框，依次上传文件、发起导入、显示进度，结束后显示成功和失败的行数，可以下载失败的行；进度同时出现在顶栏的任务中心。`api` 是生成的客户端：

```tsx
<ImportButton
  title="导入文章"
  source={{
    upload: async (file) => (await api.file.upload({ file })).id, // 返回文件 ID
    start: (fileId) => api.article.importStart({ file_id: fileId }),
    progress: (id) => (o) => api.article.importProgress(id, o),
    failures: async (id) => { const { blob, filename } = await api.article.importFailures(id); saveBlob(blob, filename) },
  }}
  template={downloadTemplate} // 下载导入模板
  onCompleted={() => list.refetch()} // 导入完成时调用一次
/>
```

`failures` 省略时不提供下载。大文件的 `upload` 改用[客户端直传](../libraries/filesystem.md)。其他选项见[后台功能](../frontend/features.md#transfer)。

<a id="background-exports"></a>
## 后台导出大量数据：`sheet/exports`

几十万行的导出放到队列中：任务把表格写到本地临时文件，完成后上传到[文件存储](../libraries/filesystem.md)，前端看到进度，结束后下载。一次导出是一个[后台操作](operations.md)。

装配：把 `exports.Migrations()` 和 `operation.Migrations()` 一起加入[发布命令](../database/migrations.md#publishing-framework-migrations)，在入口创建 `exporter := exports.New(db, ops, files)`，`ops` 是 `operation.New(db)`，`files` 是 `*filesystem.Service`。导出记录由[后台操作的清理](operations.md#cleanup)删除。

<a id="starting-exports"></a>
## 登记后台导出：`exporter.Start`

`queue_export.go`，查询和写表由[导出动作](#export-action)的 `Export` 完成：

```go
// ExportRequest 是后台导出的任务：哪一次导出、谁在导出、导出哪些列。
type ExportRequest struct {
    ID      uuid.UUID        `json:"id"`
    Subject identity.Subject `json:"subject"`
    Columns []string         `json:"columns"`
}

var ExportJob = queue.Topic[ExportRequest]("articles.export")

// QueueExport 在后台导出文章。
type QueueExport struct {
    db       *bun.DB
    hub      *broadcast.Hub
    exporter *exports.Service
    export   *Export
}

// Handle 检查要导出的列，登记导出并发布任务。
func (a *QueueExport) Handle(ctx context.Context, subject identity.Subject, format sheet.Format, columns []string) (*exports.Export, error) {
    if _, err := exportColumns.Select(columns); err != nil {
        return nil, err
    }
    var exp *exports.Export
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        var err error
        if exp, err = a.exporter.Start(ctx, tx, subject, "articles.export", format); err != nil {
            return err
        }
        return ExportJob.Publish(ctx, tx, ExportRequest{ID: exp.ID, Subject: subject, Columns: columns})
    })
    return exp, err
}
```

- 登记之前检查列名，不存在的列直接返回 `sheet.ErrUnknownColumn`。
- payload 带着发起人：任务按它的权限查询，导出的是用户提交请求时能看到的数据。`exp.Owner()` 只有 ID、类型和租户，没有权限。
- `HTTP` 方法（`POST /articles/exports`）接收[同步导出](#exporting)的 `ExportInput`，调用 `Handle`，返回带 `error_message` 的 `ExportProgress`（嵌入 `*exports.Export`），`DefaultStatus` 设为 `http.StatusAccepted`。

<a id="processing-exports"></a>
## 处理后台导出：`exporter.Process`

worker 注册 `article.ExportJob.Handle(w, article.NewQueueExport(a.db, a.hub, a.exporter, article.NewExport(a.db)).Job)`：

```go
// Job 统计行数，再把文章写入记录进度的 Writer。
func (a *QueueExport) Job(ctx context.Context, req ExportRequest) error {
    return a.exporter.Process(ctx, req.ID, func(ctx context.Context, exp *exports.Export, w *exports.Writer) error {
        cols, err := exportColumns.Select(req.Columns)
        if err != nil {
            return err
        }
        total, err := a.export.query(req.Subject).Count(ctx)
        if err != nil {
            return errors.Wrap(err, "count articles")
        }
        if err := w.SetTotal(int(total)); err != nil {
            return err
        }
        return a.export.Handle(ctx, req.Subject, w, cols)
    }, a.saved)
}
```

- `exports.Writer` 的第一行是表头，之后每写一行计数一次，每秒最多保存一次进度；`SetTotal` 设置总行数，省略时 `total` 为 0，前端显示“已导出 N 行”。
- 写完后文件以 temporary 状态上传，导出变为 `completed`，`URL`（文件的[公开地址](../libraries/filesystem.md#accessing-files)）和完成状态一起提交。`ExpiresAt` 之后（文件存储的 `temp_ttl`，默认 24 小时）文件被回收，下载地址失效。
- 文件名是 kind 中的点换成连字符再加日期，例如 `articles-export-20261111.xlsx`。
- 回调返回带错误码的业务错误时导出变为 `failed`；其他错误交给队列重试，重试时从头导出，进度清零；最后一次重试仍然出错时 `failed`，`Error` 是 `operation.failed`。
- `a.saved` 的写法与[后台操作的 `saved`](operations.md#progress) 相同，结束时的通知可以带上 `exp.URL`。

<a id="export-progress"></a>
## 导出进度与下载地址

进度接口 `GET /articles/exports/{id}/progress` 的写法与[后台操作的进度接口](operations.md#progress)相同，把 `ops.Get` 换成 `a.exporter.Get`、推送 `ExportProgress`。导出在前端连上之前已经结束时，第一个事件就是最终结果。完成时的事件带着下载地址：

```text
event: progress
data: {"id":"0193e7a2-…","kind":"articles.export","status":"completed","total":320000,"processed":320000,"failed":0,"format":"xlsx","filename":"articles-export-20261111.xlsx","url":"/storage/2026/11/11/0193e7a3-….xlsx","expires_at":"2026-11-12T08:03:10Z",…}
```

浏览器直接打开 `url` 时保存的文件名是对象键。`<a href={url} download={filename}>` 用 `filename` 保存，`download` 属性只在与页面同源时生效。下载链接打不开时检查文件存储的公开地址能否访问。

<a id="export-button"></a>
## 页面中的导出按钮：`ExportButton`

`@go-tenon/ui` 的 `ExportButton` 发起导出，对话框显示进度，完成后显示“下载”；进度同时出现在顶栏的任务中心。`columns` 是用户选择的列：

```tsx
<ExportButton
  title="导出文章"
  source={{
    start: () => api.article.exportStart({ format: "xlsx", columns }),
    progress: (id) => (o) => api.article.exportProgress(id, o),
  }}
/>
```

下载地址默认取完成时进度事件中的 `url` 和 `filename`；进度的格式不同时，用 `source.file` 从最后一次进度中取出下载地址，见[后台功能](../frontend/features.md#transfer)。
