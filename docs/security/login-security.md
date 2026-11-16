# 登录的安全功能

[登录与注册](login.md)一页介绍了密码登录、验证码登录、找回密码和管理登录方式。这一页介绍在此之上加强账号安全的功能，按需使用：

- **二次验证**：登录时除了密码，还要输入手机上的动态码；
- **Passkey**：用设备的指纹、面容或 PIN 登录，不需要密码；
- **按租户的登录策略**：例如某个企业租户要求成员必须开启二次验证；
- **确认身份**：修改密码、删除账号这类敏感操作前，让用户再验证一次；
- **后台创建账号**、**注销账号**；
- **安全提醒**：登录方式变化时通知用户；
- **人机验证**和**短信费用**：防止脚本批量注册、刷验证码。

阅读前需要了解：[登录与注册](login.md)；[API 令牌](api-tokens.md)。

- [二次验证](#two-factor)
    - [强制二次验证](#requiring-two-factor)
- [Passkey](#passkeys)
- [按租户的登录策略](#policies)
- [确认身份](#confirming)
- [后台创建账号](#admin-accounts)
- [注销账号](#forget)
- [操作日志与安全提醒](#security-events)
- [人机验证](#captcha)
- [短信费用](#sms-cost)
- [常见问题](#troubleshooting)

<a id="two-factor"></a>
## 二次验证

账号可以启用 TOTP 二次验证：用户在 Google Authenticator、1Password 等验证器 App 中添加账号，登录时除了密码还要输入验证器上的 6 位数字。`Options.Issuer` 是验证器中显示的应用名称。

启用分两步。`SetupTOTP` 生成密钥，`setup.URI` 渲染成二维码让用户扫描，`setup.Secret` 供无法扫码时手动输入：

```go
setup, err := users.SetupTOTP(ctx, a.db, sub.Ref(), email)
```

用户输入验证器上的数字后，`ConfirmTOTP` 校验并启用，返回 8 个恢复码：

```go
codes, err := users.ConfirmTOTP(ctx, a.db, sub.Ref(), in.Code)
```

恢复码在手机丢失时代替验证器，每个只能用一次。它们只在这里和 `RegenerateRecoveryCodes` 返回明文，页面要提示用户保存下来；`RecoveryCodesLeft` 返回剩余个数，用完之前提醒用户重新生成。

启用后，`Password` 和验证码登录返回 `out.Pending`：第一步已经通过，等待二次验证。客户端带着它和验证器上的数字（或一个恢复码）调用登录的第二步：

```go
out, err := users.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
if err != nil {
    return nil, err
}
if out.Pending != "" {
    resp.Body.Pending = out.Pending // 客户端显示输入验证码的界面
    return resp, nil
}
// 未启用二次验证：直接签发 token
```

```go
out, err := users.TwoFactor(ctx, in.Body.Pending, in.Body.Code)
if err != nil {
    return nil, err // 验证码不正确：login.ErrInvalidCode
}
plain, err := tokens.Login(ctx, out.Subject, device)
```

`Pending` 15 分钟内有效，最多输错 5 次，之后要重新登录。每个验证码只能用一次；验证器与服务器的时钟允许相差 30 秒。`DisableTwoFactor` 关闭二次验证，之前先[确认身份](#confirming)。

<a id="requiring-two-factor"></a>
### 强制二次验证

运营后台通常要求员工都启用二次验证。在这套账号的登录策略中设置 `RequireTwoFactor`：

```go
staff := login.New(db, login.Options{
    // ……
    Policy: login.Policy{RequireTwoFactor: true},
})
```

之后没有经过二次验证的登录都不签发凭据：账号已经启用的，返回 `out.Pending`，第三方登录也是如此；还没有启用的，返回 `out.Enroll`，客户端先引导用户启用，启用后才登录成功：

```go
setup, err := staff.EnrollTOTP(ctx, in.Body.Enroll, email)          // 返回二维码
out, codes, err := staff.CompleteEnroll(ctx, in.Body.Enroll, in.Body.Code) // 启用并登录，codes 是恢复码
plain, err := tokens.Login(ctx, out.Subject, device)
```

`Enroll` 与 `Pending` 一样 15 分钟内有效，最多输错 5 次。passkey 登录本身就算二次验证，直接成功；`Register` 注册的新账号同样先启用。后台登录接口的完整骨架见[单账号后台](admin-example.md#auth-api)。

[按租户登录](login.md#identifiers)的账号体系可以按租户的设置决定，见[按租户的登录策略](#policies)。一套账号在多个租户中使用、只有部分租户要求二次验证时，登录时还不知道要进哪个租户，改由这些租户的 Resolver 检查：`!sub.Auth.TwoFactor()` 时返回 `identity.ErrTwoFactorRequired`，请求得到 `403 auth.two_factor_required`。客户端让用户输入验证器上的数字，`CheckTwoFactor` 校验后用 token 的 `Confirm` 补上这次的方式（见[确认身份](#confirming)），再重试；账号还没有启用二次验证的，先到账号设置中启用。

<a id="passkeys"></a>
## Passkey

Passkey（通行密钥，WebAuthn）用手机或电脑的指纹、面容、设备密码登录：私钥只留在设备中，与网站域名绑定，钓鱼网站拿不到。`login/passkey` 把每个 passkey 作为一个登录标识（`Kind` 为 `passkey`）加到 `Realm` 上，所以它与邮箱、第三方账号一样可以绑定、解除、按租户隔离；用 passkey 登录时跳过[二次验证](#two-factor)这一步，它本身就同时验证了设备和使用者。

发布 `passkey` 的迁移（`passkey.Migrations()`），在入口中为用户创建：

```go
passkeys, err := passkey.New(a.db, users, a.crypt, passkey.Config{
    RPID:    "example.com",
    RPName:  "Blog",
    Origins: []string{"https://example.com"},
})
```

`RPID` 是 passkey 绑定的域名，要长期固定：改了它，已经创建的 passkey 全部失效。`Origins` 是允许发起登录的网页地址；Android App 加上 `android:apk-key-hash:<签名证书的哈希>`，iOS App 通过关联域名使用网站的地址。

每个操作分两步：`Begin*` 返回交给浏览器或 App 的 `Options` 和一个 `Token`，客户端完成后把结果连同 `Token` 交给 `Finish*`。`Token` 是用应用密钥加密的这次操作的状态，5 分钟内有效，由客户端带回。

**登录**时浏览器直接列出这个网站的 passkey 让用户选择：

```go
c, err := passkeys.BeginLogin(ctx) // 返回 c 给客户端
```

```go
out, err := passkeys.FinishLogin(ctx, in.Body.Token, in.Body.Credential) // Credential 是 json.RawMessage
if err != nil {
    return nil, err // passkey.ErrInvalid
}
plain, err := tokens.Login(ctx, out.Subject, device)
```

浏览器中用 `navigator.credentials.get` 完成中间一步：

```js
const { options, token } = await api.post("/auth/passkey/begin")
const credential = await navigator.credentials.get({
  publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(options.publicKey),
})
await api.post("/auth/passkey/finish", { token, credential: credential.toJSON() })
```

**创建**也是两步，客户端用 `navigator.credentials.create` 和 `parseCreationOptionsFromJSON`。`FinishRegistration` 返回完成凭证：已登录的用户添加 passkey 时交给 `Link`，用 passkey 注册新账号时交给注册业务动作中的 `Register`，与[验证码注册](login.md#codes)相同：

```go
c, err := passkeys.BeginRegistration(ctx, sub.Ref(), email) // 注册新账号时 ref 传零值
proof, err := passkeys.FinishRegistration(ctx, in.Body.Token, in.Body.Credential, in.Body.Name)
err = users.Link(ctx, a.db, sub.Ref(), proof)
```

`email` 是系统在保存 passkey 时显示的账号名，`in.Body.Name` 是给这个 passkey 起的名称，例如“iPhone”。同一个账号在每个设备上只创建一个 passkey。

账号设置中，`List` 列出账号的 passkey（名称、是否在设备之间同步、最近使用时间），`Rename` 改名，`Remove` 移除；账号始终保留至少一种登录方式。签名计数倒退（疑似 passkey 被复制）时登录失败。创建后没有绑定到账号的 passkey，以及账号[注销](#forget)后留下的公钥，由 `passkeys.Cleanup` 定时删除。

<a id="policies"></a>
## 按租户的登录策略

`Options.Policy` 对整套账号体系生效。[按租户登录](login.md#identifiers)（`PerTenant`）的账号体系，各租户可以有不同的策略，例如 A 租户开放注册，B 租户只能用自己的企业 IdP 登录，C 租户强制二次验证。这时设置 `PolicyFor`，它按 context 中的租户返回策略，通常读取租户的[设置](../features/settings.md)：

```go
type LoginSettings struct {
    Register         bool     `json:"register"`
    RequireTwoFactor bool     `json:"require_two_factor"`
    Methods          []string `json:"methods"` // 例如 ["acme_sso"]；空表示不限制
}

loginSettings := settings.New(a.db, "login", LoginSettings{Register: true})

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

`Methods` 列出允许的登录方式，名称与 `sub.Auth.Methods` 相同：`password`、`email`、`phone`、第三方提供方的名称、`passkey`。不允许的方式在 `Password`、`SendCode`（`SignIn`）、`VerifyCode` 和 `External` 中返回 `403 login.method_not_allowed`，公开参数 `method` 是这种方式。找回密码、二次验证的第二步始终允许。

一套账号在多个租户中使用（不设置 `PerTenant`）时，登录时还不知道要进哪个租户，`PolicyFor` 拿不到租户。这时由租户的 Resolver 在进入租户时检查这次登录用的方式：

```go
if cfg.RequireSSO && !slices.Contains(sub.Auth.Methods, cfg.Connection) {
    return identity.Subject{}, identity.ErrMethodRequired.New(errorx.Public("methods", []string{cfg.Connection}))
}
if cfg.RequireTwoFactor && !sub.Auth.TwoFactor() {
    return identity.Subject{}, identity.ErrTwoFactorRequired.New()
}
```

请求得到 `403 auth.method_required`，公开参数 `methods` 列出可以用的方式，出现在响应的 `params` 中：

```json
{"title": "Forbidden", "status": 403, "detail": "请用企业账号登录", "code": "auth.method_required", "params": {"methods": ["acme_sso"]}}
```

客户端让用户用其中一种重新登录，例如用 `@go-tenon/client` 时读取 `describeError(err).params.methods`。企业 SSO 的完整写法见[企业单点登录](enterprise-sso.md)。

<a id="confirming"></a>
## 确认身份

修改邮箱、关闭二次验证、删除账号之前，再次确认操作的就是账号本人。按账号有的登录方式选一种：

| 方式 | 调用 |
| --- | --- |
| 密码 | `Confirm(ctx, ref, password)` |
| 验证码 | `SendConfirmCode(ctx, ref, id)` 发给账号自己的邮箱或手机号，`ConfirmCode(ctx, ref, requestID, code)` 校验 |
| 二次验证 | `CheckTwoFactor(ctx, ref, code)` 校验验证器上的数字或恢复码，返回用到的方式 |
| passkey、第三方账号 | 再登录一次，`out.Subject.Ref()` 等于 `ref` 即可 |

失败次数都有限制。`HasPassword` 决定显示密码框还是“发送验证码”。确认用的验证码只发给 `ref` 自己的标识，其他标识返回 `ErrNotFound`；它只用于确认身份，`VerifyCode` 拒绝它，所以二次验证始终有效。

最简单的写法是客户端在敏感请求中直接带上密码或验证码，处理器先确认、再执行。几个敏感操作连在一起时，每次都输入密码很烦，可以改为「确认一次，一段时间内放行」：

1. 敏感操作的路由加上 `rest.Confirmed(15*time.Minute)`。距离上次登录或确认超过 15 分钟时，请求返回 `403 auth.confirm_required`；
2. 客户端弹出确认框，调用应用的确认接口：校验密码或验证码，再用 token 的 `Confirm` 记下确认时间；
3. 客户端重试原请求。

```go
// POST /account/confirm
func (a *ConfirmAccount) Handle(ctx context.Context, sub identity.Subject, password string) error {
    if err := a.users.Confirm(ctx, sub.Ref(), password); err != nil {
        return err
    }
    return a.tokens.Confirm(ctx, sub, login.MethodPassword)
}
```

```go
op := huma.Operation{OperationID: "account-delete", Method: http.MethodDelete, Path: "/account"}
rest.RequireBearer(api, tokens.Validate)(&op)
rest.Confirmed(15 * time.Minute)(&op)
```

`Confirm` 的方式参数是这次确认用到的方式：密码为 `login.MethodPassword`，验证码为标识的类型（如 `login.KindPhone`），二次验证为 `CheckTwoFactor` 的返回值。[JWT 模式](jwt.md#jwt)用 `jwt.Confirm`，它返回新的 access token，客户端替换后重试。个人访问令牌和代为登录的 token 访问 `Confirmed` 的路由总是 `403`。

登录本身也算一次确认。登录得到的 `out.Subject.Auth` 记下了这次通过的验证方式（如 `password`、`password totp`、`email`、`passkey`、`google`）和时间，token 保存它们，之后每个请求的主体都带着，见[认证](authentication.md#key-concepts)。

<a id="admin-accounts"></a>
## 后台创建账号

运营后台的员工账号通常由管理员创建，不开放注册。管理员创建 `staff` 行后，用 `Attach` 直接写入邮箱，再发一个设置密码的验证码，也就是邀请：

```go
if err := a.staff.Attach(ctx, tx, member.Ref(), login.Email(in.Email), false); err != nil {
    return err
}
// 事务提交后发送
_, err := a.staff.SendCode(ctx, login.Reset, login.Email(in.Email))
```

员工通过邀请邮件中的链接设置密码，邮箱随之标为已验证。`Attach` 直接写入标识，未验证的标识同样占用唯一性，所以只用于管理员建号这类可信的场景；自助注册一律通过验证码。

<a id="forget"></a>
## 注销账号

`Forget` 删除账号的全部登录标识、密码、二次验证和验证码。在注销账号的事务中与删除账号行一起调用，再撤销 token：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    if _, err := tx.NewDelete().Model((*User)(nil)).Where("id = ?", sub.ID).Exec(ctx); err != nil {
        return err
    }
    return a.users.Forget(ctx, tx, sub.Ref())
})
```

注销后，这个邮箱可以重新注册。

<a id="security-events"></a>
## 操作日志与安全提醒

`login` 不在登录、修改密码这些时候回调应用：它们都由处理器调用，结果当场就知道。要记[操作日志](../features/activity.md)时，在同一个事务中调用 `activity.Record`：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    if err := a.users.SetPassword(ctx, tx, sub.Ref(), in.Body.Password); err != nil {
        return err
    }
    return activity.Record(ctx, tx, sub, activity.Activity{
        Event: "account.password_changed", TargetType: string(sub.Type), TargetID: sub.ID,
    })
})
```

启用或关闭二次验证、绑定或移除登录方式的写法相同。登录成功时用 `out.Subject` 作为操作者记录；登录失败没有可以确定的操作者，需要时写进日志。

「新设备登录」提醒在签发凭据之前查一下已有的登录。其他设备上有登录、但都不是这个 User-Agent 时提醒一次：

```go
known, err := h.tokens.List(ctx, out.Subject)
if err != nil {
    return nil, err
}
pair, err := h.jwt.Login(ctx, out.Subject, device)
if err != nil {
    return nil, err
}
if len(known) > 0 && !slices.ContainsFunc(known, func(t apitoken.Token) bool { return t.UserAgent == device.UserAgent }) {
    // 发邮件、短信或站内通知，带上时间和 IP，提醒用户如果不是本人，去设备列表退出这台设备
}
```

refresh token 被另一方使用时，框架撤销这次登录并调用 [`OnReuse`](jwt.md#jwt)，在那里提醒用户。

<a id="captcha"></a>
## 人机验证

`Sends` 按标识计数。机器人每次换一个邮箱或手机号，这个限制就不起作用；换用代理池，按 IP 的限流也挡不住。验证码用短信发送时，被批量刷一晚就是一笔短信费。开放自助注册时，在发送验证码的接口前加上人机验证。`captcha` 包在服务端校验 [Cloudflare Turnstile](https://developers.cloudflare.com/turnstile/) 的结果：

```yaml
captcha:
  secret: ${TURNSTILE_SECRET}
  hostnames: [example.com]
```

```go
bots, err := captcha.New(cfg.Captcha, nil)
```

前端用 site key 渲染 Turnstile 组件，`data-action` 设为 `signup`，把组件得到的 token 和邮箱一起提交。接口先校验 token，再发送验证码：

```go
type RequestCodeInput struct {
    Body struct {
        Email   string `json:"email" format:"email"`
        Captcha string `json:"captcha" doc:"Turnstile 组件得到的 token"`
    }
    ip string
}

// Resolve 记录客户端地址，见 Huma 的 Resolver。
func (in *RequestCodeInput) Resolve(ctx huma.Context) []error {
    in.ip, _, _ = net.SplitHostPort(ctx.RemoteAddr())
    return nil
}

func requestCode(users *login.Realm, bots *captcha.Verifier) func(context.Context, *RequestCodeInput) (*RequestCodeOutput, error) {
    return func(ctx context.Context, in *RequestCodeInput) (*RequestCodeOutput, error) {
        if err := bots.Verify(ctx, in.Body.Captcha, "signup", in.ip); err != nil {
            return nil, err
        }
        requestID, err := users.SendCode(ctx, login.SignIn, login.Email(in.Body.Email))
        // ……
    }
}
```

没有通过时返回 `captcha.ErrFailed`（`422`），前端重置组件，让用户重新验证。每个 token 只能校验一次，重复提交同一个 token 也会得到这个错误。Turnstile 连不上时返回 `captcha.ErrUnavailable`（`503`），请求被拒绝。

`hostnames` 和业务动作保证 token 来自自己的站点和这张表单：在注册页得到的 token 只能用于注册。评论、联系表单等其他公开表单的做法相同，只是各用自己的业务动作。密码登录已经按账号限流，可以省掉人机验证。

使用 hCaptcha 时把 `endpoint` 设为 `https://api.hcaptcha.com/siteverify`，业务动作传空字符串，因为 hCaptcha 不返回它。本地开发可以使用 Cloudflare 提供的[测试密钥](https://developers.cloudflare.com/turnstile/troubleshooting/testing/)。测试中把 [`outboundtest`](../libraries/outbound.md#testing) 的客户端传给 `captcha.New`，按需登记 `captcha.Turnstile` 地址的响应：

```go
fake := outboundtest.New(t)
fake.On("POST " + captcha.Turnstile).Reply(outboundtest.JSON(200, map[string]any{
    "success": true, "hostname": "example.com", "action": "signup",
}))
bots, err := captcha.New(captcha.Config{Secret: "test"}, fake.Client())
```

<a id="sms-cost"></a>
## 短信费用

`Sends` 和人机验证之外，验证码用短信发送时再给费用设一个上限：在 `Send` 中先过一个全局限流器，超出后停止发送。`Send` 返回的错误由 `SendCode` 原样返回，这次的验证记录同时删除：

```go
smsBudget := redislimit.New(rdb, "app:sms:budget", ratelimit.Config{Rate: 5000, Interval: 24 * time.Hour, Burst: 5000})

readers := login.New(db, login.Options{
    // ……
    Send: func(ctx context.Context, code login.Code) error {
        if err := smsBudget.Allow(ctx, "all"); err != nil {
            return err // 当天的额度用完：返回 429，同时应当告警
        }
        return texts.Send(ctx, sms.Message{To: code.To.Value, Template: "login_code", Params: map[string]string{"code": code.Code}})
    },
})
```

`texts` 是 [`sms.Sender`](../libraries/sms.md)。服务商对同一号码也有频率限制，触发时返回 `sms.ErrLimited`，同样以 `429` 返回给客户端。

国际短信更贵，也是刷量的重点。只服务部分国家和地区时，发送前检查手机号的国家码，不在范围内的直接拒绝。App 不方便嵌入人机验证组件时，可以用 Apple App Attest 或 Google Play Integrity 证明请求来自自己的 App。

<a id="troubleshooting"></a>
## 常见问题

**输入了验证器上的数字，二次验证仍然失败**

验证器与服务器的时钟最多允许相差 30 秒，检查手机和服务器的时间；每个验证码只能用一次，同一个数字不能重复提交。

**二次验证提示需要重新登录**

`Pending` 15 分钟内有效，最多输错 5 次，过期或错太多次后要重新登录，见[二次验证](#two-factor)。

**修改了域名后，所有 passkey 都不能用了**

passkey 绑定在 `RPID` 上，改了它，已经创建的 passkey 全部失效。`RPID` 要长期固定，见 [Passkey](#passkeys)。

**敏感操作返回 `403 auth.confirm_required`**

距离上次登录或确认超过了路由设置的时间。客户端弹出确认框，调用确认接口后重试原请求，见[确认身份](#confirming)。

**人机验证返回 `captcha.ErrFailed`（422），用户明明通过了**

每个 token 只能校验一次，重复提交同一个 token 也会失败。前端在每次提交失败后重置组件，见[人机验证](#captcha)。
