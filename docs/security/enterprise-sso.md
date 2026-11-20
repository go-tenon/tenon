# 企业单点登录

B2B 产品中，租户的员工用公司自己的 IdP（Okta、Microsoft Entra ID、Google Workspace）通过 OpenID Connect 登录：租户在后台填写 OIDC 应用并验证邮箱域名，员工输入公司邮箱后跳到 IdP，第一次登录自动开通账号，IdP 的分组同步为角色，租户可以要求成员只用 SSO 登录。

包：`login/oauth`、`login`、`authz`、`outbound`。

框架提供 OIDC 流程（[`login/oauth`](social-login.md)）、登录标识和[按租户的登录策略](login.md#policies)（`login.Realm`）、角色（[`authz`](authorization.md)）。连接配置、域名验证和登录页由应用编写。本页的成员账号按租户登录（`PerTenant: true`），租户取自子域名；单页应用的接口写法同[第三方登录](social-login.md#spa-login)。

<a id="connections"></a>
## 连接与域名的表

每个租户的 SSO 配置是应用自己的两张表。第三方账号在 `login` 中的提供方名称（登录标识的 `Kind`）统一用 `sso`，标识按租户隔离。

```sql
CREATE TABLE sso_connections (
    tenant text PRIMARY KEY,                 -- 每个租户一个连接
    issuer text NOT NULL,                    -- 例如 https://acme.okta.com
    client_id text NOT NULL,
    client_secret text NOT NULL,             -- crypt.Encrypt 加密后保存
    require_sso boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL
);

CREATE TABLE sso_domains (
    domain text PRIMARY KEY,                 -- 例如 acme.com；一个域名只属于一个租户
    tenant text NOT NULL,
    token text NOT NULL,                     -- 写进 DNS TXT 记录的随机值
    verified_at timestamptz
);
```

<a id="verifying-domains"></a>
## 验证邮箱域名

已验证的域名决定输入哪个邮箱时跳到这个租户的 IdP，以及能否把 IdP 断言的邮箱关联到已有账号。租户在 DNS 中加一条 TXT 记录，业务动作检查它（`a.find` 读出租户的这个域名，`a.db` 是 `*bun.DB`）：

```go
func (a *VerifyDomain) Handle(ctx context.Context, sub identity.Subject, domain string) error {
    d, err := a.find(ctx, sub.Tenant, domain)
    if err != nil {
        return err
    }
    records, err := net.DefaultResolver.LookupTXT(ctx, "_app-verify."+d.Domain)
    if err != nil || !slices.Contains(records, "app-verify="+d.Token) {
        return ErrDomainNotVerified.New()
    }
    _, err = a.db.NewUpdate().Model(d).Set("verified_at = ?", time.Now().UTC()).WherePK().Exec(ctx)
    return err
}
```

> [!WARNING]
> 不验证域名，一个租户就能声称拥有 `gmail.com`，借此接管别人的账号。`gmail.com`、`outlook.com` 这类公共邮箱域名在添加时就拒绝。

<a id="providers"></a>
## 运行时创建提供方

连接由租户在运行时修改，提供方在使用时按需用 `oauth.Discover` 创建。按 `租户@updated_at` 缓存在进程中（`sync.Map`，用 `singleflight` 合并并发的创建）：配置改了键随之改变，所以修改连接时必须更新 `updated_at`；连接每次从数据库读取，多个实例之间始终一致。`c` 是租户在 `sso_connections` 中的行，`p.crypt` 是 `*crypt.Crypt`：

```go
// create 按租户的连接创建提供方，结果由调用方缓存。
func (p *Providers) create(ctx context.Context, c Connection) (*oauth.Provider, error) {
    secret, err := p.crypt.Decrypt(c.ClientSecret)
    if err != nil {
        return nil, err
    }
    return oauth.Discover(ctx, oauth.Config{
        Provider: "sso", Issuer: c.Issuer, ClientID: c.ClientID, ClientSecret: secret,
        RedirectURL: "https://" + c.Tenant + ".app.example.com/auth/sso/callback",
        Scopes:      []string{"openid", "email", "profile", "groups"},
        Client:      p.client, // outbound.New(outbound.Options{Timeout: 10 * time.Second})
    })
}
```

> [!WARNING]
> `issuer` 由租户填写，发现文档中的地址同样不可信，所以 `Client` 必须是 [`outbound`](../libraries/outbound.md) 的客户端，它拒绝访问内网地址。

<a id="signing-in"></a>
## 登录：`TrustEmail`、`TwoFactor`

1. 登录页先让用户输入邮箱，调用 `POST /auth/sso/start`：按域名在 `sso_domains` 中找到已验证的租户，`Start` 返回跳转地址和 `flow`；域名没有 SSO 时返回空，登录页显示密码框；
2. IdP 跳回单页应用的回调页，回调页把查询参数和 `flow` 交给 `POST /auth/sso/finish`。

`finish` 中 `ext` 是 `provider.Finish` 的结果（写法同[第三方登录](social-login.md#spa-login)），`h.members` 是成员的 `*login.Realm`：

```go
// 租户自己的 IdP：它管理的域名下的邮箱可信，它做过的多因素认证也可信。
ext.TrustEmail = h.domains.Verified(ctx, tenant, ext.Email)
ext.TwoFactor = idpMFA(ext.Claims)

out, err := h.members.External(ctx, ext)
if err != nil {
    return nil, err
}
if out.Proof != "" { // 第一次登录：直接开通账号
    if out, err = h.signUp.Handle(ctx, out.Proof, ext.Name); err != nil {
        return nil, err
    }
}
if out.Subject.ID != "" {
    if err := h.syncRoles(ctx, out.Subject.Ref(), tenant, ext.Claims); err != nil {
        return nil, err
    }
}
return h.session(ctx, out, in.UserAgent) // 签发 token，或者返回 pending、enroll

// idpMFA 按 ID token 的 amr 判断 IdP 是否做过多因素认证，各家写法不同，按实际的 IdP 调整。
func idpMFA(claims map[string]any) bool {
    amr, _ := claims["amr"].([]any)
    return slices.ContainsFunc(amr, func(v any) bool { return v == "mfa" || v == "otp" || v == "hwk" })
}
```

- **`TrustEmail`**：邮箱在这个租户中已经有账号时（公司改用 SSO 之前员工用密码注册过），`External` 把 SSO 账号绑定给它并登录。只在 `ext.Email` 的域名属于这个租户并且已验证时设置。
- **自动开通**：`signUp` 是应用的注册业务动作，在事务中创建成员行并调用 `Register`，提交之后调用 `CompleteRegister`，返回 `login.Outcome`，同[验证码注册](login.md#codes)。
- **`TwoFactor`**：这次登录记为 `[sso, idp_mfa]`，满足租户的二次验证要求。

<a id="syncing-roles"></a>
## 同步角色

IdP 的分组在 ID token 的 `groups` 中（Okta、Entra ID 要在 IdP 中配置这个声明）。每次登录按租户配置的映射同步一次，`h.roles` 是 `*authz.Store`：

```go
func (h *handlers) syncRoles(ctx context.Context, ref identity.Ref, tenant string, claims map[string]any) error {
    groups, _ := claims["groups"].([]any)
    mapping, err := h.groupRoles(ctx, tenant) // 租户配置的 分组 → 角色 ID
    if err != nil {
        return err
    }
    for group, role := range mapping {
        if slices.Contains(groups, any(group)) {
            err = h.roles.Assign(ctx, ref, tenant, role)
        } else {
            err = h.roles.Unassign(ctx, ref, tenant, role)
        }
        if err != nil {
            return err
        }
    }
    return nil
}
```

- 只同步映射中的角色，管理员手动分配的其他角色保持原样。
- 成员在 IdP 中被停用后，下次登录之前仍然持有已签发的 token。需要立即生效时，在应用中停用成员并[撤销令牌](api-tokens.md#revoking-tokens)。

<a id="requiring-sso"></a>
## 要求用 SSO 登录

租户开启 `require_sso` 后，在 `login.Options.PolicyFor` 中返回 `Methods`（`connections.Get` 读出租户的连接）：

```go
PolicyFor: func(ctx context.Context) (login.Policy, error) {
    c, err := connections.Get(ctx, identity.TenantFromContext(ctx))
    if err != nil {
        return login.Policy{}, err
    }
    p := login.Policy{RequireTwoFactor: c.RequireTwoFactor}
    if c.RequireSSO {
        p.Methods = []string{"sso"}
    }
    return p, nil
},
```

之后 `Password`、`SendCode` 在这个租户中返回 `403 login.method_not_allowed`，登录页据此只显示 SSO 按钮。账号跨租户通用、登录时不知道租户的产品，改由租户的 Resolver 检查 `sub.Auth.Methods`，返回 `identity.ErrMethodRequired`，见[按租户的登录策略](login.md#policies)。

<a id="not-supported"></a>
## 不支持的

- **SAML**：Okta、Entra ID、Google Workspace、Ping 都支持 OpenID Connect。
- **SCIM**：账号在登录时同步，停用由应用中的停用和撤销令牌完成。
- **IdP 发起的登录**：在 IdP 的应用列表中点图标时，IdP 跳到应用的登录地址，应用照常发起上面的流程。
