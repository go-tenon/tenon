# 登录与注册

`login` 证明“这个人是谁”：邮箱密码登录、验证码登录与注册、找回密码、绑定邮箱和手机号、二次验证、Passkey。得到 `identity.Subject` 之后，由 [API 令牌](api-tokens.md#login-and-logout)签发凭据，浏览器的登录把它写进[会话 cookie](api-tokens.md#browser-sessions)。

包：`login`、`login/passkey`、`login/logintest`、`captcha`。

- 账号表属于应用：`users` 表只保存业务字段，登录标识、密码哈希、验证码和二次验证保存在框架的 `auth_*` 表中，按 `identity.Ref` 引用账号。
- 每套[账号体系](authentication.md#account-systems)各用一个 `login.Realm`，在入口中创建一次。

<a id="example-usage"></a>
## 示例

登录接口用邮箱和密码登录，账号启用了二次验证时让客户端再提交验证器上的数字，最后签发 token。`users` 是入口创建的 `*login.Realm`，`tokens` 是 `*apitoken.Store`，`device` 是这次请求的 `apitoken.Device`：

```go
out, err := users.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
if err != nil {
    return nil, err // login.ErrInvalidCredentials → 401；ratelimit.ErrExceeded → 429
}
if out.Pending != "" {
    resp.Body.Pending = out.Pending // 客户端提交验证器上的数字，调用 users.TwoFactor
    return resp, nil
}
plain, err := tokens.Login(ctx, out.Subject, device)
```

`login.Outcome` 的四个字段只有一个不为空：`Subject` 登录成功；`Pending` 等待[二次验证](#two-factor)；`Proof` 交给 `Register`、`ResetPassword` 或 `Link`；`Enroll` 先[启用二次验证](#requiring-two-factor)。完整的登录接口见[入门教程第 6 章](../tutorial/06-login.md)。

<a id="assembling"></a>
## 装配：`login.New`

先[发布](../database/migrations.md#publishing-framework-migrations) `login.Migrations()`，再在[入口](../concepts/dependencies.md#assembling-the-application)中为每套账号创建一个 `Realm`。`a.db` 是入口的 `*bun.DB`，`a.crypt` 是[应用密钥](encryption.md)的 `*crypt.Crypt`，`sendCode` 见[发送验证码](#send-codes)：

```go
every15 := ratelimit.Config{Rate: 5, Interval: 15 * time.Minute, Burst: 5}
users := login.New(a.db, login.Options{
    Type:     user.Type,
    Policy:   login.Policy{Register: true},
    Hasher:   hashing.New(a.cfg.Hashing),
    Crypt:    a.crypt,
    Send:     sendCode(mailer, emails),
    Resolve:  user.Resolve,
    Issuer:   "Blog",
    Attempts: pglimit.New(a.db, "login:user:attempts", every15),
    Sends:    pglimit.New(a.db, "login:user:sends", every15),
})
```

| 选项 | 作用 |
| --- | --- |
| `Type` | 这套账号体系，`Realm` 只管理这个类型的主体；必填 |
| `Hasher`、`Crypt`、`Attempts`、`Sends` | 哈希密码；派生验证码的密钥；密码失败和发送验证码的[限流器](#limits)；必填 |
| `Send` | [发送验证码](#send-codes)；调用 `SendCode` 时必须设置 |
| `Resolve` | 签发凭据之前调用一次，通常是入口的 [Resolver](authentication.md#resolving-subjects)：账号已停用时返回错误，登录失败 |
| `PerTenant` | 登录标识在租户内唯一，见[登录标识](#identifiers) |
| `Issuer` | 验证器 App 中显示的应用名称 |
| `Policy`、`PolicyFor` | 登录策略；各租户不同时用 [`PolicyFor`](#policies) |
| `CodeLength`、`CodeTTL`、`CodeAttempts`、`ProofTTL` | 验证码 6 位、15 分钟有效、可试 3 次；完成凭证和 `Pending` 15 分钟有效 |

`login.Policy` 的字段：`Register` 开放自助注册；`PasswordKinds` 可以配合密码登录的标识类型，默认邮箱、手机号、用户名；`PasswordPolicy` 检查新密码，默认 8 到 128 个字符、首尾没有空白，不符合返回 `login.ErrWeakPassword`；`RequireTwoFactor` [强制二次验证](#requiring-two-factor)；`Methods` 允许的登录方式，`nil` 不限制。

<a id="send-codes"></a>
### 发送验证码：`Options.Send`

`Send` 收到 `login.Code`：用途 `Purpose`（`sign_in`、`reset`、`verify`、`confirm`）、接收的标识 `To`、`Code`、`RequestID`，以及 `Registered`（标识已注册，模板据此写“登录”还是“注册”）。下面按用途选择[邮件模板](../libraries/mail.md#writing-templates)，`mailer` 是 `mail.Mailer`，`emails` 是 `*mail.Templates`：

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

- 支持短信时按 `c.To.Kind` 选择通道，见[短信](../libraries/sms.md)。
- 验证码同步发送，`Send` 的错误由 `SendCode` 原样返回：邮件服务故障时用户无法登录，`mailer` 用 [`mail.Failover`](../libraries/mail.md#failover) 加一个备用服务。
- 放进链接时同时带上 `RequestID` 和 `Code`，打开的页面把两者交给 `VerifyCode`。

<a id="identifiers"></a>
## 登录标识：邮箱、手机号、用户名

| 构造 | 规范化 |
| --- | --- |
| `login.Email(s)` | 去掉首尾空白，转为小写 |
| `login.Phone(s)` | 必须是 E.164 格式，例如 `+8613800138000`，由应用转换用户输入的号码 |
| `login.Username(s)` | 去掉首尾空白，转为小写，中间不允许空白 |

格式不正确时返回 `login.ErrInvalidIdentifier`。[第三方账号](social-login.md)和 passkey 也是标识，类型分别是提供方的名称和 `passkey`。

同一个标识在一套账号体系中只属于一个账号。`PerTenant: true` 时只在租户内唯一，同一个邮箱可以在两个店铺各注册一个账号：查找和写入使用入口的 [`Options.Tenant`](tenancy.md#identifying-tenants) 取出的租户，登录得到的主体带有 `Tenant`，凭据、验证码和完成凭证只能在这个租户使用。

<a id="password"></a>
## 邮箱密码登录：`Password`

```go
out, err := users.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
```

`users` 是这套账号的 `*login.Realm`，`out` 的处理见[示例](#example-usage)。

- 标识不存在、没有密码、密码错误都返回 `login.ErrInvalidCredentials`（`401`），耗时相同，不暴露哪些邮箱注册过。
- 连续失败超过 `Attempts` 的配额返回 `ratelimit.ErrExceeded`（`429`），成功后清零。
- 标识的类型必须在 `Policy.PasswordKinds` 中；旧参数的哈希在登录成功时[重新哈希](encryption.md#rehashing)。

<a id="codes"></a>
## 验证码登录：`SendCode`、`VerifyCode`

`SendCode` 发出验证码、返回请求 ID，客户端把请求 ID 和验证码交给 `VerifyCode`。`users` 是 `*login.Realm`，`tokens` 是 `*apitoken.Store`：

```go
requestID, err := users.SendCode(ctx, login.SignIn, login.Email(in.Body.Email))
```

```go
out, err := users.VerifyCode(ctx, in.Body.RequestID, in.Body.Code)
if err != nil {
    return nil, err // 不正确、已过期或尝试次数用完：login.ErrInvalidCode
}
if out.Proof != "" {
    resp.Body.Proof = out.Proof // 邮箱还没有注册：客户端进入注册页
    return resp, nil
}
plain, err := tokens.Login(ctx, out.Subject, device)
```

- 已注册时直接登录，返回 `out.Subject`（启用了二次验证时是 `out.Pending`）；未注册时返回一次性的完成凭证 `out.Proof`，交给[注册](#register)。
- `Policy.Register` 为 `false` 时验证码不发给未注册的标识，`SendCode` 照样返回请求 ID，不暴露哪些邮箱注册过。

<a id="register"></a>
### 注册账号：`Register`、`CompleteRegister`

框架不创建账号。应用的注册业务动作在自己的事务中插入 `users` 行，再用 `Register` 把已验证的标识挂到这个账号上。`a.db` 是 `*bun.DB`，`a.users` 是 `*login.Realm`：

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
    return a.users.CompleteRegister(ctx, reg)
}
```

- `CompleteRegister` 在事务提交之后调用：它调用的 `Options.Resolve` 从连接池读账号，在事务中读不到未提交的行。
- 任何一步失败都一起回滚，同一个凭证可以重试；标识已属于其他账号时返回 `login.ErrTaken`。`WithPassword` 可以省略。
- [第三方登录](social-login.md#signing-in)和 [Passkey](#passkeys) 的完成凭证交给同一个注册业务动作。

<a id="reset"></a>
## 找回密码：`login.Reset`、`ResetPassword`

用途为 `login.Reset` 的验证码只发给已注册的标识，未注册的同样返回请求 ID。`VerifyCode` 通过后得到 `out.Proof`，客户端连同新密码交给 `ResetPassword`。`users` 是 `*login.Realm`，`tokens` 是 `*apitoken.Store`：

```go
requestID, err := users.SendCode(ctx, login.Reset, login.Email(in.Body.Email))
```

```go
who, err := users.ResetPassword(ctx, in.Body.Proof, in.Body.Password)
if err != nil {
    return nil, err // 新密码不符合 PasswordPolicy：login.ErrWeakPassword
}
if _, err := tokens.RevokeSubject(ctx, who); err != nil {
    return nil, err
}
```

- `ResetPassword` 不登录：用户接着用新密码登录，二次验证仍然有效，只拿到邮箱绕不过它。
- 没有密码的账号用同一个流程设置密码。找回后撤销全部 token，见[撤销令牌](api-tokens.md#revoking-tokens)。

<a id="managing"></a>
## 绑定和更换邮箱、手机号，修改密码

账号设置页中，`Identifiers` 列出账号的全部标识和是否已验证，`HasPassword` 决定显示“设置密码”还是“修改密码”。`a.users` 是 `*login.Realm`，`sub` 是当前登录的主体。

添加或更换邮箱、手机号：`SendCode(ctx, login.Verify, 新标识)` 发验证码，`VerifyCode` 得到的完成凭证交给 `Link`；更换时在同一个事务中用 `Detach` 移除旧的：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    if err := a.users.Link(ctx, tx, sub.Ref(), in.Proof); err != nil {
        return err // 已被其他账号使用：login.ErrTaken
    }
    return a.users.Detach(ctx, tx, sub.Ref(), login.Email(in.OldEmail))
})
```

| 方法 | 用途 |
| --- | --- |
| `SetPassword(ctx, db, ref, password)` | 设置或修改密码，按 `PasswordPolicy` 检查 |
| `RemovePassword(ctx, db, ref)` | 删除密码 |
| `Lookup(ctx, id)` | 管理后台按邮箱查找账号；登录前调用会暴露哪些标识已注册 |

- `Detach`、`RemovePassword` 移除最后一种登录方式时返回 `login.ErrLastMethod`。能登录的方式：第三方账号、passkey；设置了 `Send` 时的邮箱和手机号；有密码时 `PasswordKinds` 中的标识。
- 修改密码之前[确认身份](#confirming)，之后用 `apitoken.Store` 的 `RevokeOthers` 让其他设备下线。

<a id="two-factor"></a>
## 二次验证：`SetupTOTP`、`TwoFactor`

账号可以启用 TOTP 二次验证：登录时除了密码，还要输入验证器 App 上的 6 位数字。`users` 是 `*login.Realm`，`sub` 是当前登录的主体，`db` 是 `*bun.DB`，`tokens` 是 `*apitoken.Store`。

启用分两步。`SetupTOTP` 生成密钥，`setup.URI` 渲染成二维码，`setup.Secret` 供手动输入；用户输入验证器上的数字后，`ConfirmTOTP` 启用并返回 8 个恢复码：

```go
setup, err := users.SetupTOTP(ctx, db, sub.Ref(), email) // email 是验证器中显示的账号名
codes, err := users.ConfirmTOTP(ctx, db, sub.Ref(), in.Code)
```

恢复码在手机丢失时代替验证器，各用一次，明文只由 `ConfirmTOTP` 和 `RegenerateRecoveryCodes` 返回。

启用后，`Password`、`VerifyCode` 和第三方登录返回 `out.Pending`，客户端带着它和验证器上的数字（或恢复码）调用第二步：

```go
out, err := users.TwoFactor(ctx, in.Body.Pending, in.Body.Code)
if err != nil {
    return nil, err // 数字不正确：login.ErrInvalidCode；Pending 过期或错满 5 次：login.ErrInvalidProof
}
plain, err := tokens.Login(ctx, out.Subject, device)
```

- `Pending` 15 分钟内有效，最多输错 5 次。每个数字只能用一次，时钟允许相差 30 秒。
- `DisableTwoFactor` 关闭二次验证，之前先[确认身份](#confirming)。

<a id="requiring-two-factor"></a>
### 强制二次验证：`RequireTwoFactor`

运营后台通常要求员工都启用二次验证：

```go
staff := login.New(db, login.Options{
    // ……
    Policy: login.Policy{RequireTwoFactor: true},
})
```

之后没有经过二次验证的登录都不签发凭据：已经启用的账号返回 `out.Pending`；还没有启用的返回 `out.Enroll`，客户端先引导用户启用，启用后登录成功。`tokens` 是 `*apitoken.Store`：

```go
setup, err := staff.EnrollTOTP(ctx, in.Body.Enroll, email)                // 返回二维码
out, codes, err := staff.CompleteEnroll(ctx, in.Body.Enroll, in.Body.Code) // 启用并登录，codes 是恢复码
plain, err := tokens.Login(ctx, out.Subject, device)
```

- `Enroll` 的有效期和次数与 `Pending` 相同。passkey 登录本身算二次验证；新注册的账号同样先启用。
- 只有部分租户要求二次验证时，由租户的 Resolver 检查，见[按租户的登录策略](#policies)。请求得到 `403 auth.two_factor_required` 时，应用用 `CheckTwoFactor` 校验数字、`tokens.Confirm` 补上方式（见[确认身份](#confirming)），客户端重试。

<a id="passkeys"></a>
## Passkey：`login/passkey`

`login/passkey` 把每个 passkey 作为一个登录标识（类型 `passkey`）加到 `Realm` 上，与邮箱一样可以绑定、解除、按租户隔离。发布 `passkey.Migrations()`，在入口中创建。`users` 是 `*login.Realm`，`a.crypt` 是应用密钥：

```go
passkeys, err := passkey.New(a.db, users, a.crypt, passkey.Config{
    RPID:    "example.com",
    RPName:  "Blog",
    Origins: []string{"https://example.com"},
})
```

> [!WARNING]
> `RPID` 是 passkey 绑定的域名，要长期固定：改了它，已经创建的 passkey 全部失效。

`Origins` 是允许的网页地址；Android App 加上 `android:apk-key-hash:<签名证书的哈希>`，iOS App 用关联域名。

每个操作分两步：`Begin*` 返回 `options`（交给浏览器或 App）和 `token`，客户端把结果连同 `token` 交给 `Finish*`；`token` 5 分钟内有效，只能用一次。`sub` 是当前登录的主体：

```go
// 登录：浏览器列出这个网站的 passkey 让用户选择
c, err := passkeys.BeginLogin(ctx)
out, err := passkeys.FinishLogin(ctx, in.Body.Token, in.Body.Credential) // Credential 是 json.RawMessage；失败返回 passkey.ErrInvalid

// 添加 passkey：FinishRegistration 返回完成凭证，已登录的用户交给 Link
c, err := passkeys.BeginRegistration(ctx, sub.Ref(), email) // email 是设备上显示的账号名；注册新账号时 ref 传零值
proof, err := passkeys.FinishRegistration(ctx, in.Body.Token, in.Body.Credential, in.Body.Name) // Name 例如“iPhone”
err = users.Link(ctx, a.db, sub.Ref(), proof)
```

- 浏览器用 `navigator.credentials.get` 或 `create` 完成中间一步，参数先经 `PublicKeyCredential.parseRequestOptionsFromJSON` 或 `parseCreationOptionsFromJSON` 转换，交回 `credential.toJSON()`。
- 注册新账号时，完成凭证交给[注册业务动作](#register)中的 `Register`。
- 账号设置中用 `List`、`Rename`、`Remove` 管理 passkey。

<a id="policies"></a>
## 按租户的登录策略：`PolicyFor`

[按租户登录](#identifiers)（`PerTenant`）时，各租户可以有不同的策略，例如开放注册、只允许企业 IdP、强制二次验证。`PolicyFor` 按 context 中的租户返回策略，通常读取租户的[设置](../features/settings.md)；`loginSettings` 是 `settings.New` 创建的设置组，字段与 `Policy` 对应：

```go
customers := login.New(a.db, login.Options{
    Type:      customer.Type,
    PerTenant: true,
    PolicyFor: func(ctx context.Context) (login.Policy, error) {
        s, err := loginSettings.Tenant(identity.TenantFromContext(ctx)).Get(ctx)
        if err != nil {
            return login.Policy{}, err
        }
        return login.Policy{Register: s.Register, RequireTwoFactor: s.RequireTwoFactor, Methods: s.Methods}, nil
    },
    // ……
})
```

`Methods` 的名称与 `sub.Auth.Methods` 相同：`password`、`email`、`phone`、第三方提供方的名称、`passkey`。不允许的方式返回 `403 login.method_not_allowed`；找回密码和二次验证的第二步始终允许。

不设置 `PerTenant`、一套账号进多个租户时，登录时还不知道租户，由租户的 Resolver 检查这次登录的方式：

```go
if cfg.RequireSSO && !slices.Contains(sub.Auth.Methods, cfg.Connection) {
    return identity.Subject{}, identity.ErrMethodRequired.New(errorx.Public("methods", []string{cfg.Connection}))
}
```

请求得到 `403 auth.method_required`，`params.methods` 列出可以用的方式，客户端让用户用其中一种重新登录，见[企业单点登录](enterprise-sso.md)。要求二次验证时检查 `sub.Auth.TwoFactor()`，不满足返回 `identity.ErrTwoFactorRequired`。

<a id="confirming"></a>
## 确认身份：`Confirm`、`rest.Confirmed`

修改邮箱、关闭二次验证、删除账号之前，再次确认操作的是账号本人。按账号有的登录方式选一种，`users` 是 `*login.Realm`，`ref` 是当前主体的 `sub.Ref()`：

| 方式 | 调用 |
| --- | --- |
| 密码 | `users.Confirm(ctx, ref, password)` |
| 验证码 | `users.SendConfirmCode(ctx, ref, id)` 发给账号自己的标识，`users.ConfirmCode(ctx, ref, requestID, code)` 校验 |
| 二次验证 | `users.CheckTwoFactor(ctx, ref, code)`，返回用到的方式 |
| passkey、第三方账号 | 再登录一次，`out.Subject.Ref()` 等于 `ref` |

确认一次、一段时间内放行：敏感操作的路由加上 `rest.Confirmed(15*time.Minute)`，距离上次登录或确认超过 15 分钟时返回 `403 auth.confirm_required`；客户端调用应用的确认接口（校验后用 `tokens.Confirm` 记下），再重试原请求。

```go
// POST /account/confirm；a.tokens 是 *apitoken.Store
func (a *ConfirmAccount) Handle(ctx context.Context, sub identity.Subject, password string) error {
    if err := a.users.Confirm(ctx, sub.Ref(), password); err != nil {
        return err
    }
    return a.tokens.Confirm(ctx, sub, login.MethodPassword)
}
```

```go
op := huma.Operation{OperationID: "account-delete", Method: http.MethodDelete, Path: "/account"}
rest.RequireToken(api, tokens.Validate)(&op)
rest.Confirmed(15 * time.Minute)(&op)
```

- `tokens.Confirm` 的方式：密码为 `login.MethodPassword`，验证码为标识的类型，二次验证为 `CheckTwoFactor` 的返回值。
- 登录本身算一次确认，见[认证](authentication.md#key-concepts)。个人访问令牌、OAuth 令牌和代为登录的 token 访问 `Confirmed` 的路由总是 `403`。

<a id="admin-accounts"></a>
## 后台创建账号：`Attach`

运营后台的员工账号由管理员创建，不开放注册。管理员创建 `staff` 行后，用 `Attach` 直接写入邮箱，事务提交后发一个设置密码的验证码作为邀请。`a.staff` 是员工的 `*login.Realm`，`tx` 是创建账号的事务，`member` 是刚插入的员工行：

```go
if err := a.staff.Attach(ctx, tx, member.Ref(), login.Email(in.Email), false); err != nil {
    return err
}
// 事务提交后发送
_, err := a.staff.SendCode(ctx, login.Reset, login.Email(in.Email))
```

员工通过邀请邮件设置密码，邮箱随之标为已验证。

> [!WARNING]
> `Attach` 跳过验证码，未验证的标识同样占用唯一性：只用于管理员建号这类可信的场景，自助注册一律通过验证码。

<a id="forget"></a>
## 注销账号与导出：`Forget`、`Export`

`Forget` 删除账号的全部登录数据，之后这个邮箱可以重新注册；`Export` 导出登录标识和安全设置。两者的签名与 `privacy.Source` 的 `Erase`、`Export` 相同，登记到[注销账号](../features/privacy.md)的清单中，`users` 是 `*login.Realm`：

```go
{Name: "login", Erase: users.Forget, Export: users.Export},
```

注销后留下的 passkey 公钥由 `passkeys.Cleanup` 删除，见[清理](#cleanup)。

<a id="security-events"></a>
## 操作日志与新设备登录提醒

`login` 不回调应用：修改密码、启用二次验证、绑定登录方式时，在调用 `login` 的同一个事务中用 `activity.Record` 记[操作日志](../features/activity.md)，例如事件 `account.password_changed`。登录成功时用 `out.Subject` 作为操作者。

“新设备登录”提醒：签发凭据之前用 `tokens.List(ctx, out.Subject)` 取已有的登录，其中有登录、但没有一个的 `UserAgent` 等于这次的 `device.UserAgent` 时，发邮件或站内通知。

<a id="limits"></a>
## 限流与防探测

| 限制 | 示例配置 | 超出时 |
| --- | --- | --- |
| 同一个标识的密码登录和确认身份，`Options.Attempts` | 15 分钟内连续失败 5 次，成功或找回密码后清零 | `ratelimit.ErrExceeded`，`429` |
| 向同一个标识发送验证码，`Options.Sends` | 15 分钟 5 次 | `ratelimit.ErrExceeded`，`429` |
| 同一个验证码的尝试次数，`Options.CodeAttempts` | 默认 3 次 | 验证码作废，`login.ErrInvalidCode` |

- `Attempts`、`Sends` 用 [`pglimit`](../libraries/rate-limiting.md#postgres-limiter)，多个实例共享计数，每套账号用各自的名字。按 IP 限流用 [`httpx.RateLimit`](../libraries/rate-limiting.md#http-middleware)。
- 防探测：密码登录对“标识不存在”和“密码错误”返回同一个错误；找回密码和不开放注册时，验证码不发给未注册的标识，同样返回请求 ID。

<a id="captcha"></a>
## 人机验证：`captcha`

`Sends` 按标识计数，机器人换一个邮箱就绕过它。开放自助注册时，在发送验证码的接口前用 `captcha` 校验 [Cloudflare Turnstile](https://developers.cloudflare.com/turnstile/) 的结果：

```yaml
captcha:
  secret: ${TURNSTILE_SECRET} # Turnstile 服务端密钥（secret key）
  hostnames: [example.com]    # 允许的站点域名，为空时不检查
  # endpoint: https://api.hcaptcha.com/siteverify  # 用 hCaptcha 时设置，默认 Turnstile
```

前端组件的 `data-action` 设为 `signup`，把得到的 token 和邮箱一起提交。接口先校验 token，再发送验证码。`cfg.Captcha` 是 `captcha.Config`，`users` 是 `*login.Realm`，`in.ip` 是客户端地址（可以为空）：

```go
bots, err := captcha.New(cfg.Captcha, nil) // 入口中创建一次

if err := bots.Verify(ctx, in.Body.Captcha, "signup", in.ip); err != nil {
    return nil, err
}
requestID, err := users.SendCode(ctx, login.SignIn, login.Email(in.Body.Email))
```

- 没有通过返回 `captcha.ErrFailed`（`422`），前端重置组件；每个 token 只能校验一次。Turnstile 连不上时返回 `captcha.ErrUnavailable`（`503`）。
- action 保证注册页的 token 只能用于注册；hCaptcha 不返回 action，传空字符串。
- 测试中把 [`outboundtest`](../libraries/outbound.md#testing) 的客户端传给 `captcha.New`，登记 `captcha.Turnstile` 地址的响应。

<a id="sms-cost"></a>
## 限制短信费用

验证码用短信发送时，在 `Send` 中先过一个全局限流器，给每天的费用设上限。`db` 是 `*bun.DB`，`texts` 是 [`sms.Sender`](../libraries/sms.md)：

```go
smsBudget := pglimit.New(db, "sms:budget", ratelimit.Config{Rate: 5000, Interval: 24 * time.Hour, Burst: 5000})

send := func(ctx context.Context, code login.Code) error { // 作为 Options.Send
    if err := smsBudget.Allow(ctx, "all"); err != nil {
        return err // 当天的额度用完：429，同时告警
    }
    return texts.Send(ctx, sms.Message{To: code.To.Value, Template: "login_code", Params: map[string]string{"code": code.Code}})
}
```

服务商对同一号码限频时返回 `sms.ErrLimited`，同样是 `429`。只服务部分地区时，发送前检查国家码：国际短信更贵，也是刷量的重点。

<a id="cleanup"></a>
## 清理过期验证码：`Cleanup`

过期和用过的验证码、完成凭证留在表中，`users.Cleanup` 每次最多删除 `limit` 条；`passkeys.Cleanup` 删除没有绑定账号的 passkey 和过期的操作记录。`users` 是 `*login.Realm`，`passkeys` 是 `*passkey.Store`。注册为[定时任务](../background/scheduling.md)，每小时一次：

```go
n, err := users.Cleanup(ctx, 1000)
n, err = passkeys.Cleanup(ctx, 1000)
```

<a id="testing"></a>
## 测试：`login/logintest`

`logintest.Realm` 组装测试用的 `Realm`：没有设置的 `Hasher`、`Crypt`、`Attempts`、`Sends` 用最低成本的 bcrypt、随机的应用密钥和宽松的内存限流补上。`Outbox` 记录发出的验证码，`outbox.Send` 作为 `Options.Send`。`db` 是 `dbtest.Open` 建的临时库：

```go
outbox := logintest.NewOutbox()
users := logintest.Realm(t, db, login.Options{Type: user.Type, Send: outbox.Send})

requestID, err := users.SendCode(ctx, login.SignIn, login.Email("alice@example.com"))
code, _ := outbox.Last(login.Email("alice@example.com"))
out, err := users.VerifyCode(ctx, requestID, code.Code)
```

- `logintest.TOTP(setup.Secret)` 计算验证器上显示的数字，交给 `ConfirmTOTP` 或 `TwoFactor`。
- `outbox.FailWith(err)` 模拟邮件或短信服务故障。
- 同一个 30 秒内的 TOTP 数字只能用一次，再次输入前在 [`synctest`](../testing/testing.md#controlling-time) 中 `time.Sleep(30 * time.Second)`。
