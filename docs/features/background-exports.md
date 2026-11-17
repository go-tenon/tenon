# 后台导出

导出几十万行要几分钟，在一次请求中做会超时，所以放到[队列](../background/queue.md)中。`sheet/exports` 包记录导出的进度，任务把表格写到本地临时文件，完成后上传到[文件存储](../libraries/filesystem.md)，用户在页面上看到进度，结束后直接下载。

导出是一个[后台操作](operations.md)，在“任务中心”中与导入和其他批量操作一起显示。同步的导出和列的写法见[导入与导出](import-export.md)。

阅读前需要了解：[导入与导出](import-export.md)；[后台操作](operations.md)；[文件存储](../libraries/filesystem.md)。

- [安装](#installing)
- [登记导出](#starting-exports)
- [处理导出](#processing-exports)
- [导出进度](#export-progress)
- [在页面中使用](#export-button)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="installing"></a>
## 安装

`exports` 表记录导出的格式和生成的文件。与[后台导入](background-imports.md#background-imports)一样发布迁移、创建服务：

```go
exports.Migrations(),
```

```go
exporter := exports.New(db, ops, files)
```

<a id="starting-exports"></a>
## 登记导出

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
## 处理导出

worker 注册任务：

```go
article.ExportJob.Handle(w, article.NewQueueExport(a.db, a.hub, a.exporter, article.NewExport(a.db)).Job)
```

`Process` 把导出变为 `running`，回调先用同一个 `query` 统计行数，用 `SetTotal` 告诉前端一共有多少行，再交给 `Export.Handle` 写出。`exports.Writer` 的第一行是表头，之后每写一行计数一次，每秒最多保存一次进度。

写完后文件以 temporary 状态上传，导出变为 `completed`，文件的地址和完成状态在同一个事务中保存，前端看到 `completed` 时一定能拿到地址。`URL` 是文件的[公开地址](../libraries/filesystem.md#accessing-files)，`ExpiresAt` 是它被回收的时间：超过 [`temp_ttl`](../libraries/filesystem.md#configuration)（默认 24 小时）后文件由文件存储的 `Cleanup` 删除，下载地址随之失效。文件名是 kind 中的点换成连字符再加日期，例如 `articles-export-20261111.xlsx`。

每次保存进度都在同一个事务中调用 `saved`，与[后台导入](background-imports.md#processing-imports)相同：提交后广播，结束时发送通知。回调返回带错误码的业务错误时，导出直接变为 `failed`；其他错误交给队列重试，重试时从头重新导出，进度清零；最后一次重试仍然出错时，导出同样变为 `failed`，`Error` 是 `operation.failed`。

对象键是随机的 UUID 加扩展名，浏览器直接打开公开地址时保存的文件名也是它。前端可以用 `<a href={url} download={filename}>` 指定保存的文件名，这个属性只在与页面同源时生效，例如本地磁盘挂在应用自己的路径下。

<a id="export-progress"></a>
## 导出进度

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

推送的方式与[导入进度](background-imports.md#progress-and-failures)相同：连接时先推送当前进度，之后每次保存进度推送一次，结束后关闭连接。导出在前端连上之前就已经结束时，前端第一次就收到最终结果。导出完成时的事件带着下载地址：

```text
event: progress
data: {"id":"0193e7a2-8a01-7c3e-b1d4-5e6f7a8b9c0d","kind":"articles.export","status":"completed","total":320000,"processed":320000,"failed":0,"created_at":"2026-11-11T08:00:00Z","updated_at":"2026-11-11T08:03:10Z","completed_at":"2026-11-11T08:03:10Z","format":"xlsx","file_id":"0193e7a3-1b2c-7d4e-8f90-a1b2c3d4e5f6","filename":"articles-export-20261111.xlsx","url":"/storage/2026/11/11/0193e7a3-1b2c-7d4e-8f90-a1b2c3d4e5f6.xlsx","expires_at":"2026-11-12T08:03:10Z"}
```

`total` 为 0 时（回调没有调用 `SetTotal`），前端显示“已导出 N 行”即可。

导出记录随[后台操作的清理](operations.md#cleanup)一起删除，导出的文件已经由文件存储回收。

<a id="export-button"></a>
## 在页面中使用

框架提供了现成的导出按钮：`@go-tenon/ui` 的 `ExportButton`，见[后台功能](../frontend/features.md#transfer)。点击按钮发起导出，对话框显示进度，完成后显示“下载”。进度同时出现在顶栏的[任务中心](../frontend/features.md#operations)。

```tsx
<ExportButton
  title="导出文章"
  source={{
    start: () => api.article.exportStart({ format: "xlsx", columns }),
    progress: (id) => (o) => api.article.exportProgress(id, o),
  }}
/>
```

下载地址默认取完成时进度事件中的 `url` 和 `filename`，`sheet/exports` 的导出就是这样推送的；进度的格式不同时，用 `file` 从最后一次进度中取出下载地址。

<a id="troubleshooting"></a>
## 常见问题

**导出一直停在 `pending`**

导出任务还没有被处理：检查 worker 是否在运行，导出任务是否注册了处理函数，见[处理导出](#processing-exports)。

**导出完成了，但下载链接打不开**

导出的文件保存在文件存储中，通过它的公开地址下载。检查文件存储的 `public_url` 是否可以访问，见[文件存储](../libraries/filesystem.md#accessing-files)。

**导出的数据比列表中看到的多**

任务的 payload 中带着发起人（`Subject`），处理时要按发起人的权限和列表相同的筛选条件查询，见[登记导出](#starting-exports)。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/sheet/exports`。

```go
func New(db *bun.DB, ops *operation.Service, files *filesystem.Service) *Service
func Migrations() fs.FS
```

`Service` 登记导出、在任务中写表格并上传，见[处理导出](background-exports.md#processing-exports)。

