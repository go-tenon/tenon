# 后台导入与导出

几千行以上的表格不适合在一次请求中处理：请求会超时，失败了也只能从头再来。`sheet/imports` 在队列任务中逐行处理上传的表格，保存进度，worker 重启后从断点继续，失败的行可以下载；`sheet/exports` 在任务中写表格，完成后上传到[文件存储](../libraries/filesystem.md)。两者的状态和进度都是一个[后台操作](operations.md)。同步的导入导出和列的写法见[导入与导出](import-export.md)。

- [后台导入](#background-imports)
    - [登记导入](#starting-imports)
    - [处理导入](#processing-imports)
    - [进度与失败的行](#progress-and-failures)
    - [清理](#cleanup)
- [后台导出](#background-exports)
    - [登记导出](#starting-exports)
    - [处理导出](#processing-exports)
    - [导出进度](#export-progress)
- [API 参考](#api-reference)

<a id="background-imports"></a>
## 后台导入

文件有几万行时，处理放到后台，用户可以先去做别的事。`sheet/imports` 包把导入放到[队列](../libraries/queue.md)中：用户上传文件后马上得到一个导入 ID，前端用它查询进度；任务每处理 100 行保存一次进度，重试时从中断的地方继续；没有导入的行保存下来，用户可以下载、改正后重新导入；结束时在同一个事务中通知用户。

一次导入是一个[后台操作](operations.md)：状态和进度记在 `operations` 表中，`imports` 表记录上传的文件和处理到的行号，`import_failures` 表记录没有导入的行。和文件、队列的迁移一起[发布](../database/migrations.md#publishing-framework-migrations)，`operation` 排在 `imports` 之前：

```go
migrate.Source{Name: "operation", FS: operation.Migrations()},
migrate.Source{Name: "imports", FS: imports.Migrations()},
```

`imports.New` 在[依赖装配](../concepts/dependencies.md#assembling-the-application)的 `open` 中创建，`ops` 是 `operation.New(db)`，`files` 是[文件存储](../libraries/filesystem.md)的 `*filesystem.Service`，`languages` 是应用的[语言目录](../general/localization.md)：

```go
importer := imports.New(db, ops, files, languages)
```

<a id="starting-imports"></a>
### 登记导入

前端先用[文件上传](../libraries/filesystem.md)的接口上传表格，再把文件 ID 交给导入接口。`QueueImport` 登记导入，并在同一个事务中发布任务；处理每一行时复用 `Import` 的 `row`。例如 `queue_import.go`：

```go
// ImportJob 是后台导入文章的任务，payload 是导入的 ID。
var ImportJob = queue.Topic[uuid.UUID]("articles.import")

// QueueImport 在后台导入文章。
type QueueImport struct {
    db       *bun.DB
    hub      *broadcast.Hub
    importer *imports.Service
    rows     *Import
}

func NewQueueImport(db *bun.DB, hub *broadcast.Hub, importer *imports.Service, rows *Import) *QueueImport {
    return &QueueImport{db: db, hub: hub, importer: importer, rows: rows}
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

type QueueImportInput struct {
    Body struct {
        FileID uuid.UUID `json:"file_id"`
    }
}

// ImportProgress 是导入的进度，ErrorMessage 是导入失败的原因。
type ImportProgress struct {
    *imports.Import
    ErrorMessage string `json:"error_message,omitempty"`
}

func progressOf(ctx context.Context, imp *imports.Import) ImportProgress {
    return ImportProgress{Import: imp, ErrorMessage: imp.ErrorMessage(ctx)}
}

type ImportProgressOutput struct {
    Body ImportProgress
}

// HTTP 处理 POST /articles/imports。
func (a *QueueImport) HTTP(ctx context.Context, in *QueueImportInput) (*ImportProgressOutput, error) {
    subject, _ := identity.FromContext(ctx)
    imp, err := a.Handle(ctx, subject, in.Body.FileID)
    if err != nil {
        return nil, err
    }
    return &ImportProgressOutput{Body: progressOf(ctx, imp)}, nil
}

// Job 处理后台导入的任务。
func (a *QueueImport) Job(ctx context.Context, id uuid.UUID) error {
    return imports.Process(ctx, a.importer, id, func(ctx context.Context, imp *imports.Import, rec *sheet.Record[Changes]) error {
        return a.rows.row(ctx, imp.Owner(), rec)
    }, a.saved)
}

// saved 在每次保存进度的事务中调用：提交后广播进度；导入结束时在同一个事务中通知发起导入的人。
func (a *QueueImport) saved(ctx context.Context, tx database.Tx, imp *imports.Import) error {
    tx.AfterCommit(func(ctx context.Context) error {
        return a.hub.Publish(ctx, operationTopic(imp.ID), nil)
    })
    if !imp.Finished() {
        return nil
    }
    _, err := notification.Send(ctx, tx, notification.Notification{
        Event: "articles.imported",
        Data: map[string]any{
            "import_id": imp.ID, "status": imp.Status,
            "created": imp.Processed - imp.Failed, "failed": imp.Failed,
        },
    }, notification.RecipientOf(imp.Owner()))
    return err
}

type ImportIDInput struct {
    ID uuid.UUID `path:"id"`
}

// Progress 处理 GET /articles/imports/{id}/progress：连接后先推送当前进度，之后每次保存进度时推送一次，
// 导入结束后关闭连接。
func (a *QueueImport) Progress(ctx context.Context, in *ImportIDInput) (*huma.StreamResponse, error) {
    subject, _ := identity.FromContext(ctx)
    if _, err := a.importer.Get(ctx, subject, in.ID); err != nil {
        return nil, err // 还没有开始推送，客户端照常收到 404
    }
    return rest.SSE(func(s *httpx.Stream) error {
        events := a.hub.Subscribe(operationTopic(in.ID)) // 先订阅，再查询，中间保存的进度也能收到
        defer events.Close()
        for {
            imp, err := a.importer.Get(s.Context(), subject, in.ID)
            if err != nil {
                return err
            }
            if err := s.SendJSON(httpx.Event{Name: "progress"}, progressOf(s.Context(), imp)); err != nil {
                return nil // 客户端已经离开
            }
            if imp.Finished() {
                return nil
            }
            select {
            case <-s.Done():
                return nil
            case _, ok := <-events.C():
                if !ok {
                    return nil // 订阅已结束，客户端重连后先收到当前进度
                }
            }
        }
    }), nil
}

type ImportFailuresInput struct {
    ID     uuid.UUID    `path:"id"`
    Format sheet.Format `query:"format" enum:"csv,xlsx" default:"xlsx"`
}

// Failures 处理 GET /articles/imports/{id}/failures，下载没有导入的行。
func (a *QueueImport) Failures(ctx context.Context, in *ImportFailuresInput) (*huma.StreamResponse, error) {
    subject, _ := identity.FromContext(ctx)
    imp, err := a.importer.Get(ctx, subject, in.ID)
    if err != nil {
        return nil, err
    }
    return download("articles-failures", in.Format, func(w *sheet.Writer) error {
        return a.importer.WriteFailures(ctx, imp, w)
    }), nil
}
```

`Start` 确认上传的文件（见[确认文件](../libraries/filesystem.md#confirming-files)），检查它是 `.csv` 或 `.xlsx`，记下请求的语言，返回 `pending` 状态的导入。任务和导入记录在同一个事务中写入，事务回滚时一起撤销。

<a id="processing-imports"></a>
### 处理导入

worker 注册任务，见[启动 worker](../libraries/queue.md#running-workers)：

```go
article.ImportJob.Handle(w, article.NewQueueImport(a.db, a.hub, a.importer, article.NewImport(article.NewCreate(a.db))).Job)
```

`imports.Process` 用[后台操作](operations.md#running)的 `Run` 执行导入，一行是一个单位：导入变为 `running`，第一次执行时检查表头、统计行数；然后逐行解码，验证通过的行交给回调。回调与同步导入共用 `row`，发起人由 `imp.Owner()` 得到，它只有 ID、类型和租户；`Create` 需要检查权限时，按 ID 重新加载权限；多租户应用中 `Owner` 已经带着租户，见[队列任务](../security/tenancy.md#tenant-jobs)。

每一行的结果有三种：

| 情况 | 结果 |
| --- | --- |
| 验证失败，或者回调用 `SetError` 标出了错误 | 记为没有导入的行 |
| 回调返回带错误码的业务错误 | 同样记为没有导入的行，提示是错误码的译文 |
| 回调返回其他错误，例如数据库不可用 | 保存进度后返回错误，队列稍后重试，从这一行继续 |

验证提示和错误的译文使用登记导入时请求的语言。缺少必需的列、文件损坏等整个文件的问题，导入直接变为 `failed`，`Error` 是错误码；队列的最后一次重试仍然出错时，导入同样变为 `failed`，`Error` 是 `operation.failed`。导入结束时上传的文件被删除，由文件存储的 `Cleanup` 回收，失败的行另有保存。

`Process` 每次保存进度时都在同一个事务中调用 `saved`：导入变为 `running` 时一次，读出表头时一次，之后每 100 行一次，结束时再一次。`saved` 用 [AfterCommit](../database/quick-start.md#after-commit) 在提交之后向 `operationTopic`（见[后台操作](operations.md#starting)）发布广播，提醒正在查看进度的连接，见[进度与失败的行](#progress-and-failures)。导入变为 `completed` 或 `failed` 时，`saved` 在这个事务中发送通知，通知和状态一起提交，恰好发送一次。`notification.Send` 的用法见[通知](notifications.md)。

> [!WARNING]
> 进度每 100 行保存一次。worker 在两次保存之间崩溃时，这些行会在重试时再次交给回调，所以回调要能重复执行。示例中的 `row` 遇到已被占用的 slug 会把这一行记为失败，每篇文章只创建一次；没有唯一字段的数据，可以先按业务字段查找，存在时更新。

<a id="progress-and-failures"></a>
### 进度与失败的行

在 `routes.go` 中注册路由。`Progress` 用 [`rest.RegisterSSE`](../responses/sse.md) 注册，事件表描述推送的数据：

```go
queueImport := NewQueueImport(db, hub, importer, NewImport(NewCreate(db)))

start := huma.Operation{
    OperationID:   "article-import-start",
    Method:        http.MethodPost,
    Path:          "/articles/imports",
    Summary:       "在后台导入文章",
    DefaultStatus: http.StatusAccepted,
}
requireArticles(&start)
rest.Register(api, start, queueImport.HTTP)

progress := huma.Operation{
    OperationID: "article-import-progress",
    Method:      http.MethodGet,
    Path:        "/articles/imports/{id}/progress",
    Summary:     "导入进度",
}
requireArticles(&progress)
rest.RegisterSSE(api, progress, map[string]any{"progress": ImportProgress{}}, queueImport.Progress)
```

`Failures` 的注册方式与 `start` 相同。连接 `GET /articles/imports/{id}/progress` 后，每次保存进度都会收到一个 `progress` 事件：

```text
event: progress
data: {"id":"0193e7a2-7c1e-7b4a-9d2f-3c5a1e8b6f10","kind":"articles.import","status":"running","total":52000,"processed":18300,"failed":12,"created_at":"2026-11-11T08:00:00Z","updated_at":"2026-11-11T08:00:41Z","file_id":"0193e7a1-55aa-7c2b-8e3f-9a0b1c2d3e4f","filename":"articles.xlsx"}
```

前端用 `processed / total` 显示进度条，`processed` 包括没有导入的 `failed` 行；收到 `completed` 或 `failed` 后连接关闭，再显示结果和下载失败行的链接。

进度是状态：广播只提醒连接重新读取，连接（包括断线重连）时先推送一次当前进度，所以页面总是显示最新的进度，与[通知角标](notifications.md#realtime)的做法相同。worker 和 SSE 连接在不同的实例上时，广播由 Hub 的后端送达，见[广播](../libraries/broadcasting.md)。每次读取都经过 `Get`，只有发起导入的人能看到进度，其他人收到 `operation.ErrNotFound`。

使用 Bearer 令牌的前端用 `fetch` 读取事件流（浏览器的 `EventSource` 由浏览器决定请求头），见[客户端代码的事件流](../responses/clients.md#event-streams)。

`WriteFailures` 写出的表格是原来的表头加上一列“错误”，每一行是原始单元格和这一行的错误，例如 `标题: 不能为空`。用户在这个文件里改正后可以直接重新导入，多出的错误列自动略过。需要在页面上直接显示失败的行时，用 `Failures` 读取前几行。

<a id="cleanup"></a>
### 清理

导入记录和失败的行由[清理后台操作](operations.md#cleanup)一起删除。

<a id="background-exports"></a>
## 后台导出

导出几十万行要几分钟，同样放到队列中。`sheet/exports` 包记录导出的进度，任务把表格写到本地临时文件，完成后上传到[文件存储](../libraries/filesystem.md)，用户在页面上看到进度，结束后直接下载。

导出同样是一个[后台操作](operations.md)，`exports` 表记录格式和生成的文件。与[后台导入](#background-imports)一样发布迁移、创建服务：

```go
migrate.Source{Name: "exports", FS: exports.Migrations()},
```

```go
exporter := exports.New(db, ops, files)
```

<a id="starting-exports"></a>
### 登记导出

例如 `queue_export.go`，查询和写表仍由[同步导出](import-export.md#export-action)的 `Export` 完成：

```go
// ExportRequest 是后台导出的任务：哪一次导出、谁在导出、导出哪些列。
type ExportRequest struct {
    ID      uuid.UUID        `json:"id"`
    Subject identity.Subject `json:"subject"`
    Columns []string         `json:"columns"`
}

// ExportJob 是后台导出文章的任务。
var ExportJob = queue.Topic[ExportRequest]("articles.export")

// QueueExport 在后台导出文章。
type QueueExport struct {
    db       *bun.DB
    hub      *broadcast.Hub
    exporter *exports.Service
    export   *Export
}

func NewQueueExport(db *bun.DB, hub *broadcast.Hub, exporter *exports.Service, export *Export) *QueueExport {
    return &QueueExport{db: db, hub: hub, exporter: exporter, export: export}
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

// ExportProgress 是导出的进度，ErrorMessage 是导出失败的原因。
type ExportProgress struct {
    *exports.Export
    ErrorMessage string `json:"error_message,omitempty"`
}

func exportProgressOf(ctx context.Context, exp *exports.Export) ExportProgress {
    return ExportProgress{Export: exp, ErrorMessage: exp.ErrorMessage(ctx)}
}

type ExportProgressOutput struct {
    Body ExportProgress
}

// HTTP 处理 POST /articles/exports，返回导出的 ID，前端用它查看进度。
func (a *QueueExport) HTTP(ctx context.Context, in *ExportInput) (*ExportProgressOutput, error) {
    subject, _ := identity.FromContext(ctx)
    exp, err := a.Handle(ctx, subject, in.Format, in.Columns)
    if err != nil {
        return nil, err
    }
    return &ExportProgressOutput{Body: exportProgressOf(ctx, exp)}, nil
}

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

// saved 在每次保存进度的事务中调用：提交后广播进度；导出结束时在同一个事务中通知导出的人。
func (a *QueueExport) saved(ctx context.Context, tx database.Tx, exp *exports.Export) error {
    tx.AfterCommit(func(ctx context.Context) error {
        return a.hub.Publish(ctx, operationTopic(exp.ID), nil)
    })
    if !exp.Finished() {
        return nil
    }
    _, err := notification.Send(ctx, tx, notification.Notification{
        Event: "articles.exported",
        Data: map[string]any{
            "export_id": exp.ID, "status": exp.Status,
            "filename": exp.Filename, "url": exp.URL, "expires_at": exp.ExpiresAt,
        },
    }, notification.RecipientOf(exp.Owner()))
    return err
}

type ExportIDInput struct {
    ID uuid.UUID `path:"id"`
}

// Progress 处理 GET /articles/exports/{id}/progress：连接后先推送当前进度，之后每次保存进度时推送一次，
// 导出结束后关闭连接。
func (a *QueueExport) Progress(ctx context.Context, in *ExportIDInput) (*huma.StreamResponse, error) {
    subject, _ := identity.FromContext(ctx)
    if _, err := a.exporter.Get(ctx, subject, in.ID); err != nil {
        return nil, err // 还没有开始推送，客户端照常收到 404
    }
    return rest.SSE(func(s *httpx.Stream) error {
        events := a.hub.Subscribe(operationTopic(in.ID)) // 先订阅，再查询
        defer events.Close()
        for {
            exp, err := a.exporter.Get(s.Context(), subject, in.ID)
            if err != nil {
                return err
            }
            if err := s.SendJSON(httpx.Event{Name: "progress"}, exportProgressOf(s.Context(), exp)); err != nil {
                return nil // 客户端已经离开
            }
            if exp.Finished() {
                return nil
            }
            select {
            case <-s.Done():
                return nil
            case _, ok := <-events.C():
                if !ok {
                    return nil // 订阅已结束，客户端重连后先收到当前进度
                }
            }
        }
    }), nil
}
```

`Handle` 在登记之前检查列名，不存在的列直接返回 `sheet.ErrUnknownColumn`。导出记录和任务在同一个事务中写入。

任务的 payload 带着发起导出的主体：`Export` 按它的权限限定[查询范围](../security/authorization.md#scoping-queries)，导出的是用户提交请求时能看到的数据，多租户应用中主体也带着租户。`exp.Owner()` 只有 ID、类型和租户，权限以 payload 中的主体为准。

<a id="processing-exports"></a>
### 处理导出

worker 注册任务：

```go
article.ExportJob.Handle(w, article.NewQueueExport(a.db, a.hub, a.exporter, article.NewExport(a.db)).Job)
```

`Process` 把导出变为 `running`，回调先用同一个 `query` 统计行数，用 `SetTotal` 告诉前端一共有多少行，再交给 `Export.Handle` 写出。`exports.Writer` 的第一行是表头，之后每写一行计数一次，每秒最多保存一次进度。

写完后文件以 temporary 状态上传，导出变为 `completed`，文件的地址和完成状态在同一个事务中保存，前端看到 `completed` 时一定能拿到地址。`URL` 是文件的[公开地址](../libraries/filesystem.md#accessing-files)，`ExpiresAt` 是它被回收的时间：超过 [`temp_ttl`](../libraries/filesystem.md#configuration)（默认 24 小时）后文件由文件存储的 `Cleanup` 删除，下载地址随之失效。文件名是 kind 中的点换成连字符再加日期，例如 `articles-export-20261111.xlsx`。

每次保存进度都在同一个事务中调用 `saved`，与[后台导入](#processing-imports)相同：提交后广播，结束时发送通知。回调返回带错误码的业务错误时，导出直接变为 `failed`；其他错误交给队列重试，重试时从头重新导出，进度清零；最后一次重试仍然出错时，导出同样变为 `failed`，`Error` 是 `operation.failed`。

对象键是随机的 UUID 加扩展名，浏览器直接打开公开地址时保存的文件名也是它。前端可以用 `<a href={url} download={filename}>` 指定保存的文件名，这个属性只在与页面同源时生效，例如本地磁盘挂在应用自己的路径下。

<a id="export-progress"></a>
### 导出进度

在 `routes.go` 中注册路由：

```go
queueExport := NewQueueExport(db, hub, exporter, NewExport(db))

exportLater := huma.Operation{
    OperationID:   "article-export-start",
    Method:        http.MethodPost,
    Path:          "/articles/exports",
    Summary:       "在后台导出文章",
    DefaultStatus: http.StatusAccepted,
}
requireArticles(&exportLater)
rest.Register(api, exportLater, queueExport.HTTP)

exportProgress := huma.Operation{
    OperationID: "article-export-progress",
    Method:      http.MethodGet,
    Path:        "/articles/exports/{id}/progress",
    Summary:     "导出进度",
}
requireArticles(&exportProgress)
rest.RegisterSSE(api, exportProgress, map[string]any{"progress": ExportProgress{}}, queueExport.Progress)
```

推送的方式与[导入进度](#progress-and-failures)相同：连接时先推送当前进度，之后每次保存进度推送一次，结束后关闭连接。导出在前端连上之前就已经结束时，前端第一次就收到最终结果。导出完成时的事件带着下载地址：

```text
event: progress
data: {"id":"0193e7a2-8a01-7c3e-b1d4-5e6f7a8b9c0d","kind":"articles.export","status":"completed","total":320000,"processed":320000,"failed":0,"created_at":"2026-11-11T08:00:00Z","updated_at":"2026-11-11T08:03:10Z","completed_at":"2026-11-11T08:03:10Z","format":"xlsx","file_id":"0193e7a3-1b2c-7d4e-8f90-a1b2c3d4e5f6","filename":"articles-export-20261111.xlsx","url":"/storage/2026/11/11/0193e7a3-1b2c-7d4e-8f90-a1b2c3d4e5f6.xlsx","expires_at":"2026-11-12T08:03:10Z"}
```

`total` 为 0 时（回调没有调用 `SetTotal`），前端显示“已导出 N 行”即可。

导出记录随[后台操作的清理](operations.md#cleanup)一起删除，导出的文件已经由文件存储回收。

<a id="api-reference"></a>
## API 参考

### imports

包 `github.com/go-tenon/tenon/sheet/imports`。

```go
func New(db *bun.DB, ops *operation.Service, files *filesystem.Service, languages *goi18n.Bundle) *Service
func Process[T any](ctx context.Context, s *Service, id uuid.UUID,
    row func(ctx context.Context, imp *Import, rec *sheet.Record[T]) error,
    saved func(ctx context.Context, tx database.Tx, imp *Import) error,
) error
func Migrations() fs.FS
```

`Service` 登记导入、读取失败的行；`languages` 用于翻译失败行的错误提示。`Process` 在任务中逐行调用 `row`，每 100 行保存一次进度并调用 `saved`，见[处理导入](#processing-imports)。

### exports

包 `github.com/go-tenon/tenon/sheet/exports`。

```go
func New(db *bun.DB, ops *operation.Service, files *filesystem.Service) *Service
func Migrations() fs.FS
```

`Service` 登记导出、在任务中写表格并上传，见[处理导出](#processing-exports)。
