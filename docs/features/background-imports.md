# 后台导入

几千行以上的表格不适合在一次请求中处理：请求会超时，失败了也只能从头再来。`sheet/imports` 在队列任务中逐行处理上传的表格，保存进度，worker 重启后从断点继续，失败的行可以下载。导入的状态和进度是一个[后台操作](operations.md)。同步的导入导出和列的写法见[导入与导出](import-export.md)，后台导出见[后台导出](background-exports.md)。

阅读前需要了解：[导入与导出](import-export.md)；[队列](../libraries/queue.md)；[后台操作](operations.md)。

- [后台导入](#background-imports)
    - [登记导入](#starting-imports)
    - [处理导入](#processing-imports)
    - [进度与失败的行](#progress-and-failures)
    - [清理](#cleanup)
    - [在页面中使用](#import-button)
- [常见问题](#troubleshooting)
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

<a id="import-button"></a>
### 在页面中使用

框架提供了现成的导入按钮：运营后台（antd）的 `ImportButton` 和 shadcn registry 的 `tenon-transfer`，逻辑都来自 `@go-tenon/shell` 的 `useImport`。点击按钮打开对话框，用户选择表格后，它依次上传文件、发起导入、显示进度，结束后显示成功和没有导入的行数，有没有导入的行时可以下载它们。进度同时出现在顶栏的[任务中心](../responses/app-shell-features.md#operations)，用户关闭对话框也能看到。

把上面几个接口交给它：

```tsx
<ImportButton
  title="导入文章"
  source={{
    upload: async (file) => (await api.file.upload({ file })).id,
    start: (fileId) => api.article.importStart({ file_id: fileId }),
    progress: (id) => (o) => api.article.importProgress(id, o),
    failures: async (id) => {
      const { blob, filename } = await api.article.importFailures(id)
      saveBlob(blob, filename || "failures.xlsx")
    },
  }}
  template={downloadTemplate}
  onCompleted={() => void queryClient.invalidateQueries({ queryKey: queries.article.key() })}
/>
```

- `upload` 上传表格，返回文件 ID，这里用的是[服务端上传接口](../libraries/file-uploads.md#rest-upload)；大文件可以改用[客户端直传](../libraries/direct-uploads.md)。
- `failures` 可以省略，省略时不提供下载。
- `template` 是“下载导入模板”的链接，见[下载模板](import-export.md#templates)。
- `onCompleted` 在导入完成时调用一次，通常让列表重新读取。

<a id="troubleshooting"></a>
## 常见问题

**导入停在某一行不动了**

任务可能失败后在等待重试。看失败任务表和 worker 的日志；修好问题后任务从上次保存的进度继续，见[处理导入](#processing-imports)。

**worker 重启后，有几行被导入了两次**

进度每 100 行保存一次，重试时从上次保存的位置继续，中间的行会再处理一次。导入每一行的处理要能重复执行，例如按唯一键判断是否已经存在。

**失败的行没法下载**

失败的行保存在导入的记录中，过了保留期会被清理，见[清理](#cleanup)。

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
