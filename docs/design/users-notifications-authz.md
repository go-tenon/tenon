# 设计：用户、通知、授权、角色

目标：像 Laravel 一样，go-kit 内置这些业务概念与流程，具体的东西（用户有哪些字段、注册填什么、
通知说什么、谁能做什么）由产品实现。`examples/` 提供默认实现，新产品复制后归产品所有。

原则：

- 只在产品之间确实不同的地方留给产品；密码哈希、token 轮换、两步验证、限流、队列这些各产品一样的，仍由 go-kit 实现。
- 契约尽量用“数据约定 + 函数”表达，少用接口，不用泛型把类型参数扩散到产品各处。
- 装配仍然在产品的 Setup 中显式完成，没有服务容器与自动发现。

| 概念 | Laravel 对应 | go-kit 中的位置 |
|---|---|---|
| 用户 | Authenticatable、users 表在应用中、Fortify Actions | `modules/account`（流程）+ 产品的 users 表与 User 模型 |
| 通知 | Notification 类、Notifiable、Channel | `modules/notification`（分发、渠道、站内信）+ 产品的通知类型 |
| 授权 | Gate、Policy | 基础设施 `authz` |
| 角色 | spatie/laravel-permission | `modules/role` |

---

## 1. 用户（account）

### 1.1 users 表归产品

users 表的迁移、User 模型都在产品中（示例里有默认版本），产品可以随意加字段。
go-kit 只约定它要用到的几列（类似 Laravel 只用 `password`、`email_verified_at`）：

```go
// account.UserFields 是账号模块要求 users 表具有的列，产品的 User 模型嵌入它。
type UserFields struct {
	database.Model                      // id、created_at、updated_at
	Email           string    `bun:"email,notnull"`             // 小写，唯一约束 users_email_key
	PasswordHash    string    `bun:"password_hash,notnull"`     // 为空表示不能用密码登录
	EmailVerifiedAt time.Time `bun:"email_verified_at,nullzero"`
	DisabledAt      time.Time `bun:"disabled_at,nullzero"`
	LastLoginAt     time.Time `bun:"last_login_at,nullzero"`
	Locale          string    `bun:"locale,notnull"`            // 用户选择的语言，空表示跟随请求
	AvatarKey       string    `bun:"avatar_key,notnull"`        // 不用头像功能时一直为空
}
```

产品的模型：

```go
type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`
	account.UserFields

	Name  string `bun:"name,notnull"`
	Phone string `bun:"phone,nullzero"`
	Plan  string `bun:"plan,notnull"`
}
```

account 只读写这些列（bun 按自己的结构体生成列名，不会碰产品加的列）。
示例中的 `create_users` 迁移给出这些列的定义，产品在此基础上加字段。

account 自己的表（user_sessions、user_mfa、user_api_keys、user_identities、password_reset_tokens）
仍由 account 的迁移创建。它们引用 users 表，但**不加外键**（迁移顺序由产品决定，加外键容易出错）；
产品删除用户时调用 `users.Purge(ctx, tx, userID)` 清理这些表。

### 1.2 产品实现的部分（Actions）

类似 Fortify 发布到应用中的 `CreateNewUser` 等 Action，由产品实现、示例中有默认版本：

```go
type Actions struct {
	// CreateOAuthUser 在第三方登录且没有对应账号时创建用户，返回用户 ID；为 nil 时不允许第三方登录自动注册。
	CreateOAuthUser func(ctx context.Context, tx bun.Tx, ou *OAuthUser) (uuid.UUID, error)
}

users, err := account.New(a.DB, cfg.Account, account.Actions{...})
```

注册的输入完全由产品定义：

```go
type registerInput struct {
	Body struct {
		Email    string `json:"email" format:"email"`
		Password string `json:"password"`
		Name     string `json:"name" maxLength:"100"`
		Company  string `json:"company"`
	}
}

