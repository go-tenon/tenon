# 注销账号与导出个人数据

用户申请注销账号后，经过冷静期，定时任务在一个事务中删除他在各处的个人数据；用户导出个人数据时，后台把同样这些地方的数据打包成 zip，由本人下载。两者用同一份 `privacy.Source` 清单：每个保存了用户数据的包登记一项，删除什么和导出什么总是一致。

包：`privacy`、`privacy/export`。

<a id="example-usage"></a>
## 示例

```go
deletions := privacy.New(db, privacy.Options{
    Type:  user.Type,           // 注销哪一套账号体系
    Grace: 15 * 24 * time.Hour, // 冷静期 15 天
    Sources: []privacy.Source{
        {Name: "profile", Erase: user.Erase, Export: user.Export}, // 应用自己的用户表
        {Name: "login", Erase: users.Forget, Export: users.Export},
        {Name: "api_tokens", Erase: apitoken.Erase, Export: apitoken.Export},
        {Name: "notifications", Erase: notification.Erase, Export: notification.Export},
        {Name: "activity", Export: activity.Export}, // 操作日志不删除，只导出
    },
})
personal := export.New(db, ops, files, deletions)

// 注销：申请后 15 天由定时任务删除
e, err := deletions.Request(ctx, tx, sub.Ref())
sched.Add("privacy:erase", "*/10 * * * *", schedule.Cleanup(deletions.EraseDue, 100))

// 导出：接口登记并发布任务，worker 中打包，完成后本人下载
exp, err := personal.Start(ctx, tx, sub)
err = personal.Process(ctx, id, nil)
body, _, err := personal.Open(ctx, sub, id)
```

