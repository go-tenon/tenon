# 认证进阶

[认证](authentication.md)一页介绍了主体、Token 校验器和怎样保护路由，一个应用只有一套账号时这些就够了。这一页介绍更复杂的情况：

- **多套账号体系**：例如作者和后台运营人员是两套独立的账号，登录接口、权限都不同；
- **多租户**：同一个用户属于多个组织，请求要说明在哪个组织中操作；
- **代为登录**：客服以某个用户的身份查看问题，同时留下记录。

阅读前需要了解：[认证](authentication.md)；[授权](authorization.md)。完整的例子见[运营后台与多租户](saas-example.md)。

- [多套账号体系](#account-systems)
- [多租户](#tenants)
- [代为登录](#impersonation)
- [常见问题](#troubleshooting)

<a id="account-systems"></a>
## 多套账号体系

运营人员 + 租户成员的完整接线见[运营后台与多租户](saas-example.md)。

框架不替你定义用户表。一个产品里完全可以有几套互不相干的账号——平台运营、商家员工、终端客户——各自一张表，主键和登录方式都由产品自己定。框架只需要知道：这个主体属于哪一套，也就是它的 `Type`。

**1. 每套账号声明一个类型**，放在拥有那张表的模块里：

```go
// internal/staff/staff.go
const Type identity.SubjectType = "staff"

// internal/member/member.go
const Type identity.SubjectType = "member"

// internal/customer/customer.go
const Type identity.SubjectType = "customer"
```

登录时用它构造主体，例如 `identity.Subject{Type: customer.Type, ID: c.ID}`。

**2. 每个入口声明接受哪几套账号。** 运营后台只收运营人员，商家后台只收商家员工，App / 小程序只收客户：

```go
router.Route("/ops/api", func(r chi.Router) {
    ops := rest.New(r, rest.Options{BasePath: "/ops/api", Accounts: identity.Accounts{staff.Type: roles.Resolve}})
    // 注册运营后台的接口
})
router.Route("/console/api", func(r chi.Router) {
    console := rest.New(r, rest.Options{BasePath: "/console/api", Accounts: identity.Accounts{member.Type: roles.Resolve}, Tenant: fromPath})
    // 注册商家后台的接口
})
router.Route("/api", func(r chi.Router) {
    shop := rest.New(r, rest.Options{BasePath: "/api", Accounts: identity.Accounts{customer.Type: customers.Resolve}, Tenant: fromHost})
    // 注册 App 和小程序的接口
})
```

几个 API 可以共用同一个 token 校验器：客户的 token 打到商家后台，会像无效 token 一样得到 `401`。一个入口也可以同时接受多套账号，例如 `identity.Accounts{member.Type: roles.Resolve, customer.Type: customers.Resolve}`；`sub.Can` 和业务动作对它们一视同仁。[`authz`](authorization.md#roles) 按主体的类型和 ID 存角色，可以同时服务多套账号。终端客户往往不需要角色：Resolver 检查账号是否停用，再按「数据归属本人」授权即可。

**3. 每套账号有自己的登录方式和令牌有效期。** 每套账号用一个 [`login.Realm`](login.md) 管理登录标识和密码——比如运营人员只能由管理员创建、必须用密码；客户可以用手机验证码自助注册。令牌用各自的配置创建 `apitoken.Store` 签发，见[装配](api-tokens.md#assembling)。

**4. ID 只需在同一套账号中唯一。** 令牌、角色、[操作日志](../features/activity.md)、[通知](../features/notifications.md)、[功能开关](../features/flags.md)和实时频道的在线成员，都会同时保存主体的类型和 ID，所以员工表和客户表都用自增 ID 也能分清。主键是 `(tenant_id, id)` 这类组合时，把租户编进 ID，例如 `t42:1001`。新表推荐 UUIDv7。需要把主体塞进一个字符串时（缓存键、广播主题），用 `sub.Ref().String()`，结果形如 `customer:42`。

[代为登录](#impersonation)也可以跨账号体系：运营人员代客户登录时，主体是客户，`Impersonator` 是运营人员的 `identity.Ref`。

<a id="tenants"></a>
## 多租户

SaaS 里很常见：同一个人属于多个组织，在每个组织里权限不同。本次请求属于哪个租户，由请求本身决定——路径里的 `/t/{tenant}`、子域名、或请求头。用 `rest.Options.Tenant` 把它取出来：

```go
api := rest.New(router, rest.Options{
    Tenant:   func(ctx huma.Context) string { return ctx.Param("tenant") },
    Accounts: identity.Accounts{user.Type: resolve},
})
```

`Tenant` 的结果在每个操作开始时写入 context，公开操作也不例外；用 `identity.TenantFromContext` 读取。没设 `Tenant`、或取到空字符串时，租户是 `""`，表示平台本身（个人中心、运营后台这类）。

Resolver 据此加载用户在该租户里的权限，并把租户写进主体：

```go
func resolve(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
    tenant := identity.TenantFromContext(ctx)
    perms, err := members.Permissions(ctx, tenant, sub.Ref())
    if err != nil {
        return identity.Subject{}, err // 不是成员时返回 errorx.NotFound 类错误，得到 404
    }
    sub.Tenant, sub.Permissions = tenant, perms
    return sub, nil
}
```

凭据也可以绑死一个租户，例如「只属于某个租户的个人访问令牌」：校验器返回的主体 `Tenant` 不为空时，只要和本次请求的租户不一致，就会在 Resolver 之前直接 `403`。

角色和成员可以交给 [`authz`](authorization.md#roles) 管理，它的 `Resolve` 方法就是上面这类函数。租户怎么定、成员怎么管、数据怎么隔离、运营后台怎么做，见[多租户](tenancy.md)。

<a id="impersonation"></a>
## 代为登录

客服排查问题时，经常需要「以用户的身份看一眼」。代为登录拿到的主体就是那个用户，权限也是他的；真正动手的人记在 `Impersonator` 里。`Impersonator` 会和其他字段一起存在令牌里，之后每个请求都会带着它。

做法很直接：签发一个带 `Impersonator` 的[登录令牌](api-tokens.md#login-and-logout)。运营后台的「以此用户登录」调用一个要求 `users:impersonate` 的接口，为目标用户签发令牌，交给用户端前端使用；排查完再[撤销](api-tokens.md#revoking-tokens)。运营人员和用户属于不同[账号体系](#account-systems)时也一样——主体是用户，`Impersonator` 是运营人员：

```go
staff, _ := identity.FromContext(ctx)
if !staff.Impersonator.IsZero() {
    return nil, identity.ErrForbidden.New() // 先回到自己的账号，代为登录只有一层
}
target := identity.Subject{Type: customer.Type, ID: in.CustomerID, Impersonator: staff.Ref()}
plain, err := tokens.Login(ctx, target, device)
```

用户端前端在 `sub.Impersonator` 不为零值时，显示「正在代 {用户} 操作」和退出按钮；退出时撤销这个令牌，回到运营后台。

令牌放请求头或请求体里传；不要塞进跳转地址的查询参数——那样会留在浏览器历史和访问日志里。

几件需要应用自己拿主意的事：

- **操作者仍然有效**：在 Resolver 里检查 `Impersonator` 是否还拥有代为登录权限。客服离职或权限被收回后，他代登的令牌在下一个请求就该失效：

  ```go
  if !sub.Impersonator.IsZero() {
      perms, _, err := roles.Permissions(ctx, sub.Impersonator, "")
      if err != nil {
          return identity.Subject{}, err
      }
      if !identity.Grants(perms, "users:impersonate") {
          return identity.Subject{}, identity.ErrUnauthenticated.New()
      }
  }
  return roles.Resolve(ctx, sub)
  ```

- **谁可以被代为登录**：通常不允许代登其他运营人员；在 `Impersonate` 里用 `roles.Permissions(ctx, target.Ref(), "")` 确认目标在平台中没有角色
- **哪些操作不允许代为执行**：改密码和邮箱、管理设备和令牌、付款等，在对应业务动作里拒绝 `!subject.Impersonator.IsZero()` 的主体。代登期间创建的[个人访问令牌](api-tokens.md#personal-access-tokens)会记下 `Impersonator`，之后每次使用都归到这个操作者。[OAuth 授权](oauth-server.md)的同意接口由框架注册，已经会拒绝代登主体
- **审计**：[操作日志](../features/activity.md)会自动记下 `Impersonator`；自己写日志时也要同时写下两者，例如 `slog.InfoContext(ctx, "article published", "subject", subject.Ref().String(), "impersonator", subject.Impersonator.String())`

<a id="troubleshooting"></a>
## 常见问题

**客户的 token 调用运营后台的接口，得到 401**

每个入口在 `Accounts` 中声明接受哪几套账号，不接受的账号的 token 会像无效 token 一样被拒绝。这是预期的行为，见[多套账号体系](#account-systems)。

**带着某个租户的个人访问令牌请求另一个租户，得到 403**

凭据绑定了租户时，只要和本次请求的租户不一致，就在 Resolver 之前直接拒绝，见[多租户](#tenants)。

**客服离职后，他代登的令牌仍然能用**

在 Resolver 中检查 `Impersonator` 是否还拥有代为登录的权限，见[代为登录](#impersonation)。