// 产品的注册逻辑：校验、写 users 表。go-kit 负责事务、邮箱已注册的判断、发送验证邮件与注册事件。
rest.Post(v1, "/auth/register", account.RegisterEndpoint(users,
	func(ctx context.Context, tx bun.Tx, in *registerInput) (uuid.UUID, error) {
		if err := users.CheckPassword(in.Body.Password); err != nil {
			return uuid.Nil(), err
		}
		u := &User{Name: in.Body.Name, Company: in.Body.Company}
		u.Email = account.NormalizeEmail(in.Body.Email)
		u.PasswordHash = account.HashPassword(in.Body.Password)
		_, err := tx.NewInsert().Model(u).Exec(ctx)
		return u.ID, err
	}))
```

### 1.3 移出 go-kit 的部分

与用户资料相关的都归产品：`MeEndpoint`、`UpdateMeEndpoint`、`UserDTO`、`UpdateName` 删除，
示例中实现“当前用户”“修改资料”接口。重置密码、验证邮箱的邮件也不再由 account 发送（见 §2.5）。

### 1.4 保留在 go-kit 的部分

登录（密码、两步验证、第三方）、会话与 token、退出与登录设备、找回与修改密码、邮箱验证、
API Key、头像、停用账号、安全事件（`OnEvent`）——这些流程只依赖 `UserFields` 中的列。

登录名暂时只支持邮箱；手机号 + 短信验证码登录以后作为单独的流程加入。

---

## 2. 通知（notification）

### 2.1 通知类型由产品定义

```go
// 订单发货通知：产品定义，字段会序列化到队列中，所以只放 ID 等简单数据。
type OrderShipped struct {
	OrderID uuid.UUID `json:"order_id"`
	No      string    `json:"no"`
}

func (OrderShipped) Type() string { return "order.shipped" }

// 实现哪个渠道的接口，就通过哪个渠道发送。
func (n OrderShipped) ToDatabase(ctx context.Context, r notification.Recipient) (notification.Content, error) {
	return notification.Content{
		Title: i18n.T(ctx, "order.shipped.title", map[string]any{"No": n.No}),
		URL:   "/orders/" + n.OrderID.String(),
	}, nil
}

func (n OrderShipped) ToMail(ctx context.Context, r notification.Recipient) (mail.Message, error) { ... }
```

- 渠道由实现的接口决定：`ToDatabase`（站内信）、`ToMail`（邮件），产品自定义的渠道（短信、App 推送）定义自己的接口。
- 可选实现 `Via(r Recipient) []string` 进一步筛选渠道（例如按用户的通知偏好）。
- 渲染时 ctx 的语言已设为收件人的语言，`i18n.T` 直接得到对应语言的文字。

### 2.2 收件人

```go
// Recipient 是通知的接收者，产品从自己的 User 构造。
type Recipient struct {
	UserID uuid.UUID // 站内信的归属
	Email  string
	Phone  string
	Name   string
	Locale string    // 渲染通知使用的语言
}
```

不用接口，因为产品 User 上的 `ID` 字段与同名方法冲突；产品写一个 `func (u *User) Recipient() notification.Recipient` 即可。

### 2.3 发送

```go
notifier := notification.New(a.DB, a.Bus, notification.Config{})
notifier.AddChannel(notification.MailChannel(a.Mailer))
notifier.AddChannel(notification.DatabaseChannel())
notification.Register[OrderShipped](notifier) // 队列中按 Type() 还原通知

err := notifier.Send(ctx, u.Recipient(), OrderShipped{OrderID: o.ID, No: o.No})
err := notifier.SendTx(ctx, tx, u.Recipient(), n) // 与业务数据同一事务
```

- 发送总是异步：每个渠道一条消息（消息总线），某个渠道失败重试时不会重复发送其他渠道。
- 站内信保存**发送时按收件人语言渲染好的**标题、正文与链接，同时保存 `type` 与原始数据；
  用户切换语言后旧通知不变（与大多数产品一致）。

### 2.4 站内信

列表、未读数、标记已读、删除的服务方法与接口保持现状。

### 2.5 账号相关的邮件

account 只发布消息（`TopicPasswordReset`、`TopicEmailVerification`，payload 带 token 与收件人语言），
产品的处理函数加载自己的用户并发送通知。示例中提供 `ResetPassword`、`VerifyEmail` 两个通知类型
与对应的中英文模板，新产品直接沿用、按需修改。

---

## 3. 授权（authz，基础设施）

类似 Laravel 的 Gate：能力（ability）用字符串命名，规则由产品定义。

```go
gate := authz.New()