`db` 是入口打开的 `*bun.DB`，`tx` 是业务事务，`sub` 是当前请求的主体（`identity.Subject`），`id` 是导出的 ID。`user.Type` 是用户这套账号体系的名称（`identity.SubjectType`），`users` 是它的 [`login.Realm`](../security/login.md#assembling)，`ops` 是[后台操作](operations.md#installation)的服务，`files` 是[文件服务](../libraries/filesystem.md)，`sched` 是[调度器](../background/scheduling.md#running-the-scheduler)，`export` 是包 `github.com/go-tenon/tenon/privacy/export`。

<a id="installation"></a>
## 装配

1. 把迁移加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行 `migrate up`。`privacy_exports` 引用 `operation` 的表，缺少 `operation.Migrations()` 时报 `relation "operations" does not exist`：

    ```go
    migratecmd.Publish(
        operation.Migrations(),
        privacy.Migrations(), // privacy_erasures：每次注销一行，只有主体的类型、ID 和时间
        export.Migrations(),  // privacy_exports：导出生成的压缩包
    )
    ```

2. 每套账号体系用 `privacy.New` 创建一个 `*privacy.Service`，登记[各项数据](#sources)。选项不合法时 panic。
3. 用 `export.New(db, ops, files, deletions)` 创建导出服务，它导出 `deletions` 中有 `Export` 的各项。
4. 把 `deletions.EraseDue` 交给[定时任务](#erasing)，把导出任务交给 [worker](#processing)。

<a id="sources"></a>
## 用 `privacy.Source` 登记数据

`privacy.Source` 是保存了主体数据的一个地方。`Name` 区分各项，出现在错误信息中，也是导出文件的名字 `<Name>.json`。`Check`、`Erase`、`Export` 至少写一个：

| 字段 | 作用 |
| --- | --- |
| `Check(ctx, ref)` | 报告现在能否注销，见[阻止注销](#checks) |
| `Erase(ctx, db, ref)` | 删除 `ref` 在这里的数据，见[定时删除](#erasing) |
| `Export(ctx, db, ref)` | 返回 `ref` 在这里的数据，编码为 JSON 写进压缩包，见[处理导出](#processing) |

`ref` 是主体的类型和 ID（`identity.Ref`），`db` 是删除或导出所在的事务。

- 全部 `Erase` 按登记的顺序在同一个事务中执行。有外键约束时，删除引用方的项排在被引用的项之前，否则 `EraseDue` 一直报 `violates foreign key constraint`。
- `Erase` 失败后会重试，必须幂等：数据已经不存在时返回 `nil`。
- 全部 `Export` 在同一个只读事务中执行，各个文件是同一时刻的数据。返回 `nil` 时不写这个文件。
- 只有 `Export` 的项（例如操作日志）只导出，注销时不删除。

<a id="framework-sources"></a>
### 框架包提供的 `Erase` 和 `Export`

用到哪个包就登记哪个。它们在 `ref` 无效时返回错误，数据已经不存在时什么都不做：

| 包 | 登记 | 删除 | 导出 |
| --- | --- | --- | --- |
| [`login`](../security/login.md) | `users.Forget`、`users.Export`（`login.Realm`） | 登录标识、密码、二次验证、验证码；passkey 的公钥随后由 [`passkey.Store` 的 `Cleanup`](../security/login.md#passkeys) 删除 | 登录标识及是否验证过、是否设置了密码、是否启用了二次验证 |
| [`apitoken`](../security/api-tokens.md) | `apitoken.Erase`、`apitoken.Export` | 登录 token、个人访问令牌、OAuth 令牌 | 设备和令牌的类型、名称、权限、IP、User-Agent、时间 |
| [`oauthserver`](../security/oauth-server.md) | `oauthserver.Erase` | 还没有换成令牌的授权码 | — |
| [`authz`](../security/authorization.md) | `roles.Erase`、`roles.Export`（`authz.Store`） | 所有租户中的角色，提交之后使权限缓存失效 | 各个租户中的角色 |
| [`notification`](notifications.md) | `notification.Erase`、`notification.Export` | 所有租户中收到的通知 | 同左 |
| [`push`](push.md) | `push.Erase`、`push.Export` | 登记的设备 | 设备的平台、租户、时间 |
| [`feature`](flags.md) | `feature.Erase` | 为这个用户单独做的开关设置 | — |
| [`savedview`](saved-views.md) | `savedview.Erase`、`savedview.Export` | 全部租户与业务对象下的个人视图 | 同左 |
| [`operation`](operations.md) | `operation.Erase` | 发起的后台操作，包括导入和导出的记录 | — |
| [`activity`](activity.md) | `activity.Export` | 不删除，见[保留的数据](#retained-data) | 作为操作人的操作日志 |
| [`ai/chat`](../ai/chat.md) | 包一层 `chat.EraseUser`、`chat.ExportUser` | 全部 AI 对话、摘要和用户状态 | 同左 |

密码哈希、TOTP 密钥、token 和推送设备的 token 是凭据，不导出。`users.Forget` 删除了登录标识，同一个手机号之后可以注册新账号。

AI 对话的用户 ID 由应用约定，`chat.EraseUser`、`chat.ExportUser` 接收这个字符串。用主体的 ID 作为用户 ID 时这样登记：

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

<a id="app-sources"></a>
### 应用自己的表

业务包提供与 `Source` 字段签名相同的 `Erase` 和 `Export`。引用账号行的表（收藏、草稿）用外键的 `ON DELETE CASCADE` 随账号行删除；要保留的数据（评论、订单）去掉署名或清空个人字段，见[保留的数据](#retained-data)：

```go
// internal/user/privacy.go

// Erase 删除用户的账号行；收藏通过外键级联删除，评论保留但去掉署名。
func Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error {
    if _, err := db.NewUpdate().Model((*comment.Comment)(nil)).
        Set("author_id = NULL").Where("author_id = ?", ref.ID).Exec(ctx); err != nil {
        return err
    }
    _, err := db.NewDelete().Model((*User)(nil)).Where("id = ?", ref.ID).Exec(ctx)
    return err
}

// Export 返回用户的资料，用于导出个人数据。
func Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error) {
    u := new(User)
    if err := db.NewSelect().Model(u).Where("id = ?", ref.ID).Scan(ctx); err != nil {
        return nil, err
    }
    return map[string]any{"nickname": u.Nickname, "avatar": u.AvatarURL, "created_at": u.CreatedAt}, nil
}
```

`Export` 返回只含给用户看的字段的 map 或带 `json` 标签的结构：直接返回 Bun 模型时，`BaseModel` 和内部字段也会出现在文件中。

<a id="account-systems"></a>
### 多套账号体系

一个 `privacy.Service` 只处理 `Options.Type` 这一套账号体系，与它的 `login.Realm` 一一对应。应用同时有用户和员工时各建一个服务，登记各自的数据，两个 `EraseDue` 都交给定时任务：

```go
userDeletions := privacy.New(db, privacy.Options{Type: user.Type, Grace: 15 * 24 * time.Hour, Sources: []privacy.Source{
    {Name: "users", Erase: user.Erase},
    {Name: "login", Erase: users.Forget},
    {Name: "api_tokens", Erase: apitoken.Erase},
}})
staffDeletions := privacy.New(db, privacy.Options{Type: staff.Type, Sources: []privacy.Source{
    {Name: "staff", Erase: staff.Erase},
    {Name: "login", Erase: staffRealm.Forget},
    {Name: "api_tokens", Erase: apitoken.Erase},
}})
```

两个服务共用 `privacy_erasures` 表。方法和导出服务收到别的账号体系的主体时返回错误 `subject type "staff" does not belong to service "user"`。

<a id="requesting"></a>
## 申请注销：`Request`

`Request` 先执行全部 `Check`，再在传入的事务中登记注销，申请与操作日志、通知邮件一起提交。返回的 `Erasure.DueAt` 是冷静期结束的时间。已经申请过、还在冷静期中时返回 `privacy.ErrPending`（`409 privacy.erasure_pending`）。`a.deletions` 是 `*privacy.Service`：

```go
// DELETE /account
func (a *DeleteAccount) Handle(ctx context.Context, sub identity.Subject) (*privacy.Erasure, error) {
    var e *privacy.Erasure
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        var err error
        if e, err = a.deletions.Request(ctx, tx, sub.Ref()); err != nil {
            return err
        }
        return activity.Record(ctx, tx, sub, activity.Activity{Event: "account.deletion_requested"})
    })
    return e, err
}
```

注销是敏感操作，接口要求用户刚刚[确认过身份](../security/login.md#confirming)，`tokens` 是 `*apitoken.Store`：

```go
op := huma.Operation{OperationID: "account-delete", Method: http.MethodDelete, Path: "/account"}
rest.RequireToken(api, tokens.Validate)(&op)
rest.Confirmed(15 * time.Minute)(&op)
```

<a id="grace-period"></a>
## 冷静期：`Pending` 与 `Cancel`

`Options.Grace` 设置冷静期，0 表示下一次 `EraseDue` 就删除。冷静期内数据都在，`Request` 不撤销 token，用户照常登录，看到“账号将于某日注销”，可以自己撤销。

客户端启动或登录后用 `Pending` 查询状态，`a.deletions` 是 `*privacy.Service`：

```go
e, err := a.deletions.Pending(ctx, sub.Ref())
switch {
case errors.Is(err, privacy.ErrNotFound):
    // 没有申请注销
case err != nil:
    return nil, err
default:
    // 显示“账号将于 e.DueAt 注销”和撤销按钮
}
```

撤销用 `Cancel`，同样接收事务；没有待执行的注销时返回 `privacy.ErrNotFound`（`404 privacy.erasure_not_found`）：

```go
// POST /account/restore
err := a.deletions.Cancel(ctx, tx, sub.Ref())
```

<a id="checks"></a>
## 阻止注销：`Check`

账号暂时不能注销时（例如他是团队唯一的所有者），在 `Source` 上写 `Check`，返回[业务错误](../general/errors.md)。`db` 是入口打开的 `*bun.DB`：

```go
var ErrSoleOwner = errorx.Define(errorx.Conflict, "team.sole_owner")

{Name: "teams", Check: func(ctx context.Context, ref identity.Ref) error {
    n, err := team.CountSoleOwned(ctx, db, ref.ID)
    if err != nil {
        return err
    }
    if n > 0 {
        return ErrSoleOwner.New()
    }
    return nil
}},
```

- `Request` 和 `Erase` 开始时依次执行全部 `Check`，第一个错误返回给客户端（`409 team.sole_owner`），什么都不登记。
- 申请页面打开时调用 `deletions.Check(ctx, ref)`，提前告诉用户需要先做什么。
- 冷静期结束时不再执行 `Check`。冷静期内新产生的数据（例如新建的团队）由 `Erase` 处理。

<a id="erasing"></a>
## 定时删除：`EraseDue`

`EraseDue` 找到冷静期已过的主体，每个主体在自己的事务中执行全部 `Erase`，成功后在 `privacy_erasures` 中记下 `ErasedAt`。交给 `schedule.Cleanup`，`sched` 是[调度器](../background/scheduling.md#running-the-scheduler)：

```go
sched.Add("privacy:erase", "*/10 * * * *", schedule.Cleanup(deletions.EraseDue, 100))
```

- 任何一项返回错误时，这个主体的修改全部回滚，错误记在 `LastError`，`Attempts` 加一，10 分钟后重试；其他主体照常删除。
- 多个实例同时执行是安全的：每个主体锁定后才删除。正在删除时调用 `Cancel`，它等删除结束后返回 `ErrNotFound`。

`privacy.Erasure` 是 `privacy_erasures` 的 Bun 模型。一直失败的注销这样查：

```go
var stuck []privacy.Erasure
err := db.NewSelect().Model(&stuck).Where("erased_at IS NULL").Where("attempts > 0").Scan(ctx)
```

<a id="erasing-now"></a>
## 立即删除：`Erase`

管理员在后台删除账号时跳过冷静期，用 `Erase`：执行 `Check`，然后在一个事务中执行全部 `Erase`，返回时数据已经删除。主体有待执行的注销时一并完成；任何一项失败时什么都不改变，错误直接返回。

```go
err := a.deletions.Erase(ctx, identity.Ref{Type: user.Type, ID: in.ID})
```

<a id="external-data"></a>
## 删除数据库之外的数据

对象存储中的头像、向量库中的嵌入、支付平台的客户不能随数据库回滚。在 `Erase` 中[发布队列任务](../background/queue.md#publishing-jobs)，任务与删除一起提交，由 worker 执行并在失败时重试：

```go
{Name: "billing", Erase: func(ctx context.Context, db bun.IDB, ref identity.Ref) error {
    return billingjob.DeleteCustomer.Publish(ctx, db, ref.ID)
}},
```

文件用 `files.WithDB(db).Delete(id)` 在事务中标为删除，对象由文件服务的[清理任务](../libraries/filesystem.md#cleanup)删除。

已经打开的 SSE、实时连接在 30 秒内的定期复查中发现 token 已删除并结束。需要立即断开时，在提交之后[提醒复查](../http/sse.md#immediate-revocation)，`credentials` 是 `*broadcast.Credentials`：

```go
{Name: "connections", Erase: func(ctx context.Context, db bun.IDB, ref identity.Ref) error {
    db.(database.Tx).AfterCommit(func(ctx context.Context) error {
        return credentials.RecheckSubject(ctx, ref)
    })
    return nil
}},
```

`Erase` 收到的 `db` 是 `database.Tx`，事务回滚时登记的回调随之丢弃。

<a id="retained-data"></a>
## 保留的数据

下面这些数据通常保留，改为不再指向这个人：

- **[操作日志](activity.md)**：法律要求网络日志保留至少 6 个月。注销后日志中只剩主体的类型和 ID，到期后由 `activity.Cleanup` 删除。所以 `Properties` 中不写手机号、邮箱这类个人信息。
- **订单、发票**：电子商务法要求保存至少 3 年。保留订单，清空或替换收货人姓名、电话。
- **其他人的数据**：群消息、对别人文章的评论，保留内容、去掉署名。

`privacy_erasures` 中删除完成的行保留，证明账号在什么时候删除，它只有类型、ID 和时间。

<a id="starting"></a>
## 发起导出：`export.Start`

导出要几秒到几分钟，放在[队列](../background/queue.md)中执行。接口在一个事务中登记导出、发布任务，`a.personal` 是 `*export.Service`：

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

- 上一次导出还没有完成时返回 `export.ErrInProgress`（`409 privacy.export_in_progress`）。导出的是用户在所有租户中的数据，在哪个租户中发起都算同一个用户。
- 压缩包含有用户的全部个人信息，接口和注销一样加上 `rest.Confirmed`，要求刚刚[确认过身份](../security/login.md#confirming)。
- 导出是一个[后台操作](operations.md)（类型 `export.Kind`，即 `privacy.export`），和其他导入导出出现在同一个任务列表中。

<a id="processing"></a>
## 处理导出：`Process`

worker 中处理任务，`w` 是队列的 worker，`personal` 是 `*export.Service`：

```go
ExportJob.Handle(w, func(ctx context.Context, id uuid.UUID) error {
    return personal.Process(ctx, id, a.saved)
})
```

`Process` 依次调用各项的 `Export`，每完成一项保存一次进度，全部完成后把压缩包作为临时文件上传到文件服务，导出变为 `completed`。

- 某一项返回[业务错误](../general/errors.md)时导出变为 `failed`，`Error` 是错误码。
- 其他错误交给队列重试，重试时从头导出，期间导出停在 `running`；看 worker 的日志找出出错的项。

`saved` 与[后台操作](operations.md#running)中的用法相同：每次保存进度时在同一个事务中调用，可以为 `nil`。导出完成时发一条通知：

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

`personal.Get(ctx, sub, id)` 返回导出的状态和进度（`Processed`、`Total`），前端显示“正在导出 3/7”；实时进度见[服务端推送](operations.md#progress)。

<a id="downloading"></a>
## 下载压缩包：`Open`

压缩包没有公开地址：转发出去的链接会泄露用户的全部个人信息。下载接口先认证，再用 `Open` 读取，只有发起导出的本人能打开：

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
| 还没有完成，或者失败了 | `export.ErrNotReady`（`409 privacy.export_not_ready`） |
| 压缩包已经过期 | `export.ErrExpired`（`404 privacy.export_expired`） |

压缩包是临时文件，超过文件服务的 `temp_ttl`（默认 24 小时）后回收，`ExpiresAt` 是回收的时间，过期后重新导出。

<a id="archive"></a>
## 压缩包的内容

每一项一个缩进排版的 JSON 文件，另有 `export.json` 说明这个包：

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
