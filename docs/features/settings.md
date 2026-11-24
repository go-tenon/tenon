# 设置

设置是管理员在运行时修改的值，例如站点名称、是否开放注册、每个租户的发票抬头；部署时决定、改了要重启的值放在[配置](../general/configuration.md)中。

包：`settings`。

- 一组设置是一个结构体，默认值写在代码中，整组保存为 `settings` 表中的一行 JSON。读取时把保存的 JSON 合并到默认值上。
- 读取不缓存，每次 `Get` 是一次主键查询，修改后所有实例立即生效。

<a id="example-usage"></a>
## 示例

```go
type Site struct {
    Name         string `json:"name" minLength:"1"`
    Registration bool   `json:"registration"`
}

siteSettings := settings.New(db, "site", Site{Name: "我的站点", Registration: true})

site, err := siteSettings.Get(ctx) // 没有保存过时就是默认值
err = siteSettings.Update(ctx, func(s *Site) error {
    s.Registration = false
    return nil
})
```

`db` 是入口打开的 `*bun.DB`，`New` 的第三个参数是默认值。

<a id="installation"></a>
## 安装

1. 把 `settings.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行 `migrate up`，建出 `settings` 表。
2. 在[入口](../concepts/dependencies.md#assembling-the-application)的 `Open` 中为每组设置调用一次 `settings.New`，保存在 `App` 中，交给需要它的业务动作，见[声明设置](#defining-settings)。

<a id="defining-settings"></a>
## 用 `settings.New` 声明设置

结构体和默认值放在使用它的业务包中，例如 `site/settings.go`。字段的 Huma 标签在设置作为[接口](#admin-pages)的请求体时用于校验：

```go
type Settings struct {
    Name          string   `json:"name" minLength:"1" maxLength:"60"`
    SignupOpen    bool     `json:"signup_open"`
    ModerateFirst bool     `json:"moderate_first" doc:"新用户的第一条评论先审核"`
    BannedWords   []string `json:"banned_words" maxItems:"200"`
}