// 不针对具体对象的能力。
gate.Define("audit.view", func(ctx context.Context, user uuid.UUID) bool { ... })

// 针对某类对象的能力（类似 Policy）：对象类型作为泛型参数，调用处类型安全。
authz.DefineFor(gate, "order.update", func(ctx context.Context, user uuid.UUID, o *Order) bool {
	return o.UserID == user
})

// 在所有规则之前执行，返回 decided=true 时直接采用结果（例如超级管理员）。
gate.Before(func(ctx context.Context, user uuid.UUID, ability string) (allowed, decided bool) { ... })
```

使用：

```go
if err := authz.Authorize(ctx, gate, "order.update", order); err != nil {
	return err // 未登录 401，没有权限 403（错误已登记状态码与多语言提示）
}
ok := gate.Allows(ctx, "audit.view")

// 路由级：整组接口或页面要求某个能力。
admin.UseMiddleware(rest.Can(api, gate, "admin.access"))
r.With(web.Can(gate, "admin.access")).Get("/admin", ...)
```

- 用户取自 `auth.UserID(ctx)`；未定义的能力一律拒绝。
- API Key 的 scope 是另一层限制（类似 Sanctum 的 token abilities）：先看 Key 是否有该 scope，再看用户是否有该能力。
- 模块提供的“管理类”接口（审计日志查询、角色管理）在 Operation 中声明需要的能力，例如 `audit.view`，
  产品只需定义谁拥有这个能力，不用再自己包一层检查。

## 4. 角色与权限（role）

类似 spatie/laravel-permission：

- **权限**是代码中的字符串，由产品与各模块声明（带多语言的显示名），用于分配给角色：

  ```go
  roles.Declare("order.update", "audit.view", "role.manage")
  ```

- **角色**是数据，管理员在后台创建，给角色勾选权限，再把角色分配给用户。表：`roles`、`role_permissions`、`user_roles`。
- 与授权连接：`roles.Gate(gate)` 注册一个 Before：用户的角色拥有与能力同名的权限时允许。
  于是 `audit.view` 这种不需要对象的能力，不用写任何规则，分配权限即可。
  需要看对象的能力（“只能改自己的订单”）仍用 `DefineFor` 写规则，规则中也可以先查权限。
- 超级管理员：拥有 `*` 权限的角色允许一切。
- 提供接口：角色的增删改查、给用户分配角色、列出全部权限；这些接口需要 `role.manage` 能力。
- 一个请求内缓存当前用户的权限，避免每次检查都查库。
- 暂不支持“团队 / 租户内的角色”，多租户以后单独设计。

---

## 5. 对约定（conventions.md）的影响

- 模块之间互不依赖的规则不变：account 不依赖 notification（通过消息），role 只依赖基础设施 `authz`。
  audit 的 `AccountEvent` 适配移到示例。
- 多语言【待确认】项的结论：站内信在发送时按收件人语言渲染（§2.3）；审计日志保存 action 与 data，显示时翻译。
- 新增基础设施包：`i18n`、`authz`。

## 6. 待确认

1. **用户约定的方式**：上面用“列约定”（产品嵌入 `account.UserFields`）。另一种是“接口约定”：
   产品实现 `FindByID`、`FindByLogin`、`SetPassword` 等方法，用户可以存在任何地方，但产品要写的代码多得多。
   建议用列约定。
2. **通知一律异步**：好处是请求快、失败可重试；代价是测试与开发时要运行消息总线（app 的 serve 已经在跑）。建议一律异步。
3. **角色是全局的**：不支持团队内角色。建议先这样，多租户以后单独设计。
