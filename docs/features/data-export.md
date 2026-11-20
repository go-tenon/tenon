# 导出个人数据

用户有权查阅、复制你保存的他的个人信息，个人信息保护法规定了这项权利。`privacy/export` 包在后台把一个用户的数据导出成一个 zip 压缩包：每个保存了他数据的地方写成一个 JSON 文件，完成后由用户本人下载。

导出用的是[注销账号](privacy.md)时登记的同一份清单：每一项 `privacy.Source` 除了 `Erase`，再加一个 `Export`。这样“删除了哪些数据”和“导出了哪些数据”总是同一份。导出是一个[后台操作](operations.md)，和其他导入导出一起出现在“任务中心”里。

阅读前需要了解：[注销账号](privacy.md)；[后台操作](operations.md)；[队列](../background/queue.md)；[文件存储](../libraries/filesystem.md)。

- [示例用法](#example-usage)
- [安装](#installation)
- [登记数据](#sources)
- [发起导出](#starting)
- [处理导出](#processing)
- [下载](#downloading)
- [压缩包的内容](#archive)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

给注销时登记的各项加上 `Export`，用同一个 `privacy.Service` 创建导出服务：

```go
deletions := privacy.New(db, privacy.Options{
    Type:  user.Type,
    Grace: 15 * 24 * time.Hour,
    Sources: []privacy.Source{
        {Name: "profile", Erase: user.Erase, Export: user.Export},
        {Name: "login", Erase: users.Forget, Export: users.Export},
        {Name: "notifications", Erase: notification.Erase, Export: notification.Export},
        {Name: "activity", Export: activity.Export}, // 操作日志不删除，但要导出
    },
})
personal := export.New(db, ops, files, deletions)

// 发起导出：登记，并在同一个事务中发布任务
exp, err := personal.Start(ctx, tx, sub)
err = ExportJob.Publish(ctx, tx, exp.ID)

// 队列任务：导出各项，打包上传
err = personal.Process(ctx, id, nil)

// 下载：只有本人能打开
body, exp, err := personal.Open(ctx, sub, id)
```

`ops` 是[后台操作](operations.md#installation)的服务，`files` 是[文件存储](../libraries/filesystem.md)的服务，`export` 是包 `github.com/go-tenon/tenon/privacy/export`。下面逐步介绍每一部分。

<a id="installation"></a>
## 安装

`privacy_exports` 表记录生成的压缩包，它引用 `operation` 的表，要和 `operation` 的迁移一起发布：

```go
migratecmd.Publish(
    operation.Migrations(),
    privacy.Migrations(),
    export.Migrations(),
)
```

<a id="sources"></a>
## 登记数据

`Source.Export` 返回这一项的数据，导出时编码为 JSON，写成压缩包中的 `<Name>.json`：

```go
Export func(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error)
```

框架中保存主体数据的包都提供了这个签名的函数：

| 包 | 登记 | 导出什么 |
| --- | --- | --- |
| [`login`](../security/login.md) | `users.Export`（`login.Realm`） | 登录标识（邮箱、手机号等，是否验证过）、是否设置了密码、是否启用了二次验证 |
| [`apitoken`](../security/api-tokens.md) | `apitoken.Export` | 登录过的设备和令牌：类型、名称、权限、IP、User-Agent、时间 |
| [`authz`](../security/authorization.md) | `roles.Export`（`authz.Store`） | 在各个租户中的角色 |
| [`notification`](notifications.md) | `notification.Export` | 所有租户中收到的通知 |
| [`push`](push.md) | `push.Export` | 登记的设备：平台、租户、时间 |
| [`activity`](activity.md) | `activity.Export` | 他作为操作人的操作日志 |
| [`ai/chat`](../ai/chat.md) | 包一层 `chat.ExportUser` | AI 对话的会话、消息、摘要和用户状态 |

密码哈希、TOTP 密钥、token 本身和推送设备的 token 都不导出：它们是凭据，不是用户需要看的信息。

应用自己的表返回给用户看的结构，字段用 `json` 标签命名，不要直接返回 Bun 模型，否则内部字段也会出现在文件里：

```go
// internal/user/export.go

// Export 返回用户的资料和收藏，用于导出个人数据。
func Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error) {
    u := new(User)
    if err := db.NewSelect().Model(u).Where("id = ?", ref.ID).Scan(ctx); err != nil {
        return nil, err
    }
    var favorites []string
    if err := db.NewSelect().Model((*Favorite)(nil)).Column("article_id").
        Where("user_id = ?", ref.ID).Scan(ctx, &favorites); err != nil {
        return nil, err
    }
    return map[string]any{
        "nickname":   u.Nickname,
        "avatar":     u.AvatarURL,
        "created_at": u.CreatedAt,
        "favorites":  favorites,
    }, nil
}
```

全部 `Export` 在同一个只读的事务中依次执行，`db` 就是这个事务，所以各个文件是同一时刻的数据。`Export` 返回 `nil` 时不写这个文件。只登记了 `Export`、没有 `Erase` 的项（例如操作日志）只导出、注销时不删除。

AI 对话和注销时一样包一层：

```go
{Name: "ai_sessions",
    Erase: func(ctx context.Context, db bun.IDB, ref identity.Ref) error {
        return chat.EraseUser(ctx, db, ref.ID)
    },
    Export: func(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error) {
        return chat.ExportUser(ctx, db, ref.ID)
    },
},
```

<a id="starting"></a>
## 发起导出

导出要几秒到几分钟，放在[队列](../background/queue.md)中执行。发起导出的接口在一个事务中登记导出、发布任务：

```go
// ExportJob 是导出个人数据的任务，内容是导出的 ID。
var ExportJob = queue.Topic[uuid.UUID]("account.export")

// POST /account/exports
func (a *ExportAccount) Handle(ctx context.Context, sub identity.Subject) (*export.Export, error) {
    var exp *export.Export
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        var err error
        if exp, err = a.personal.Start(ctx, tx, sub); err != nil {
            return err
        }
        return ExportJob.Publish(ctx, tx, exp.ID)
    })
    return exp, err
}
```

上一次导出还没有完成时，`Start` 返回 `export.ErrInProgress`（`409 privacy.export_in_progress`），防止用户反复点击，生成一堆压缩包。导出的是用户在所有租户中的数据，所以在另一个租户中发起也算同一个用户。

导出的文件包含他的全部个人信息，接口应当要求刚刚[确认过身份](../security/login.md#confirming)，和注销一样加上 `rest.Confirmed`。

<a id="processing"></a>
## 处理导出

worker 中处理任务，调用 `Process`：

```go
ExportJob.Handle(w, func(ctx context.Context, id uuid.UUID) error {
    return personal.Process(ctx, id, a.saved)
})
```

`Process` 依次调用各项的 `Export`，每完成一项保存一次进度（一项是一个单位），全部完成后把压缩包作为临时文件上传到文件存储，导出变为 `completed`。

某一项返回[业务错误](../general/errors.md)时导出变为 `failed`，`Error` 是错误码。其他错误交给队列重试，重试时从头导出。

`saved` 和[后台操作](operations.md#running)中的用法一样：每次保存进度时在同一个事务中调用，可以为 `nil`。导出完成时发一条通知，告诉用户可以下载了：

```go
func (a *ExportAccount) saved(ctx context.Context, tx database.Tx, exp *export.Export) error {
    if !exp.Finished() {
        return nil
    }
    _, err := notification.Send(ctx, tx, notification.Notification{
        Event: "account.exported",
        Data:  map[string]any{"export_id": exp.ID, "status": exp.Status, "expires_at": exp.ExpiresAt},
    }, notification.RecipientOf(exp.Owner()))
    return err
}
```

`Get(ctx, sub, id)` 返回导出的状态和进度，前端据此显示“正在导出 3/7”；需要实时进度时，用[服务端推送](operations.md#progress)。

<a id="downloading"></a>
## 下载

压缩包不提供公开地址：它包含用户的全部个人信息，一个转发出去的链接就会泄露这些数据。下载接口先认证，再用 `Open` 读取，只有发起导出的本人能打开：

```go
// GET /account/exports/{id}/download
func (a *ExportAccount) Download(ctx context.Context, in *ExportIDInput) (*huma.StreamResponse, error) {
    sub, _ := identity.FromContext(ctx)
    body, exp, err := a.personal.Open(ctx, sub, in.ID)
    if err != nil {
        return nil, err
    }
    return &huma.StreamResponse{Body: func(hctx huma.Context) {
        defer body.Close()
        hctx.SetHeader("Content-Type", "application/zip")
        hctx.SetHeader("Content-Disposition", `attachment; filename="`+exp.Filename+`"`)
        _, _ = io.Copy(hctx.BodyWriter(), body)
    }}, nil
}
```

| 情况 | `Open` 返回 |
| --- | --- |
| 不存在，或者不是本人的导出 | `operation.ErrNotFound`（`404`） |
| 还没有完成，或者失败了 | `export.ErrNotReady`（`409`） |
| 压缩包已经过期 | `export.ErrExpired`（`404`） |

压缩包是临时文件，超过文件存储的 `temp_ttl`（默认 24 小时）后被回收，`ExpiresAt` 是回收的时间。过期后用户重新导出即可。

<a id="archive"></a>
## 压缩包的内容

文件名是 `personal-data-20261117.zip`，里面每一项一个 JSON 文件，另有一个 `export.json` 说明这个包：

```text
personal-data-20261117.zip
├── export.json          主体、导出时间和包含的文件
├── profile.json
├── login.json
├── notifications.json
└── activity.json
```

```json
{
  "subject": {"type": "user", "id": "42"},
  "exported_at": "2026-11-17T08:00:00Z",
  "files": ["profile.json", "login.json", "notifications.json", "activity.json"]
}
```

JSON 缩进排版，普通的文本编辑器就能打开阅读，也方便用户导入到其他服务。

<a id="troubleshooting"></a>
## 常见问题

**导出的 JSON 里有 `BaseModel`、`ID` 这样的字段。**
`Export` 直接返回了 Bun 模型。返回一个只包含给用户看的字段的结构，见[登记数据](#sources)。

**`relation "operations" does not exist`。**
没有发布 `operation` 的迁移。按[安装](#installation)把它加入发布命令。

**导出一直是 `running`。**
任务在重试：某一项返回了普通错误。查看 worker 的日志，修复后任务会从头重新导出；重试用完后导出变为 `failed`。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/privacy/export`。

### New

```go
func New(db *bun.DB, ops *operation.Service, files *filesystem.Service, deletions *privacy.Service) *Service
```

- **参数**
    - `db`：已执行 `operation`、`filesystem` 和本包迁移的数据库
    - `ops`、`files`：记录进度的后台操作服务，保存压缩包的文件服务
    - `deletions`：注销账号的服务，提供账号体系和登记的 `Sources`，其中有 `Export` 的各项依次导出
- **返回**：可以并发使用的服务。参数为 `nil` 时 panic。

### Service 的方法

```go
func (s *Service) Start(ctx context.Context, tx bun.IDB, owner identity.Subject) (*Export, error)
func (s *Service) Process(ctx context.Context, id uuid.UUID, saved func(ctx context.Context, tx database.Tx, exp *Export) error) error
func (s *Service) Get(ctx context.Context, owner identity.Subject, id uuid.UUID) (*Export, error)
func (s *Service) Open(ctx context.Context, owner identity.Subject, id uuid.UUID) (io.ReadCloser, *Export, error)
```

| 方法 | 说明 |
| --- | --- |
| `Start` | 在 `tx` 中登记导出；有没完成的导出时返回 `ErrInProgress` |
| `Process` | 在队列任务中导出、打包并上传 |
| `Get` | 本人的导出的状态和进度 |
| `Open` | 打开本人已完成的压缩包，调用方负责关闭 |

### Export

嵌入 `operation.Operation`（`ID`、`Status`、`Processed`、`Total`、`Error` 等），另有 `FileID`、`Filename` 和 `ExpiresAt`，在完成时写入。`Kind` 常量 `export.Kind`（`privacy.export`）是它在操作列表中的类型。

### 错误

| 错误 | 类别 | 含义 |
| --- | --- | --- |
| `ErrInProgress` | `Conflict` | 上一次导出还没有完成 |
| `ErrNotReady` | `Conflict` | 导出还没有完成，或者失败了 |
| `ErrExpired` | `NotFound` | 压缩包已经过期回收 |

### 各包的 Export

```go
func (r *login.Realm) Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error)
func apitoken.Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error)
func (s *authz.Store) Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error)
func notification.Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error)
func push.Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error)
func activity.Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error)
func chat.ExportUser(ctx context.Context, db bun.IDB, userID string) (any, error)
```
