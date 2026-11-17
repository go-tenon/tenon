# 注销账号

用户可以要求注销账号，删除你保存的他的个人信息。个人信息保护法规定了这项权利，应用商店审核 App 时也会检查有没有注销入口。难点在于数据分散：账号行在应用自己的表里，登录凭据、token、通知、推送设备、权限、AI 对话分别在框架的不同表里，漏掉一处就没有删干净。

`privacy` 包把这些地方登记在一起。用户申请注销后先经过一段**冷静期**，期间可以撤销；冷静期结束后，定时任务在**一个事务**中删除全部登记的数据，要么全部删除，要么失败回滚、稍后重试。

阅读前需要了解：[数据库事务](../database/quick-start.md#transactions)；[发布框架迁移](../database/migrations.md#publishing-framework-migrations)；[定时任务](../background/scheduling.md)；[业务错误](../general/errors.md)。

- [示例用法](#example-usage)
- [安装](#installation)
- [登记数据](#sources)
    - [框架的数据](#framework-sources)
    - [应用自己的数据](#app-sources)
    - [多套账号体系](#account-systems)
- [申请注销](#requesting)
- [冷静期](#grace-period)
- [能否注销](#checks)
- [执行删除](#erasing)
- [立即删除](#erasing-now)
- [数据库之外的数据](#external-data)
- [保留的数据](#retained-data)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

装配时列出保存了用户数据的地方，申请注销的接口调用 `Request`，定时任务调用 `EraseDue`：

```go
deletions := privacy.New(db, privacy.Options{
    Type:  user.Type,           // 注销哪一套账号体系
    Grace: 15 * 24 * time.Hour, // 冷静期 15 天
    Sources: []privacy.Source{
        {Name: "users", Erase: user.Erase},          // 应用自己的用户表
        {Name: "login", Erase: users.Forget},        // 登录标识、密码、二次验证
        {Name: "api_tokens", Erase: apitoken.Erase}, // 全部 token
        {Name: "push", Erase: push.Erase},
        {Name: "notifications", Erase: notification.Erase},
    },
})

// 申请注销：15 天后删除
e, err := deletions.Request(ctx, db, sub.Ref())

// 定时任务：每 10 分钟删除一批冷静期已过的账号
sched.Add("privacy:erase", "*/10 * * * *", schedule.Cleanup(deletions.EraseDue, 100))
```

`user.Type` 是用户这套账号体系的名称（`identity.SubjectType`），`users` 是它[登录](../security/login.md#assembling)用的 `login.Realm`，`sched` 是[调度器](../background/scheduling.md#running-the-scheduler)。下面先发布迁移，再逐步介绍每一部分。

<a id="installation"></a>
## 安装

把 `privacy` 的迁移加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行 `migrate up`：

```go
migratecmd.Publish(
    privacy.Migrations(),
)
```

迁移创建 `privacy_erasures` 表，每次注销一行：主体的类型和 ID、申请时间、计划删除的时间和删除完成的时间，不保存别的个人信息。

<a id="sources"></a>
## 登记数据

`privacy.Source` 是保存了主体数据的一个地方。`Name` 用来区分各项，出现在错误信息里；`Erase` 删除这个主体在这里的数据：

```go
type Source struct {
    Name  string
    Check func(ctx context.Context, ref identity.Ref) error
    Erase func(ctx context.Context, db bun.IDB, ref identity.Ref) error
    Export func(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error)
}
```

`ref` 是主体的类型和 ID（`identity.Ref`），`db` 是删除所在的事务。全部 `Erase` 按登记的顺序在同一个事务中执行。`Check` 见[能否注销](#checks)，`Export` 用于[导出个人数据](data-export.md)，同一份清单既决定删除什么，也决定导出什么。

<a id="framework-sources"></a>
### 框架的数据

框架中保存主体数据的包都提供了这个签名的函数，用到哪个包就登记哪个：

| 包 | 登记 | 删除什么 |
| --- | --- | --- |
| [`login`](../security/login.md) | `users.Forget`（每套账号的 `login.Realm`） | 登录标识、密码、二次验证、验证码；passkey 的公钥随后由 [`passkeys.Cleanup`](../security/login-security.md#passkeys) 删除 |
| [`apitoken`](../security/api-tokens.md) | `apitoken.Erase` | 登录 token、个人访问令牌、OAuth 令牌 |
| [`oauthserver`](../security/oauth-server.md) | `oauthserver.Erase` | 还没有换成令牌的授权码 |
| [`authz`](../security/authorization.md) | `roles.Erase`（`authz.Store`） | 所有租户中的角色，提交之后使权限缓存失效 |
| [`notification`](notifications.md) | `notification.Erase` | 所有租户中收到的通知 |
| [`push`](push.md) | `push.Erase` | 登记的设备 |
| [`feature`](flags.md) | `feature.Erase` | 为这个用户单独做的开关设置 |
| [`operation`](operations.md) | `operation.Erase` | 发起的后台操作，包括导入和导出的记录 |
| [`ai/chat`](../ai/chat.md) | 包一层 `chat.EraseUser`，见下文 | 全部 AI 对话、摘要和用户状态 |

它们在 `ref` 无效时返回错误，数据已经不存在时什么都不做，所以失败后重试也是安全的。

AI 对话的用户 ID 由应用约定，`chat.EraseUser` 接收这个字符串。用主体的 ID 作为用户 ID 时这样登记：

```go
{Name: "ai_sessions", Erase: func(ctx context.Context, db bun.IDB, ref identity.Ref) error {
    return chat.EraseUser(ctx, db, ref.ID)
}},
```

<a id="app-sources"></a>
### 应用自己的数据

应用自己的表由业务包提供 `Erase`。删除账号行时，引用它的表（例如用户的收藏）用外键的 `ON DELETE CASCADE` 一起删除；不能删除的数据（例如订单）改为匿名化，见[保留的数据](#retained-data)：

```go
// internal/user/erase.go

// Erase 删除用户的账号行；收藏、草稿通过外键级联删除，评论保留但去掉署名。
func Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error {
    if _, err := db.NewUpdate().Model((*comment.Comment)(nil)).
        Set("author_id = NULL").Where("author_id = ?", ref.ID).Exec(ctx); err != nil {
        return err
    }
    _, err := db.NewDelete().Model((*User)(nil)).Where("id = ?", ref.ID).Exec(ctx)
    return err
}
```

有外键约束时，登记的顺序很重要：被引用的行要在引用它的行之后删除。

<a id="account-systems"></a>
### 多套账号体系

一个 `privacy.Service` 只注销 `Options.Type` 这一套账号体系的主体，与这套账号的 `login.Realm` 一一对应，各项 `Erase` 只会收到这个类型的主体。应用同时有用户（`user`）和员工（`staff`）时，各建一个服务，登记各自的数据：

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

两个服务共用 `privacy_erasures` 表，各自的 `EraseDue` 只删除自己这套账号体系的注销，两个都要交给定时任务。方法收到别的账号体系的主体时返回错误。

<a id="requesting"></a>
## 申请注销

注销是敏感操作，接口要求用户刚刚[确认过身份](../security/login-security.md#confirming)。`Request` 接收事务，申请与其他修改（例如操作日志、通知邮件）一起提交：

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

```go
op := huma.Operation{OperationID: "account-delete", Method: http.MethodDelete, Path: "/account"}
rest.RequireToken(api, tokens.Validate)(&op)
rest.Confirmed(15 * time.Minute)(&op)
```

`Request` 先执行全部 `Check`，再写入 `privacy_erasures`，`DueAt` 是冷静期结束的时间。已经申请过、还在冷静期中时返回 `privacy.ErrPending`（`409 privacy.erasure_pending`）。

<a id="grace-period"></a>
## 冷静期

冷静期内账号照常可以登录，数据也都还在。`Request` 不撤销任何 token，用户登录后看到“账号将于某日注销”，改变主意时自己撤销。

客户端在启动或登录后查询状态，用 `Pending`：

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

撤销用 `Cancel`，同样接收事务；没有待执行的注销时返回 `privacy.ErrNotFound`：

```go
// POST /account/restore
err := a.deletions.Cancel(ctx, tx, sub.Ref())
```

`Options.Grace` 设置冷静期，常见的是 7 到 15 天。设为 0 表示没有冷静期，下一次 `EraseDue` 就删除。

<a id="checks"></a>
## 能否注销

有些账号现在不能注销，例如他是团队唯一的所有者，注销后团队就没人管理了。在 `Source` 上写 `Check`，返回[业务错误](../general/errors.md)：

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

`Request` 和 `Erase` 在开始时依次执行全部 `Check`，第一个错误原样返回给客户端（`409 team.sole_owner`），什么都不登记。申请页面打开时可以先调用 `deletions.Check(ctx, ref)`，提前告诉用户需要先做什么。

冷静期结束时不再执行 `Check`：冷静期内用户仍然可以操作，例如新建了一个团队，这些新数据由 `Erase` 处理，例如删除只有他一个成员的团队。

<a id="erasing"></a>
## 执行删除

`EraseDue` 找到冷静期已过的主体，每个主体在自己的事务中执行全部 `Erase`，成功后在 `privacy_erasures` 中记下 `ErasedAt`。它的形式与框架的[清理函数](../background/scheduling.md#framework-cleanups)相同，交给 `schedule.Cleanup`：

```go
sched.Add("privacy:erase", "*/10 * * * *", schedule.Cleanup(deletions.EraseDue, 100))
```

任何一项返回错误时，这个主体的修改全部回滚，错误记在 `LastError`，`Attempts` 加一，10 分钟后重试；其他主体照常删除，错误合并返回，调度器记录日志。一直失败的注销可以这样查出来：

```go
var stuck []privacy.Erasure
err := db.NewSelect().Model(&stuck).Where("erased_at IS NULL").Where("attempts > 0").Scan(ctx)
```

多个实例同时执行 `EraseDue` 也是安全的：每个主体被锁定后才删除，只删除一次。正在删除时调用 `Cancel`，会等删除结束，然后返回 `ErrNotFound`。

<a id="erasing-now"></a>
## 立即删除

管理员在后台删除账号时不需要冷静期，用 `Erase`。它执行 `Check`，然后在一个事务中执行全部 `Erase`，返回时数据已经删除：

```go
err := a.deletions.Erase(ctx, identity.Ref{Type: user.Type, ID: in.ID})
```

主体有待执行的注销时一并完成。任何一项失败时什么都不改变，错误直接返回。

<a id="external-data"></a>
## 数据库之外的数据

有些数据不在 PostgreSQL 中：对象存储里的头像、向量库里的嵌入、支付平台的客户。这些不能和数据库一起回滚，所以不在 `Erase` 中直接删除，而是在事务中[发布队列任务](../background/queue.md#publishing-jobs)，任务与删除一起提交，由 worker 执行并在失败时重试：

```go
{Name: "billing", Erase: func(ctx context.Context, db bun.IDB, ref identity.Ref) error {
    return billingjob.DeleteCustomer.Publish(ctx, db, ref.ID)
}},
```

文件服务本身就是这样：在 `Erase` 中用 `files.WithDB(db).Delete(id)` 把文件标为删除，对象由文件服务的[清理任务](../libraries/filesystem.md#cleanup)删除。

已经打开的 SSE、实时连接会在 30 秒内的定期复查中发现 token 已经删除并结束。需要立即断开时，在提交之后[提醒复查](../http/sse.md#immediate-revocation)：

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

删除个人信息不等于删除一切。下面这些数据通常要保留，改为不再指向这个人：

- **[操作日志](activity.md)**：法律要求网络日志保留至少 6 个月，日志也是出现纠纷时的证据。注销后日志中只剩主体的类型和 ID，不再能对应到任何人；到期后由 `activity.Cleanup` 按保留期删除。因此不要在 `Properties` 中写手机号、邮箱这类个人信息。
- **订单、发票**：电子商务法要求保存至少 3 年。保留订单，把收货人姓名、电话这类字段清空或替换。
- **其他人的数据**：用户发在群里的消息、对别人文章的评论，删除会破坏别人的内容，通常保留内容、去掉署名。

`privacy_erasures` 中删除完成的行也会保留，用来证明这个账号在什么时候删除。它只有类型、ID 和时间。

<a id="troubleshooting"></a>
## 常见问题

**`EraseDue` 一直报 `violates foreign key constraint`。**
登记顺序不对：引用账号行的表还有数据，账号行就被删除了。把删除引用方的那一项排在前面，或者给外键加上 `ON DELETE CASCADE`。

**`subject type "staff" does not belong to service "user"`。**
把员工交给了用户的服务。每套账号体系各建一个服务，见[多套账号体系](#account-systems)。

**注销后，同一个手机号能重新注册吗？**
可以。`login.Realm.Forget` 删除了登录标识，手机号可以重新注册，得到的是一个新的账号。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/privacy`。

### New

```go
func New(db *bun.DB, opts Options) *Service
```

- **参数**
    - `db`：已执行 `Migrations` 的数据库
    - `opts.Type`：这个服务注销的账号体系，必填
    - `opts.Grace`：冷静期，0 表示没有，不能为负数
    - `opts.Sources`：保存了主体数据的地方，至少一项；`Name` 不能为空或重复，`Check` 和 `Erase` 至少有一个
- **返回**：可以并发使用的服务。选项不合法时 panic。

### Service 的方法

```go
func (s *Service) Request(ctx context.Context, tx bun.IDB, ref identity.Ref) (*Erasure, error)
func (s *Service) Pending(ctx context.Context, ref identity.Ref) (*Erasure, error)
func (s *Service) Cancel(ctx context.Context, tx bun.IDB, ref identity.Ref) error
func (s *Service) Check(ctx context.Context, ref identity.Ref) error
func (s *Service) Erase(ctx context.Context, ref identity.Ref) error
func (s *Service) EraseDue(ctx context.Context, limit int) (int, error)
```

| 方法 | 说明 |
| --- | --- |
| `Request` | 执行 `Check`，在 `tx` 中登记注销；已经申请过时返回 `ErrPending` |
| `Pending` | 待执行的注销；没有时返回 `ErrNotFound` |
| `Cancel` | 在 `tx` 中撤销待执行的注销；没有时返回 `ErrNotFound` |
| `Check` | 依次执行各项的 `Check`，返回第一个错误 |
| `Erase` | 跳过冷静期，立即删除；失败时什么都不改变 |
| `EraseDue` | 删除最多 `limit` 个冷静期已过的主体，返回删除完成的个数，交给 `schedule.Cleanup` |

### Erasure

`privacy_erasures` 表的 Bun 模型，后台列表等其他查询可以直接用 Bun 编写。字段：`ID`、`SubjectType`、`SubjectID`、`RequestedAt`、`DueAt`（冷静期结束或下一次重试的时间）、`ErasedAt`（零值表示还没有删除）、`Attempts`、`LastError`。方法 `Subject()` 返回主体，`Erased()` 报告是否已经删除。

### 错误

| 错误 | 类别 | 含义 |
| --- | --- | --- |
| `ErrPending` | `Conflict` | 已经申请注销，还在冷静期中 |
| `ErrNotFound` | `NotFound` | 没有待执行的注销 |

### 各包的 Erase

```go
func (r *login.Realm) Forget(ctx context.Context, db bun.IDB, ref identity.Ref) error
func apitoken.Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error
func oauthserver.Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error
func (s *authz.Store) Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error
func notification.Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error
func push.Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error
func feature.Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error
func operation.Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error
func chat.EraseUser(ctx context.Context, db bun.IDB, userID string) error
```
