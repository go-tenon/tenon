# 登录与注册

`login` 包回答“这个人是谁”：用登录标识和密码登录、用验证码登录或注册、找回密码、绑定和更换邮箱或手机号。得到 `identity.Subject` 之后，由 API 签发凭据：[`tokens.Login`](api-tokens.md#login-and-logout)，浏览器的登录把它写进[会话 cookie](api-tokens.md#browser-sessions)。单页应用、App 和后台都通过这些接口登录。

账号表属于应用。应用的 `users` 表只保存业务字段；登录标识、密码哈希和验证码保存在框架的 `auth_*` 表中，按 [`identity.Ref`](authentication.md#key-concepts) 引用账号。一个账号可以有多个邮箱、手机号，也可以没有密码只用验证码登录。

每套[账号体系](authentication-advanced.md#account-systems)各用一个 `login.Realm`。单账号后台见[单账号后台](admin-example.md)；运营 + 租户两套账号见[运营后台与多租户](saas-example.md)。

阅读前需要了解：[入门教程第 6 章](../tutorial/06-login.md)；[哈希](hashing.md)；[限流](../libraries/rate-limiting.md)。二次验证、Passkey、人机验证等见[登录的安全功能](login-security.md)。

- [示例用法](#example-usage)
- [装配](#assembling)
- [登录标识](#identifiers)
- [密码登录](#password)
- [验证码登录与注册](#codes)
- [找回密码](#reset)
- [管理登录方式](#managing)
- [限流与防探测](#limits)
- [清理](#cleanup)
- [测试](#testing)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面为用户创建一个 `Realm`，用邮箱和密码登录，再为登录成功的用户签发令牌：

```go
users := login.New(db, login.Options{
    Type:     user.Type,
    Policy:   login.Policy{Register: true},
    Hasher:   hashing.New(cfg.Hashing),
    Crypt:    crypt.New(cfg.Crypt),
    Send:     sendCode, // 发送验证码：邮件或短信
    Attempts: pglimit.New(db, "login:attempts", every15),
    Sends:    pglimit.New(db, "login:sends", every15),
})

out, err := users.Password(ctx, login.Email(email), password)
if err != nil {
    return err // login.ErrInvalidCredentials 等
}
if out.Pending != "" {
    // 账号启用了二次验证：让客户端提交验证器上的数字，再调用 users.TwoFactor
}
plain, err := tokens.Login(ctx, out.Subject, device)
```

`Password` 校验邮箱和密码。登录成功时，`out.Subject` 就是这个用户；如果账号启用了二次验证，`out.Pending` 不为空，客户端还要提交验证器上的数字。最后，由 [API 令牌](api-tokens.md) 签发凭据。

<a id="assembling"></a>
## 装配

`login` 没有单独的配置段；它吃的是数据库、[`Hasher`](hashing.md)、[`Crypt`](encryption.md) 和限流器这些已经装配好的依赖。串起来看：

1. 发布迁移，建好 `auth_*` 表
2. 启动时 `Load` 校验哈希、加密等依赖的配置
3. 在入口创建一次 `login.Realm`（每套账号体系各一个）
4. 登录 / 注册接口调用 `Realm` 的方法；成功后再交给 [API 令牌](api-tokens.md) 签发凭据

先发布 `login` 的迁移，见[发布框架迁移](../database/migrations.md#publishing-framework-migrations)：

```go
migrationCmd.AddCommand(migratecmd.Publish(
    login.Migrations(),
))
```

在[入口](../concepts/dependencies.md#assembling-the-application)中为用户创建 `Realm`，`Hasher` 哈希密码，`Crypt` 派生验证码的密钥：

```go
every15 := ratelimit.Config{Rate: 5, Interval: 15 * time.Minute, Burst: 5}
users := login.New(a.db, login.Options{
    Type:     user.Type,
    Policy:   login.Policy{Register: true},
    Hasher:   hashing.New(a.cfg.Hashing),
    Crypt:    a.crypt,
    Send:     sendCode(mailer, emails),
    Resolve:  user.Resolve,
    Attempts: pglimit.New(a.db, "login:user:attempts", every15),
    Sends:    pglimit.New(a.db, "login:user:sends", every15),
})
```

| 选项 | 含义 |
| --- | --- |
| `Type` | 这套账号体系，`Realm` 只管理这个类型的主体 |
| `Policy` | 登录策略，见下表；各租户的策略不同时改用 `PolicyFor`，见[按租户的登录策略](login-security.md#policies) |
| `Send` | 发送验证码，邮件和短信的内容、通道由应用决定，见[邮件](../libraries/mail.md)和[短信](../libraries/sms.md) |
| `Resolve` | 登录成功、签发凭据之前调用一次，通常就是入口的 [Resolver](authentication.md#resolving-subjects)：账号已停用时返回错误，登录失败 |
| `PerTenant` | 登录标识在租户内唯一，见[登录标识](#identifiers) |
| `Attempts`、`Sends` | 密码登录失败和发送验证码的[限流器](#limits)，必填 |

`login.Policy` 的字段：

| 字段 | 含义 |
| --- | --- |
| `Register` | 是否开放自助注册：为 `true` 时验证码也发给未注册的邮箱或手机号，验证后可以注册 |
| `PasswordKinds` | 可以配合密码登录的标识类型，默认邮箱、手机号和用户名 |
| `PasswordPolicy` | 检查新密码，默认要求 8 到 128 个字符、首尾没有空白 |
| `RequireTwoFactor` | 强制[二次验证](login-security.md#requiring-two-factor) |
| `Methods` | 允许的登录方式，`nil` 表示不限制，见[按租户的登录策略](login-security.md#policies) |

验证码默认 6 位数字、15 分钟有效、最多试 3 次，都可以在 `Options` 中修改。`Send` 收到的 `login.Code` 带有用途、接收的标识和验证码，下面用[邮件模板](../libraries/mail.md#writing-templates)发送：

```go
func sendCode(mailer mail.Mailer, emails *mail.Templates) func(context.Context, login.Code) error {
    return func(ctx context.Context, c login.Code) error {
        msg, err := emails.Render(ctx, "code_"+string(c.Purpose), c)
        if err != nil {
            return err
        }
        msg.To = []mail.Address{{Email: c.To.Value}}
        return mailer.Send(ctx, msg)
    }
}
```

`c.Purpose` 是 `sign_in`、`reset`、`verify` 或 `confirm`（[确认身份](login-security.md#confirming)），对应四个模板。`c.Registered` 表示邮箱已经注册，模板可以据此写“登录”还是“注册”。用短信时按 `c.To.Kind` 区分 `email` 和 `phone`。

验证码是同步发送的，用户在等着收码。邮件服务故障时用户就无法登录，所以这里的 `mailer` 最好用 [`mail.Failover`](../libraries/mail-providers.md#failover) 组合一个备用的服务。

<a id="identifiers"></a>
## 登录标识

登录标识是“用什么找到这个账号”：

| 构造 | 规范化 |
| --- | --- |
| `login.Email(s)` | 去掉首尾空白，转为小写 |
| `login.Phone(s)` | 必须是 E.164 格式，例如 `+8613800138000`，由应用把用户输入的号码转换成这个格式 |
| `login.Username(s)` | 去掉首尾空白，转为小写，中间不允许空白 |

格式不正确时返回 `login.ErrInvalidIdentifier`。[第三方登录](social-login.md)的账号也是一种标识，类型是提供方的名称。

同一个标识在一套账号体系中只能属于一个账号。`PerTenant` 为 `true` 时只在租户内唯一：同一个邮箱可以在两个店铺各注册一个客户账号。这时查找和写入都使用 `identity.TenantFromContext(ctx)`，即入口的 [`Options.Tenant`](tenancy.md#identifying-tenants) 取出的租户；登录得到的主体带有 `Tenant`，签发的凭据只能在这个租户使用。验证码和完成凭证也只能在发送时的租户中使用。

<a id="password"></a>
## 密码登录

`Password` 用标识和密码登录，成功后把 `out.Subject` 交给入口签发凭据：

```go
out, err := users.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
if err != nil {
    return nil, err
}
plain, err := tokens.Login(ctx, out.Subject, device)
```

完整的接口见 [API 令牌的登录与登出](api-tokens.md#login-and-logout)。邮箱不存在、没有设置密码、密码错误都返回 `login.ErrInvalidCredentials`，响应 `401`。连续失败太多次时返回 `ratelimit.ErrExceeded`，响应 `429`，见[限流与防探测](#limits)。

允许用哪些标识配合密码登录由 `Policy.PasswordKinds` 决定，默认是邮箱、手机号和用户名。登录成功时，如果密码哈希是用旧的算法或参数生成的，会按当前配置[重新哈希](hashing.md#rehashing)。

<a id="codes"></a>
## 验证码登录与注册

验证码分两步：`SendCode` 发出验证码，返回请求 ID；客户端把请求 ID 和用户输入的验证码交给 `VerifyCode`。

```go
requestID, err := users.SendCode(ctx, login.SignIn, login.Email(in.Body.Email))
```

```go
out, err := users.VerifyCode(ctx, in.Body.RequestID, in.Body.Code)
if err != nil {
    return nil, err // 验证码不正确或已失效：login.ErrInvalidCode
}
if out.Proof != "" {
    // 邮箱还没有注册：把完成凭证交给客户端，进入注册页
    resp.Body.Proof = out.Proof
    return resp, nil
}
plain, err := tokens.Login(ctx, out.Subject, device)
```

邮箱已注册时，`VerifyCode` 直接登录，返回 `out.Subject`。还没有注册时返回 `out.Proof`：一个一次性的完成凭证，15 分钟内有效，证明客户端确实收到了验证码。

框架不替应用创建账号。应用的注册业务动作在自己的事务中插入 `users` 行，再调用 `Register`，把已验证的邮箱挂到这个账号上：

```go
func (a *SignUp) Handle(ctx context.Context, in SignUpInput) (login.Outcome, error) {
    var reg login.Registration
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        u := &User{ID: uuid.Must(uuid.NewV7()).String(), Name: in.Name}
        if _, err := tx.NewInsert().Model(u).Exec(ctx); err != nil {
            return err
        }
        var err error
        reg, err = a.users.Register(ctx, tx, in.Proof, u.Ref(), login.WithPassword(in.Password))
        return err
    })
    if err != nil {
        return login.Outcome{}, err
    }
    // 事务提交之后：调用 Options.Resolve，得到可以签发凭据的 out.Subject
    return a.users.CompleteRegister(ctx, reg)
}
```

`Register` 在事务中写入，`CompleteRegister` 在事务提交之后调用 `Options.Resolve` 并返回 `Outcome`。分成两步是因为 `Resolve` 通常从连接池读取账号，在事务中调用时读不到还没有提交的 `users` 行。`Outcome.Subject` 的 `Auth` 记下注册时验证的方式；账号体系[强制二次验证](login-security.md#requiring-two-factor)时返回的是 `Outcome.Enroll`，先启用二次验证再签发凭据。

[第三方登录](social-login.md#signing-in)得到的注册凭证也交给同一个注册业务动作。注册需要什么资料、要不要同意协议都由应用决定。任何一步失败，账号行和凭证的使用一起回滚，客户端可以修正后用同一个凭证重试。两个人同时用同一个邮箱注册时，后完成的得到 `login.ErrTaken`。`WithPassword` 可以省略，只用验证码登录的账号没有密码。

`Policy.Register` 为 `false` 时不开放自助注册：未注册的邮箱收不到验证码。为了不暴露哪些邮箱注册过，`SendCode` 照样返回请求 ID，只是用它验证必然失败。

验证码也可以放进链接：模板中的链接同时带上 `RequestID` 和 `Code`，链接打开的页面把两者交给 `VerifyCode`。

<a id="reset"></a>
## 找回密码

找回密码用 `login.Reset`：

```go
requestID, err := users.SendCode(ctx, login.Reset, login.Email(in.Body.Email))
```

验证码只发给已注册的邮箱，未注册的同样返回一个请求 ID。`VerifyCode` 通过后得到 `out.Proof`，客户端连同新密码交给 `ResetPassword`：

```go
who, err := users.ResetPassword(ctx, in.Body.Proof, in.Body.Password)
if err != nil {
    return nil, err
}
if _, err := tokens.RevokeSubject(ctx, who); err != nil {
    return nil, err
}
```

新密码按 `Policy.PasswordPolicy` 检查，默认要求 8 到 128 个字符、首尾没有空白，不符合时返回 `login.ErrWeakPassword`。接收验证码的邮箱同时标为已验证，它的登录失败计数清零。找回密码后通常撤销这个账号的全部 token，见[撤销令牌](api-tokens.md#revoking-tokens)。`ResetPassword` 只重置密码，用户接着用新密码登录，启用了[二次验证](login-security.md#two-factor)的账号仍然要通过二次验证，所以只拿到邮箱绕不过它。没有设置过密码的账号同样可以用这个流程设置密码。

<a id="managing"></a>
## 管理登录方式

账号设置页面中，`Identifiers` 列出账号的全部标识和是否已验证，`HasPassword` 决定显示“设置密码”还是“修改密码”。

添加或更换邮箱、手机号时，先用 `login.Verify` 向新的标识发验证码，验证后把完成凭证交给 `Link`。更换时在同一个事务中用 `Detach` 移除旧的：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    if err := a.users.Link(ctx, tx, sub.Ref(), in.Proof); err != nil {
        return err // 已被其他账号使用：login.ErrTaken
    }
    return a.users.Detach(ctx, tx, sub.Ref(), login.Email(in.OldEmail))
})
```

`Detach` 和 `RemovePassword` 始终为账号保留至少一种登录方式，移除最后一种时返回 `login.ErrLastMethod`。能登录的方式有三种：第三方账号和 passkey；设置了 `Options.Send` 时的邮箱和手机号（用验证码登录）；有密码时的 `PasswordKinds` 标识。例如只剩用户名的账号要保留密码。

| 方法 | 用途 |
| --- | --- |
| `SetPassword(ctx, db, ref, password)` | 设置或修改密码，按密码策略检查 |
| `RemovePassword(ctx, db, ref)` | 删除密码，之后只能用验证码、第三方账号或 passkey 登录 |
| `Lookup(ctx, id)` | 管理后台按邮箱或手机号查找账号 |

修改密码后，用 `RevokeOthers` 让其他设备下线，见[撤销令牌](api-tokens.md#revoking-tokens)。

<a id="limits"></a>
## 限流与防探测

| 限制 | 上面的配置 | 超出时 |
| --- | --- | --- |
| 同一个标识的密码登录，`Options.Attempts` | 15 分钟内连续失败 5 次，登录成功或找回密码后清零 | `ratelimit.ErrExceeded`，`429` |
| 向同一个标识发送验证码，`Options.Sends` | 15 分钟 5 次 | `ratelimit.ErrExceeded`，`429` |
| 同一个验证码的尝试次数，`Options.CodeAttempts` | 默认 3 次 | 验证码作废，`login.ErrInvalidCode` |

`Attempts` 和 `Sends` 用 [`pglimit`](../libraries/rate-limiting.md#postgres-limiter)，多个实例共享计数，记得发布它的迁移、定时清理回满的计数；每套账号用各自的名字。按 IP 的限流不在 `login` 中，需要时在登录接口上安装 [`httpx.RateLimit`](../libraries/rate-limiting.md#http-middleware)。

为了保护“哪些邮箱注册过”这一信息：密码登录对“邮箱不存在”和“密码错误”返回同一个错误，耗时相同；找回密码和不开放注册时的验证码不发给未注册的邮箱，但同样返回请求 ID。验证码在库中只保存由应用密钥派生的 HMAC，数据库泄露时验证码仍然安全；完成凭证只保存 SHA-256。

<a id="cleanup"></a>
## 清理

过期和已用过的验证码校验失败，但行仍然留在表中。`Cleanup` 每次最多删除 `limit` 条，可以注册为[定时任务](../background/scheduling.md)：

```go
n, err := users.Cleanup(ctx, 1000)
```

<a id="testing"></a>
## 测试

`login/logintest` 提供三个工具。`Realm` 组装测试用的 `Realm`：没有设置的 `Hasher`、`Crypt`、`Attempts`、`Sends` 用适合测试的值补上，即最低成本的 bcrypt、随机的应用密钥和宽松的内存限流。`Outbox` 记录发出的验证码，测试中把 `outbox.Send` 作为 `Options.Send`：

```go
outbox := logintest.NewOutbox()
users := logintest.Realm(t, db, login.Options{Type: user.Type, Send: outbox.Send})

requestID, err := users.SendCode(ctx, login.SignIn, login.Email("alice@example.com"))
code, _ := outbox.Last(login.Email("alice@example.com"))
out, err := users.VerifyCode(ctx, requestID, code.Code)
```

`FailWith` 让之后的发送都返回错误，模拟邮件或短信服务故障。`TOTP` 计算验证器上显示的数字：

```go
setup, err := users.SetupTOTP(ctx, db, ref, "alice@example.com")
codes, err := users.ConfirmTOTP(ctx, db, ref, logintest.TOTP(setup.Secret))
```

同一个 30 秒内的验证码只能用一次，测试接着还要输入时（例如启用后重新登录），在 [`synctest`](../testing/testing.md#controlling-time) 中 `time.Sleep(30 * time.Second)`。密码哈希按生产配置计算很慢，`logintest.Realm` 用的是成本最低的 bcrypt；自己组装 `Realm` 时用 `hashing.New(hashing.Config{Driver: "bcrypt", Bcrypt: hashing.BcryptConfig{Cost: 4}})`。

<a id="troubleshooting"></a>
## 常见问题

**密码登录返回 401，`login.ErrInvalidCredentials`**

邮箱不存在、没有设置密码、密码错误都返回同一个错误，避免泄露哪些邮箱注册过。检查用户输入的标识和密码。

**登录返回 429**

同一个标识 15 分钟内连续失败 5 次，或者 15 分钟内发送验证码超过 5 次，见[限流与防探测](#limits)。

**设置密码时返回 `login.ErrWeakPassword`**

默认要求 8 到 128 个字符、首尾没有空白。需要其他规则时设置 `Policy.PasswordPolicy`。

**移除登录方式时返回 `login.ErrLastMethod`**

账号始终保留至少一种登录方式。先添加另一种，再移除这一种，见[管理登录方式](#managing)。

**找回密码时，未注册的邮箱没有收到验证码**

这是有意的：找回密码不发给未注册的邮箱，但同样返回成功，避免泄露哪些邮箱注册过。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/login`。

### New、Options

```go
func New(db bun.IDB, opts Options) *Realm
```

一套账号体系一个 `Realm`。`Options` 的 `Type`、`Hasher`、`Crypt`、`Attempts`、`Sends` 必填：

| 字段 | 说明 |
| --- | --- |
| `Type identity.SubjectType` | 账号体系 |
| `PerTenant bool` | 按租户登录，同一个人在两个租户中是两个账号 |
| `Policy Policy`、`PolicyFor` | 登录策略：是否开放注册、密码规则、是否要求二次验证、允许的登录方式；`PolicyFor` 按请求（例如租户）决定 |
| `Send func(ctx, Code) error` | 发送验证码 |
| `Resolve identity.Resolver` | 登录成功后加载主体的其他字段 |
| `Attempts`、`Sends ratelimit.Limiter` | 校验和发送的频率限制 |
| `CodeLength`、`CodeTTL`、`CodeAttempts`、`ProofTTL` | 验证码的长度、有效期、可尝试次数，完成凭证的有效期 |

### 登录

```go
func (r *Realm) Password(ctx context.Context, id Identifier, password string) (Outcome, error)
func (r *Realm) SendCode(ctx context.Context, purpose Purpose, id Identifier) (string, error)
func (r *Realm) VerifyCode(ctx context.Context, requestID, code string) (Outcome, error)
func (r *Realm) External(ctx context.Context, ext External) (Outcome, error)
func (r *Realm) TwoFactor(ctx context.Context, pending, code string) (Outcome, error)
func Email(address string) Identifier
func Phone(e164 string) Identifier
func Username(name string) Identifier
```

`Outcome.Subject` 是登录成功的主体；`Pending` 不为空时还要二次验证；`Proof` 不为空时交给 `Register`、`ResetPassword` 或 `Link`；`Enroll` 不为空时要先启用二次验证。

### 注册与找回

```go
func (r *Realm) Register(ctx context.Context, db bun.IDB, proof string, ref identity.Ref, opts ...RegisterOption) (Registration, error)
func (r *Realm) CompleteRegister(ctx context.Context, reg Registration) (Outcome, error)
func (r *Realm) ResetPassword(ctx context.Context, proof, password string) (identity.Ref, error)
func (r *Realm) Attach(ctx context.Context, db bun.IDB, ref identity.Ref, id Identifier, verified bool) error
```

### 账号管理

| 方法 | 说明 |
| --- | --- |
| `SetPassword`、`RemovePassword`、`HasPassword` | 设置、删除、检查密码 |
| `Identifiers`、`Link`、`Detach`、`Lookup` | 登录标识的列表、绑定、解绑、查找 |
| `Confirm`、`SendConfirmCode`、`ConfirmCode` | 用密码或验证码确认身份 |
| `SetupTOTP`、`ConfirmTOTP`、`DisableTwoFactor`、`TwoFactorEnabled` | 二次验证 |
| `RegenerateRecoveryCodes`、`RecoveryCodesLeft` | 恢复码 |
| `Forget` | 删除账号的全部登录数据，登记到[注销账号](../features/privacy.md#framework-sources) |
| `Export` | 导出账号的登录标识和安全设置，登记到[导出个人数据](../features/data-export.md#sources) |
| `Cleanup` | 删除过期的验证码和凭证 |

### passkey

包 `github.com/go-tenon/tenon/login/passkey`。

```go
func New(db bun.IDB, realm *login.Realm, c *crypt.Crypt, cfg Config) (*Store, error)
func (s *Store) BeginRegistration(ctx context.Context, ref identity.Ref, name string) (Ceremony, error)
func (s *Store) FinishRegistration(ctx context.Context, token string, response []byte, name string) (string, error)
func (s *Store) BeginLogin(ctx context.Context) (Ceremony, error)
func (s *Store) FinishLogin(ctx context.Context, token string, response []byte) (login.Outcome, error)
```

见 [Passkey](login-security.md#passkeys)。
