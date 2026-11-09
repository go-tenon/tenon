# 授权

- [简介](#introduction)
- [路由级授权：Scope](#scopes)
    - [在代码中检查 Scope](#checking-scopes)
- [资源级授权：所有权](#ownership)
    - [编写授权函数](#authorization-functions)
    - [在事务中检查](#checking-in-transactions)
    - [缓存与授权](#caching-and-authorization)
- [授权失败的响应](#authorization-failures)

<a id="introduction"></a>
## 简介

[认证](authentication.md)确认了调用者是谁，接下来还需要回答：这个调用者**能不能**执行当前的操作？这就是授权。

在 mygoravel 中，授权分为两个层次：

- **路由级授权**：调用者是否具备访问这一类接口的权限，例如“可以访问笔记接口”。它通过 scope 声明在路由上，由框架在调用处理器之前检查；
- **资源级授权**：调用者能否操作某一个具体的资源，例如“只能查看自己的笔记”。它依赖具体的数据，由业务服务检查。

两个层次缺一不可：路由级授权挡住了明显不该访问的请求，资源级授权则保证每一条数据都只被正确的人访问。

<a id="scopes"></a>
## 路由级授权：Scope

Scope 是一个简单的字符串，表示一类访问权限。在 `rest.RequireBearer` 中列出路由需要的 scope：

```go
requireNotes := rest.RequireBearer(api, validateToken, "notes")
requireNotes(&op)
```

校验器返回的主体必须在 `Scopes` 中包含 `notes`，否则请求会以 `403` 被拒绝。传入多个 scope 时，调用者必须全部具备。

Scope 的命名由你决定。常见的做法是按资源和操作划分，例如 `notes:read`、`notes:write`：

```go
requireRead := rest.RequireBearer(api, validateToken, "notes:read")
requireWrite := rest.RequireBearer(api, validateToken, "notes:write")
```

<a id="checking-scopes"></a>
### 在代码中检查 Scope

有时同一个接口需要根据权限返回不同的内容，这时可以在处理器或服务中使用 `Subject.HasScope`：

```go
if subject.HasScope("notes:admin") {
    // 管理员可以看到所有用户的笔记
}
```

<a id="ownership"></a>
## 资源级授权：所有权

具备 `notes` scope 只说明调用者可以使用笔记接口，并不意味着他可以读取任意一条笔记。业务服务需要检查资源是否属于当前的主体：

```go
var ErrForbidden = errorx.Define(errorx.PermissionDenied, "note.forbidden")

func (s *Service) Get(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Note, error) {
    if subject.ID == "" {
        return nil, ErrForbidden.New()
    }
    note, err := s.find(ctx, id)
    if err != nil {
        return nil, err
    }
    if note.OwnerID != subject.ID {
        return nil, ErrForbidden.New()
    }
    return note, nil
}
```

请注意，主体是作为参数**显式传入**业务服务的，而不是在服务内部从 context 中读取。这样，服务的权限要求一目了然，在测试中也很容易构造不同身份的调用者。

服务在入口处拒绝空的主体 ID。这保证了即使服务被命令行或其他代码直接调用，也不会跳过身份检查。

> [!TIP]
> 对于不属于当前用户的资源，返回 `404` 而不是 `403` 也是一种常见做法，它可以避免泄露“这个资源存在”的信息。你可以根据业务需要选择 `errorx.NotFound` 或 `errorx.PermissionDenied`。

<a id="authorization-functions"></a>
### 编写授权函数

当授权规则变得复杂时，可以把它们提取为独立的函数，集中放在业务包中，类似于其他框架中的“策略（Policy）”：

```go
func canEdit(subject identity.Subject, note *Note) bool {
    if subject.HasScope("notes:admin") {
        return true
    }
    return note.OwnerID == subject.ID && !note.Archived
}
```

这些函数只是普通的 Go 函数，不需要注册到任何地方，单元测试也非常简单。

<a id="checking-in-transactions"></a>
### 在事务中检查

对于写操作，请在实际读写数据的**同一个事务**中完成所有权检查。如果先在事务外检查、再在事务内修改，两步之间资源可能已经被其他请求改变：

```go
err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
    var note Note
    if err := tx.NewSelect().Model(&note).Where("id = ?", id).For("UPDATE").Scan(ctx); err != nil {
        return err
    }
    if !canEdit(subject, &note) {
        return ErrForbidden.New()
    }
    _, err := tx.NewUpdate().Model(&note).Set("title = ?", title).WherePK().Exec(ctx)
    return err
})
```

<a id="caching-and-authorization"></a>
### 缓存与授权

如果你缓存了资源，从缓存中读取到数据后同样要执行所有权检查，详见[缓存](cache.md#cache-aside)。

<a id="authorization-failures"></a>
## 授权失败的响应

- 缺少路由所需的 scope 时，框架返回 `403`，错误码为 `auth.forbidden`；
- 业务服务返回 `errorx.PermissionDenied` 分类的错误时，返回 `403`，错误码为你定义的业务错误码，例如 `note.forbidden`。

两者都使用统一的[错误响应格式](errors.md#response-format)。