var Defaults = Settings{
    Name:       "My Blog",
    SignupOpen: true,
}
```

在入口的 `Open` 中创建，`a.db` 是 `*bun.DB`：

```go
siteSettings := settings.New(a.db, "site", site.Defaults)
```

名称 `"site"` 保存在表中，发布后不再修改。名称为空或包含空白、类型不是结构体时 `New` panic。

> [!IMPORTANT]
> 字段（包括嵌套结构体的字段）不能带 `omitempty` 或 `omitzero`，否则 `New` panic：零值会从 JSON 中省略，读取时又被默认值覆盖，例如默认为 `true` 的 `SignupOpen` 关闭之后再读又变回 `true`。

需要加密保存第三方密钥时，给 `string` 字段加上 `settings:"secret"` 并在 `New` 传入 `settings.WithCrypt(keys)`：保存时自动由 `crypt.Crypt` 加密存库，读取时自动解密。

<a id="reading-settings"></a>
## 用 `Get` 读取设置

`a.site` 是 `*settings.Group[site.Settings]`：

```go
s, err := a.site.Get(ctx)
if err != nil {
    return nil, err
}
if !s.SignupOpen {
    return nil, ErrSignupClosed.New()
}
```

每次返回新的值，可以随意修改。读取出错时 `Get` 返回错误，不退回默认值：已经关闭的注册在数据库抖动时仍然保持关闭。

<a id="changing-settings"></a>
## 用 `Save`、`Reset` 修改设置

`a.site` 是 `*settings.Group[site.Settings]`。`Save` 用整组值覆盖保存的设置，`Reset` 删除它，恢复默认值：

```go
err := a.site.Save(ctx, s)
err = a.site.Reset(ctx)
```

`Save` 不按标签校验，值来自接口时由接口的请求体校验，见[后台页面与接口](#admin-pages)。

<a id="concurrent-updates"></a>
### 用 `Update` 并发修改

`Save` 是后写覆盖先写。只修改部分字段时用 `Update`：它在事务中锁住这一行，读出、修改、保存，并发的 `Update` 依次执行。`a.site` 是 `*settings.Group[site.Settings]`：

```go
err := a.site.Update(ctx, func(s *site.Settings) error {
    s.BannedWords = append(s.BannedWords, word)
    return nil
})
```

`fn` 返回错误时放弃保存，`Update` 原样返回这个错误。

<a id="logging-changes"></a>
### 用 `WithDB` 记录操作日志

`WithDB` 返回使用事务的副本，让设置和[操作日志](activity.md)一起提交。`a.db` 是 `*bun.DB`，`subject` 是修改设置的主体：

```go
return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    if err := a.site.WithDB(tx).Save(ctx, s); err != nil {
        return err
    }
    return activity.Record(ctx, tx, subject, activity.Activity{
        Event: "settings.changed", TargetType: "settings", TargetID: "site",
    })
})
```

<a id="tenant-settings"></a>
## 租户的设置：`Tenant`

同一组设置可以为每个[租户](../security/tenancy.md)各保存一份，`Tenant` 返回读写某个租户的副本。`a.billing` 是 `*settings.Group[Billing]`：

```go
b, err := a.billing.Tenant(subject.Tenant).Get(ctx)
```

- 租户的设置和全局设置各自独立：租户没有保存过时使用代码中的默认值，不回退到全局保存的值。
- 租户 ID 为空或包含空白时每个方法都返回 `settings.ErrInvalid`，未登录的请求要先确定租户再读取。

<a id="changing-fields"></a>
## 修改设置的字段

| 修改 | 需要做什么 |
| --- | --- |
| 新增字段 | 在默认值中给出它的值，已保存的设置读取时使用它 |
| 删除字段 | 直接删除，旧字段留在 JSON 中，下次保存时去掉 |
| 新增嵌套结构体的字段 | 直接新增，嵌套的结构体逐个字段合并 |
| 改名、改类型 | 写一条迁移转换保存的 JSON |

只有结构体逐个字段合并。切片、map 和指针整体使用保存的值：默认的 `BannedWords` 是 `["spam"]`、保存的是 `[]` 时，读到的是 `[]`。

改名时写一条 SQL [迁移](../database/migrations.md)：

```sql
-- +goose Up
UPDATE settings
SET payload = payload - 'moderate' || jsonb_build_object('moderate_first', payload -> 'moderate')
WHERE name = 'site' AND payload ? 'moderate';
```

改了类型又没有迁移时，`Get` 返回错误，例如 `settings: decode "site": json: cannot unmarshal number into Go struct field ...`。一组设置弃用时，删掉代码，再写一条迁移 `DELETE FROM settings WHERE name = 'site'`。

<a id="system-state"></a>
## 保存系统写入的状态

全局或每个租户只有一份、由系统写入的状态也用 `settings.Group` 保存，例如接入微信第三方平台后推送来的验证票据、定时刷新的平台凭据。

> [!IMPORTANT]
> 一组设置只有一个写入者：管理员填写的配置、回调写入的票据、刷新的凭据分成三组。几个写入者共用一组时，各自的 `Save` 会覆盖别人写的字段。

```go
wechat := settings.New(db, "wechat", WechatConfig{})        // 管理员填写
ticket := settings.New(db, "wechat.ticket", Ticket{})       // 只有推送回调写
token := settings.New(db, "wechat.token", ComponentToken{}) // 只有刷新凭据时写
```

- 只保存事实（收到票据的时间、凭据的到期时间、最近一次失败的时间和原因），“已连接”“等待推送”这类状态在读取时推算；零值表示“还没有发生”。
- 每个授权方各有一份的状态（例如每个公众号的刷新令牌）放在业务自己的表中；授权流程中的临时数据（例如 state）放进回跳地址，用[签名链接](../security/encryption.md#signed-urls)签名。

<a id="access-tokens"></a>
### 用 `Update` 刷新共用的调用凭据

所有实例共用的凭据快过期时，只应有一个实例去刷新。先不加锁 `Get`，快过期时进入 `Update`，在行锁内再检查一次：其他实例在 `Update` 处等待，锁释放后读到新的凭据。`a.token` 是 `*settings.Group[ComponentToken]`，`a.client` 是调用微信接口的客户端：

```go
t, err := a.token.Get(ctx)
if err != nil || time.Until(t.ExpiresAt) > 5*time.Minute {
    return t.Value, err
}
err = a.token.Update(ctx, func(cur *ComponentToken) error {
    if time.Until(cur.ExpiresAt) <= 5*time.Minute { // 等锁时别的实例可能已经刷新过
        v, expiresIn, err := a.client.ComponentToken(ctx)
        if err != nil {
            cur.FailedAt, cur.Failure = time.Now(), err.Error() // 失败也保存，页面显示原因
        } else {
            cur.Value, cur.ExpiresAt, cur.RefreshedAt = v, time.Now().Add(expiresIn), time.Now()
        }
    }
    t = *cur
    return nil
})
```

调用外部接口期间一直持有行锁和一个数据库连接，所以只适合几小时刷新一次、每次耗时很短的凭据。

<a id="admin-pages"></a>
## 后台页面与接口

设置的结构体直接作为 [JSON API](../http/routing.md) 的请求体和响应体，Huma 按字段的标签生成文档并校验。字段都没有 `omitempty`，所以请求体必须给出全部字段，正好对应 `Save` 整组保存：

```go
type SiteSettingsBody struct {
    Body site.Settings
}
```

- 修改设置的接口用权限保护，例如只允许有 `platform:settings` 权限的[运营人员](../security/tenancy.md#platform)；租户管理员只能修改 `Tenant(sub.Tenant)` 的设置。
- 用 `api.Settings` 把读取和保存的两个接口声明为一个设置页面，前端的 `SettingsForm` 按声明渲染表单，见[设置页面](../frontend/resources.md#settings)；几组设置放在一个页面中见[多组设置](../frontend/resources.md#settings-groups)。

<a id="testing"></a>
## 测试

在[测试数据库](../testing/testing.md#database-tests)上使用真实的 `Group`，需要非默认值时先 `Save`：

```go
func TestRegisterWhenSignupClosed(t *testing.T) {
    t.Parallel()
    ctx := t.Context()
    db := dbtest.Open(t, migrations.FS) // 应用的迁移，包括发布的 settings 迁移
    siteSettings := settings.New(db, "site", site.Defaults)
    s := site.Defaults
    s.SignupOpen = false
    require.NoError(t, siteSettings.Save(ctx, s))

    register := user.NewRegister(db, siteSettings)
    // ...
}
```
