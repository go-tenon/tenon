# 认证与授权

[文档首页](README.md) / 安全

认证回答“调用者是谁”，授权回答“这个调用者能否执行当前操作”。mygoravel 用 `identity.Subject` 传递已验证身份，用端点策略和业务检查分别保护入口与资源。

本章内容：

- [提供凭据校验器](#validator)
- [保护路由](#protect-routes)
- [读取主体](#subject)
- [业务层授权](#authorization)
- [管理凭据来源](#credentials)

<a id="validator"></a>

## 提供凭据校验器

`rest.TokenValidator` 接收 Huma context 与 bearer token，返回主体或错误。下面是完整的演示实现：

```go
package auth

import (
    "crypto/subtle"

    "github.com/danielgtaylor/huma/v2"
    "github.com/ryan053857/mygoravel/config"
    "github.com/ryan053857/mygoravel/fault"
    "github.com/ryan053857/mygoravel/httpx/rest"
    "github.com/ryan053857/mygoravel/identity"
)

var ErrUnauthenticated = fault.Define(fault.Unauthenticated, "auth.unauthenticated")

func Validator(expected config.Secret) rest.TokenValidator {
    return func(ctx huma.Context, token string) (identity.Subject, error) {
        if expected.Reveal() == "" || subtle.ConstantTimeCompare(
            []byte(token), []byte(expected.Reveal()),
        ) != 1 {
            return identity.Subject{}, ErrUnauthenticated.New()
        }
        return identity.Subject{
            ID: "alice", Type: identity.SubjectUser,
            Method: "bearer", Scopes: []string{"notes"},
        }, nil
    }
}
```

这个函数只演示静态凭据。实际应用可以查询凭据存储或使用经过验证的 token 库；返回 Subject 之前必须完成相应的有效性检查。校验器负责签名、期限和权限来源，Subject 保存验证结果。

<a id="protect-routes"></a>

## 保护路由

构造 API 后，将校验器和 scope 要求绑定到 Operation：

```go
requireNotes := rest.RequireBearer(api, validateToken, "notes")
op := huma.Operation{
    OperationID: "note-list",
    Method:      http.MethodGet,
    Path:        "/notes",
}
requireNotes(&op)
```

然后使用 `rest.Register` 注册处理器。所有传入的 scope 都必须满足。HTTP bearer 的 OpenAPI scope 数组为空；所需业务 scopes 记录在 `x-required-scopes` 扩展中。

缺少凭据、凭据无效或返回空主体 ID 时是 401；缺少 scope 时是 403。401 带有 `WWW-Authenticate: Bearer`。校验后端返回 Unavailable 等内部错误时按 5xx 处理。

公开接口调用 `rest.Public()(&op)`。漏掉访问策略会在登记时失败。

<a id="subject"></a>

## 读取主体

认证成功后，处理器从 context 取得主体：

```go
subject, ok := identity.FromContext(ctx)
if !ok {
    return nil, ErrUnauthenticated.New()
}
return service.List(ctx, subject, params)
```

这是处理器中的片段；`service` 和 `params` 是应用自己的业务服务与参数。Subject 的 ID 是不透明字符串，Type 可表示用户或服务，Method 记录认证方式，Scopes 保存已验证的访问范围。

<a id="authorization"></a>

## 业务层授权

端点验证 `notes` scope 后，业务服务继续检查资源所有权：

```go
if note.OwnerID != subject.ID {
    return nil, ErrForbidden.New()
}
```

`ErrForbidden` 是 PermissionDenied 分类的业务模板。这项检查也适用于缓存命中；写操作应在实际读写资源的事务中完成检查。业务入口还应拒绝空主体 ID，确保直接调用服务时也会执行身份检查。

<a id="credentials"></a>

## 管理凭据来源

凭据来源由应用决定。使用静态配置时，可以定义自己的结构：

```go
type Credential struct {
    Token     config.Secret `yaml:"token"`
    SubjectID string        `yaml:"subject_id"`
}

type AuthConfig struct {
    Credentials []Credential `yaml:"credentials"`
}
```

解码后检查空 token、空主体和重复凭据，再把已校验值传给校验器。配置文件中的环境变量通过 config.WithLookup 显式读取。

账户、token 和凭据存储由应用定义。用户注册、会话、刷新 token 等能力使用所选认证组件，其验证结果转换为 Subject。

---

[上一篇：日志](logging.md) · [下一篇：数据库](database.md)
