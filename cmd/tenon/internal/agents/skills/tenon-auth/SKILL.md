---
name: tenon-auth
description: Tenon 应用中的认证与授权：用 rest.RequireToken 保护路由并要求权限、在业务动作中取出调用者并检查资源归属、权限的命名与目录、用 authz 把角色保存在数据库中，以及登录、API 令牌。给接口加权限、写“只有作者能修改”这类规则、加登录或角色时使用。
---

# 认证与授权

认证回答“这个请求是谁发的”，授权回答“他能不能做这件事”。先读 `.tenon/docs/security/authentication.md` 和 `.tenon/docs/security/authorization.md`。

## 步骤

1. **保护路由**：`rest.RequireToken(api, tokens.Validate, "articles:write")(&op)`。没有有效令牌返回 401，缺少权限返回 403，OpenAPI 文档同时标记认证和权限。见 `.tenon/docs/security/authentication.md#protecting-routes`、`.tenon/docs/security/authorization.md#route-permissions`。
2. **取出调用者**：处理器中 `subject, _ := identity.FromContext(ctx)`，作为参数传给 `Handle`，见 `.tenon/docs/security/authentication.md#retrieving-the-subject`。
3. **资源归属写在业务规则中**：例如只有作者或管理员能修改，`if !subject.Can("articles:admin") && item.AuthorID != subject.ID { return ErrForbidden.New() }`。页面也要用的规则写成导出的授权函数（`CanEdit`），见 `.tenon/docs/security/authorization.md#ownership`、`.tenon/docs/security/authorization.md#authorization-functions`。
4. **查询按调用者限定范围**：列表只返回调用者能看到的数据，见 `.tenon/docs/security/authorization.md#scoping-queries`。
5. **权限名**：“资源:操作”，例如 `articles:read`、`articles:write`；`articles:*` 包含所有文章权限。把权限目录交给 `rest.Options.Permissions`，前端拿到每个权限的说明，见 `.tenon/docs/security/authorization.md#permission-catalog`。
6. **角色**：需要管理员在后台分配角色时，用 `authz` 把角色和权限保存在数据库中，见 `.tenon/docs/security/authorization.md#roles`。

## 登录与令牌

- 令牌校验函数和令牌存储见 `.tenon/docs/security/authentication.md#token-validators`、`.tenon/docs/security/api-tokens.md`。
- 浏览器中的网页用 HttpOnly 的会话 cookie 携带 token，App、桌面端和其他服务用 `Authorization: Bearer`，服务端校验的是同一个 token，见 `.tenon/docs/security/api-tokens.md#browser-sessions`。
- 邮箱和密码登录、验证码、找回密码见 `.tenon/docs/security/login.md`；二次验证和 Passkey 见 `.tenon/docs/security/login-security.md`。
- 给程序和第三方用的个人访问令牌见 `.tenon/docs/security/api-tokens.md#personal-access-tokens`。

## 要点

- 每条路由都要声明访问策略，忘了声明时注册直接 panic；公开的路由显式写 `rest.Public()`。
- 授权检查放在 `Handle` 中，不只放在前端：前端按权限隐藏按钮只是展示，任何客户端都能直接调用接口。
- 在事务中检查归属时先 `For("UPDATE")` 锁住行，见 `.tenon/docs/security/authorization.md#checking-in-transactions`。
- 多租户见 `.tenon/docs/security/tenancy.md`，完整的例子见 `.tenon/docs/security/admin-example.md`、`.tenon/docs/security/saas-example.md`。
