package apitoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

var (
	// ErrInvalid 表示 token 不存在、已撤销或已过期。rest.RequireToken 把它呈现为 401。
	ErrInvalid = errorx.Define(errorx.Unauthenticated, "apitoken.invalid")
	// ErrNotFound 表示要撤销的 token 不存在或不属于该主体。
	ErrNotFound = errorx.Define(errorx.NotFound, "apitoken.not_found")
	// ErrTicketInvalid 表示代为登录交换票据不存在、已被消费或已过期。
	ErrTicketInvalid = errorx.Define(errorx.Unauthenticated, "apitoken.ticket_invalid")
)

// Kind 是 token 的类别。
type Kind string

const (
	// KindLogin 是网页、App 登录时签发的 token，按 Config 滑动过期，代表一台设备。
	KindLogin Kind = "login"
	// KindPersonal 是个人访问令牌，供脚本和第三方集成使用，过期时间由创建者指定。
	KindPersonal Kind = "personal"
	// KindOAuth 是用户授权 OAuth 客户端（例如 claude.ai 的连接器）后签发的令牌，见 Authorize。
	// 它只能用于授权时指定的资源，由 ValidateFor 校验；Validate 不接受它。
	KindOAuth Kind = "oauth"
	// KindImpersonation 是管理员或客服代为登录目标用户时签发的短时受限令牌，见 Impersonate。
	KindImpersonation Kind = "impersonation"
)

// 代为登录令牌与一次性交换票据的默认时长和上限。
const (
	DefaultImpersonationLifetime    = time.Hour
	MaxImpersonationLifetime        = 4 * time.Hour
	DefaultImpersonationIdleTimeout = 30 * time.Minute
	DefaultTicketTTL                = 30 * time.Second
	MaxTicketTTL                    = 5 * time.Minute
)

// Device 描述登录 token 所在的设备，用于设备列表。
type Device struct {
	Remember  bool   // 登录时选择了“记住我”，空闲时长使用 Config.RememberIdleTimeout。
	IP        string // 登录请求的客户端地址。
	UserAgent string // 登录请求的 User-Agent，超过 255 个字符时截断。
}

// Impersonation 描述一次代为登录会话的参数。
type Impersonation struct {
	Tenant      string        // 绑定的租户边界；非空时令牌只能用于该租户及账号自省接口
	Scopes      []string      // 可选的权限限制，支持通配符（例如只读排查传 []string{"*:read"}）；为空表示不额外限制
	Reason      string        // 代为登录原因或工单号，超过 255 个字符时截断
	Lifetime    time.Duration // 最长有效期；0 时使用 DefaultImpersonationLifetime，超过 MaxImpersonationLifetime 返回错误
	IdleTimeout time.Duration // 空闲超时时长；0 时使用 DefaultImpersonationIdleTimeout，负数返回错误
	Device      Device        // 发起或建立代为登录时的设备信息（IP 和 UserAgent）
}

// ExchangedImpersonation 是消费一次性代为登录交换票据后得到的结果。
type ExchangedImpersonation struct {
	Token    string        // 新签发的代为登录令牌明文，只返回一次
	Subject  identity.Ref  // 被代为登录的目标主体
	Actor    identity.Ref  // 实际操作者
	Tenant   string        // 令牌绑定的租户边界
	Reason   string        // 代为登录原因
	Lifetime time.Duration // 本次代为登录令牌的最长有效期，供设置会话 cookie 的 MaxAge
}

// Token 是已签发 token 的信息，token 明文只在签发时返回。
type Token struct {
	ID           string
	Kind         Kind
	Name         string       // 个人访问令牌的名称；登录 token 为空
	Scopes       []string     // 个人访问令牌、OAuth 令牌和受限代为登录令牌的权限限制；不限制时为空
	ClientID     string       // OAuth 令牌授权的客户端；其余为空
	ClientName   string       // OAuth 令牌授权时客户端的名称，例如 Claude；其余为空
	Resource     string       // OAuth 令牌只能用于的资源，例如 https://blog.example.com/mcp；其余为空
	Tenant       string       // token 绑定的租户；"" 表示不绑定
	Impersonator identity.Ref // 代为登录时签发 token 的实际操作者，见 identity.Subject.Impersonator；零值表示用户本人
	Reason       string       // 代为登录时的原因或工单号；其余为空
	IP           string
	UserAgent    string
	CreatedAt    time.Time
	LastUsedAt   time.Time
	ExpiresAt    time.Time // 零值表示不过期
	Current      bool      // 是否为 List 传入的主体本次请求所用的 token
}

// touchInterval 是 Validate 两次写入 last_used_at 的最小间隔：每个 token 每分钟最多写一次库，
// 空闲过期的精度相应为一分钟。
const touchInterval = time.Minute

const (
	maxName      = 255
	maxUserAgent = 255
	maxReason    = 255
)

// Store 是基于 Bun 的 token 存储，Migrations 提供表结构。可以并发使用。
//
// Config 只在 Login 签发时使用：登录 token 的空闲时长和最长期限写入 token 本身，Validate、List、Revoke 等只读表。
// 所以多个 Store 可以共用一张表，例如每套账号体系用自己的 Config 创建一个 Store 签发 token，
// 校验交给其中任何一个都得到相同的结果。
//
// Store 不在后台清理过期 token：过期 token 校验不通过，由应用定时调用 Cleanup 删除。
type Store struct {
	db          bun.IDB
	cfg         Config
	credentials identity.CredentialRechecker // 非 nil 时在撤销令牌提交后自动通知长连接复查
}

// Option 是 NewStore 的选项。
type Option func(*Store)

// Credentials 让 Store 在撤销令牌提交后自动通知所有实例上的长连接立即复查凭据，
// 通常是 broadcast.NewCredentials 的返回值。Store 建在 database.Tx 上时通过 Tx.AfterCommit 延迟到提交后发送。
func Credentials(c identity.CredentialRechecker) Option {
	return func(s *Store) { s.credentials = c }
}

// row 是 api_tokens 表中的一行。
type row struct {
	bun.BaseModel `bun:"table:api_tokens,alias:t"`

	ID uuid.UUID `bun:"id,pk,type:uuid"`
	database.Timestamps
	TokenHash        string    `bun:"token_hash,notnull"`
	SubjectType      string    `bun:"subject_type,notnull"`
	SubjectID        string    `bun:"subject_id,notnull"`
	Tenant           string    `bun:"tenant,notnull"`
	ImpersonatorType string    `bun:"impersonator_type,notnull"`
	ImpersonatorID   string    `bun:"impersonator_id,notnull"`
	Kind             Kind      `bun:"kind,notnull"`
	Name             string    `bun:"name,notnull"`
	Scopes           string    `bun:"scopes,notnull"`
	Reason           string    `bun:"reason,notnull"`
	IdleTimeout      int64     `bun:"idle_timeout,notnull"` // 秒；0 表示不按空闲延长
	EndsAt           time.Time `bun:"ends_at,nullzero"`     // 登录、OAuth、代为登录 token 的最长期限，滑动延长不超过它
	IP               string    `bun:"ip,notnull"`
	UserAgent        string    `bun:"user_agent,notnull"`
	LastUsedAt       time.Time `bun:"last_used_at,notnull"`
	ExpiresAt        time.Time `bun:"expires_at,nullzero"`
	ClientID         string    `bun:"client_id,notnull"`
	ClientName       string    `bun:"client_name,notnull"`
	Resource         string    `bun:"resource,notnull"`
	AuthMethods      string    `bun:"auth_methods,notnull"` // 登录的验证方式，空格分隔；其他种类为空
	AuthTime         time.Time `bun:"auth_time,nullzero"`   // 登录最近一次验证身份的时间
}

// ticketRow 是 impersonation_tickets 表中的一行。
type ticketRow struct {
	bun.BaseModel `bun:"table:impersonation_tickets,alias:it"`

	ID uuid.UUID `bun:"id,pk,type:uuid"`
	database.Timestamps
	TicketHash       string    `bun:"ticket_hash,notnull"`
	SubjectType      string    `bun:"subject_type,notnull"`
	SubjectID        string    `bun:"subject_id,notnull"`
	Tenant           string    `bun:"tenant,notnull"`
	ImpersonatorType string    `bun:"impersonator_type,notnull"`
	ImpersonatorID   string    `bun:"impersonator_id,notnull"`
	Scopes           string    `bun:"scopes,notnull"`
	Reason           string    `bun:"reason,notnull"`
	Lifetime         int64     `bun:"lifetime,notnull"`
	IdleTimeout      int64     `bun:"idle_timeout,notnull"`
	ExpiresAt        time.Time `bun:"expires_at,notnull"`
}

// NewStore 创建 token 存储。db 必须已执行 api_tokens 迁移。cfg 不合法或 db 为 nil 时 panic。
func NewStore(db bun.IDB, cfg Config, opts ...Option) *Store {
	if db == nil {
		panic("apitoken: nil database")
	}
	if err := cfg.Validate(); err != nil {
		panic(errors.Wrap(err, "apitoken"))
	}
	s := &Store{db: db, cfg: cfg}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	s.checkDB(db)
	return s
}

// WithDB 返回使用 db（通常是 database.RunInTx 的事务）的副本，继承配置与凭据复查器，
// 让令牌操作与长连接复查随外层事务一起提交。db 为 nil，或启用了 Credentials 却传入裸 bun.Tx 时 panic。
func (s *Store) WithDB(db bun.IDB) *Store {
	if db == nil {
		panic("apitoken: nil database")
	}
	cp := *s
	cp.db = db
	cp.checkDB(db)
	return &cp
}

// checkDB 确认启用了 Credentials 时不使用裸 bun.Tx。
func (s *Store) checkDB(db bun.IDB) {
	if s.credentials == nil {
		return
	}
	switch db.(type) {
	case database.Tx, *database.Tx:
	case bun.Tx, *bun.Tx:
		panic("apitoken: Credentials needs a database or a database.Tx so recheck runs after commit")
	}
}

// Login 为 sub 签发登录 token，返回 token 明文，明文只在这里返回一次。
// 有效期按此刻的 Config 确定并写入 token：最长 Lifetime，空闲时长为 IdleTimeout（Device.Remember 时为 RememberIdleTimeout）。
//
// 登录 token 代表用户本人，不限制权限：校验得到的主体 Scopes 为 nil，权限由 Resolve 按用户当前的角色加载。
// sub.BoundTenant（为空时取 sub.Tenant）不为空时，token 只能在这个租户中使用，例如只属于某个租户的终端客户账号。
// sub.Auth 记录这次登录怎样验证了身份（login.Realm 填写），校验得到的主体带有它，见 Confirm。
// 代为登录必须使用 Impersonate，传入带 Impersonator 的主体直接返回错误。
func (s *Store) Login(ctx context.Context, sub identity.Subject, d Device) (string, error) {
	if sub.Impersonated() {
		return "", errors.New("apitoken: use Impersonate to issue an impersonation token")
	}
	plain, _, err := s.login(ctx, sub, d, KindLogin)
	return plain, err
}

// login 签发 kind 类的登录 token，返回明文和写入的行。
func (s *Store) login(ctx context.Context, sub identity.Subject, d Device, kind Kind) (string, *row, error) {
	now := time.Now().UTC()
	methods, err := joinWords(sub.Auth.Methods, "auth method")
	if err != nil {
		return "", nil, err
	}
	idle, end := s.cfg.idle(d.Remember), now.Add(s.cfg.Lifetime)
	tenant := boundTenantOf(sub)
	r := &row{
		AuthMethods: methods,
		AuthTime:    sub.Auth.Time.UTC(),
		Kind:        kind,
		Tenant:      tenant,
		IdleTimeout: int64(idle / time.Second),
		EndsAt:      end,
		IP:          d.IP,
		UserAgent:   truncate(d.UserAgent, maxUserAgent),
		ExpiresAt:   deadline(end, idle, now),
	}
	plain, err := s.issue(ctx, sub, r, now)
	return plain, r, err
}

// Impersonate 由操作者 actor 为目标主体 target 签发代为登录令牌（KindImpersonation），返回令牌明文。
// 操作者本身处于代为登录状态时返回 identity.ErrImpersonationForbidden（禁止嵌套代登）。
// 令牌的 Auth 始终为零值，默认有效期为 DefaultImpersonationLifetime（最长 MaxImpersonationLifetime），
// 支持按 opt.Tenant 锁定租户边界并按 opt.Scopes 限制权限（例如只读排查）。
func (s *Store) Impersonate(ctx context.Context, actor identity.Subject, target identity.Ref, opt Impersonation) (string, error) {
	lifetime, idle, joinedScopes, reason, err := validateImpersonation(actor, target, opt)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	end := now.Add(lifetime)
	sub := identity.Subject{Type: target.Type, ID: target.ID, Impersonator: actor.Ref()}
	r := &row{
		Kind:        KindImpersonation,
		Tenant:      opt.Tenant,
		Scopes:      joinedScopes,
		Reason:      reason,
		IdleTimeout: int64(idle / time.Second),
		EndsAt:      end,
		IP:          opt.Device.IP,
		UserAgent:   truncate(opt.Device.UserAgent, maxUserAgent),
		ExpiresAt:   deadline(end, idle, now),
	}
	return s.issue(ctx, sub, r, now)
}

// CreateImpersonationTicket 由操作者 actor 为目标主体 target 签发一次性代为登录交换票据，返回票据明文。
// 票据默认有效期为 DefaultTicketTTL（ttl 为 0 时），最长 MaxTicketTTL。目标域调用 ExchangeImpersonationTicket
// 原子消费票据后换取真正的代为登录令牌并写入目标域的会话 cookie，同源和跨子域/自定义域名均适用。
func (s *Store) CreateImpersonationTicket(ctx context.Context, actor identity.Subject, target identity.Ref, opt Impersonation, ttl time.Duration) (string, error) {
	lifetime, idle, joinedScopes, reason, err := validateImpersonation(actor, target, opt)
	if err != nil {
		return "", err
	}
	if ttl == 0 {
		ttl = DefaultTicketTTL
	}
	if ttl < 0 || ttl > MaxTicketTTL {
		return "", errors.Newf("apitoken: ticket ttl must be between 0 and %s", MaxTicketTTL)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", errors.Wrap(err, "apitoken: new ticket id")
	}
	now := time.Now().UTC()
	plain := rand.Text()
	r := &ticketRow{
		ID:               id,
		TicketHash:       hash(plain),
		SubjectType:      string(target.Type),
		SubjectID:        target.ID,
		Tenant:           opt.Tenant,
		ImpersonatorType: string(actor.Type),
		ImpersonatorID:   actor.ID,
		Scopes:           joinedScopes,
		Reason:           reason,
		Lifetime:         int64(lifetime / time.Second),
		IdleTimeout:      int64(idle / time.Second),
		ExpiresAt:        now.Add(ttl),
	}
	r.CreatedAt = now
	if _, err := s.db.NewInsert().Model(r).Exec(ctx); err != nil {
		return "", errors.Wrap(err, "apitoken: insert ticket")
	}
	return plain, nil
}

// ExchangeImpersonationTicket 原子消费一次性代为登录交换票据并签发真正的 KindImpersonation 令牌。
// 票据不存在、已被消费或已过期时返回 ErrTicketInvalid。
func (s *Store) ExchangeImpersonationTicket(ctx context.Context, ticket string, d Device) (ExchangedImpersonation, error) {
	if ticket == "" {
		return ExchangedImpersonation{}, ErrTicketInvalid.New()
	}
	now := time.Now().UTC()
	var tr ticketRow
	err := s.db.NewDelete().Model(&tr).
		Where("ticket_hash = ?", hash(ticket)).
		Where("expires_at > ?", now).
		Returning("*").
		Scan(database.Primary(ctx))
	if errors.Is(err, sql.ErrNoRows) {
		return ExchangedImpersonation{}, ErrTicketInvalid.New()
	}
	if err != nil {
		return ExchangedImpersonation{}, errors.Wrap(err, "apitoken: exchange ticket")
	}
	lifetime := time.Duration(tr.Lifetime) * time.Second
	idle := time.Duration(tr.IdleTimeout) * time.Second
	end := now.Add(lifetime)
	target := identity.Ref{Type: identity.SubjectType(tr.SubjectType), ID: tr.SubjectID}
	actor := identity.Ref{Type: identity.SubjectType(tr.ImpersonatorType), ID: tr.ImpersonatorID}
	sub := identity.Subject{Type: target.Type, ID: target.ID, Impersonator: actor}
	r := &row{
		Kind:        KindImpersonation,
		Tenant:      tr.Tenant,
		Scopes:      tr.Scopes,
		Reason:      tr.Reason,
		IdleTimeout: tr.IdleTimeout,
		EndsAt:      end,
		IP:          d.IP,
		UserAgent:   truncate(d.UserAgent, maxUserAgent),
		ExpiresAt:   deadline(end, idle, now),
	}
	plain, err := s.issue(ctx, sub, r, now)
	if err != nil {
		return ExchangedImpersonation{}, err
	}
	return ExchangedImpersonation{
		Token:    plain,
		Subject:  target,
		Actor:    actor,
		Tenant:   tr.Tenant,
		Reason:   tr.Reason,
		Lifetime: lifetime,
	}, nil
}

// validateImpersonation 校验代为登录的操作者、目标和时长限制，返回规范化后的参数。
func validateImpersonation(actor identity.Subject, target identity.Ref, opt Impersonation) (lifetime, idle time.Duration, scopes, reason string, err error) {
	if err := actor.Ref().Validate(); err != nil {
		return 0, 0, "", "", errors.Wrap(err, "apitoken: actor")
	}
	if actor.Impersonated() {
		return 0, 0, "", "", identity.ErrImpersonationForbidden.New()
	}
	if err := target.Validate(); err != nil {
		return 0, 0, "", "", errors.Wrap(err, "apitoken: target")
	}
	lifetime = opt.Lifetime
	if lifetime == 0 {
		lifetime = DefaultImpersonationLifetime
	}
	if lifetime < 0 || lifetime > MaxImpersonationLifetime {
		return 0, 0, "", "", errors.Newf("apitoken: impersonation lifetime must be between 0 and %s", MaxImpersonationLifetime)
	}
	idle = opt.IdleTimeout
	if idle == 0 {
		idle = DefaultImpersonationIdleTimeout
	}
	if idle < 0 {
		return 0, 0, "", "", errors.New("apitoken: impersonation idle timeout must not be negative")
	}
	if len(opt.Scopes) > 0 {
		scopes, err = joinScopes(opt.Scopes)
		if err != nil {
			return 0, 0, "", "", err
		}
	}
	reason = truncate(strings.TrimSpace(opt.Reason), maxReason)
	return lifetime, idle, scopes, reason, nil
}

// boundTenantOf 返回主体的租户边界：优先使用 BoundTenant，为空时使用 Tenant。
func boundTenantOf(sub identity.Subject) string {
	if sub.BoundTenant != "" {
		return sub.BoundTenant
	}
	return sub.Tenant
}

// Personal 描述一个个人访问令牌。
type Personal struct {
	Name      string    // 用于在令牌列表中辨认，长度为 1 到 255 个字符。
	Scopes    []string  // 令牌被限制在内的权限，至少一个，支持通配符（见 identity.Subject.Can）。令牌实际能做的是用户当前权限与 Scopes 的交集，用户被收回权限后令牌随之收窄。
	ExpiresAt time.Time // ExpiresAt 为零值表示不过期，否则必须晚于当前时间。
}

// Create 为 sub 创建个人访问令牌，返回 token 明文，明文只在这里返回一次。
// 新令牌不比 sub 所用的凭据更宽：sub.Scopes 不为 nil 时，p.Scopes 的每一项都要在 sub.Scopes 之内，否则返回 identity.ErrForbidden；
// sub.BoundTenant 或 sub.Tenant 不为空时令牌只能在这个租户中使用。
// 代为登录期间（sub.Impersonator 不为零值）拒绝创建个人访问令牌，返回 identity.ErrImpersonationForbidden，防止操作者绕过代登有效期。
func (s *Store) Create(ctx context.Context, sub identity.Subject, p Personal) (string, error) {
	if !sub.Impersonator.IsZero() {
		return "", identity.ErrImpersonationForbidden.New()
	}
	now := time.Now().UTC()
	name := strings.TrimSpace(p.Name)
	if name == "" || len([]rune(name)) > maxName {
		return "", errors.Newf("apitoken: name must be 1 to %d characters", maxName)
	}
	if len(p.Scopes) == 0 {
		return "", errors.New("apitoken: a personal access token needs at least one scope")
	}
	if !p.ExpiresAt.IsZero() && !p.ExpiresAt.After(now) {
		return "", errors.New("apitoken: ExpiresAt must be in the future")
	}
	joined, err := joinScopes(p.Scopes)
	if err != nil {
		return "", err
	}
	if sub.Scopes != nil {
		for _, scope := range p.Scopes {
			if !identity.Grants(sub.Scopes, scope) {
				return "", identity.ErrForbidden.New(errorx.Public("permission", scope))
			}
		}
	}
	r := &row{Kind: KindPersonal, Name: name, Scopes: joined, Tenant: boundTenantOf(sub), ExpiresAt: p.ExpiresAt.UTC()}
	return s.issue(ctx, sub, r, now)
}

// Grant 描述用户对一个 OAuth 客户端的授权，由 OAuth 授权服务器（oauthserver）在用户同意后使用。
type Grant struct {
	ClientID    string        // 客户端 ID，记录下来用于“已连接的应用”列表
	ClientName  string        // 同意页上显示的客户端名称，记录下来用于“已连接的应用”列表
	Resource    string        // 令牌只能用于的资源，例如 MCP 服务的地址 https://blog.example.com/mcp。
	Scopes      []string      // 用户同意的范围，至少一个，与个人访问令牌的 Scopes 相同：令牌能做的是用户当前权限与它的交集。
	Lifetime    time.Duration // 令牌从签发起的最长有效期
	IdleTimeout time.Duration // 最长空闲时间，0 表示不限制；与登录 token 相同按使用滑动延长
}

// Authorize 为 sub 签发 OAuth 令牌，返回 token 明文，明文只在这里返回一次。sub.BoundTenant 或 sub.Tenant 不为空时令牌只能在这个租户中使用。
// 令牌只由 ValidateFor(g.Resource) 接受；它出现在 List 中，可以用 Revoke 撤销，RevokeOthers 保留它。
func (s *Store) Authorize(ctx context.Context, sub identity.Subject, g Grant) (string, error) {
	if g.ClientID == "" || g.Resource == "" {
		return "", errors.New("apitoken: an OAuth grant needs a client ID and a resource")
	}
	if len(g.Scopes) == 0 {
		return "", errors.New("apitoken: an OAuth grant needs at least one scope")
	}
	if g.Lifetime <= 0 || g.IdleTimeout < 0 {
		return "", errors.New("apitoken: an OAuth grant needs a positive lifetime and a non-negative idle timeout")
	}
	joined, err := joinScopes(g.Scopes)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	end := now.Add(g.Lifetime)
	r := &row{
		Kind:        KindOAuth,
		Name:        truncate(g.ClientName, maxName),
		Scopes:      joined,
		Tenant:      boundTenantOf(sub),
		IdleTimeout: int64(g.IdleTimeout / time.Second),
		EndsAt:      end,
		ExpiresAt:   deadline(end, g.IdleTimeout, now),
		ClientID:    g.ClientID,
		ClientName:  truncate(g.ClientName, maxName),
		Resource:    g.Resource,
	}
	return s.issue(ctx, sub, r, now)
}

// issue 为主体 sub 生成新的 token，写入 r 并保存，返回 token 明文。
func (s *Store) issue(ctx context.Context, sub identity.Subject, r *row, now time.Time) (string, error) {
	if err := sub.Ref().Validate(); err != nil {
		return "", errors.Wrap(err, "apitoken")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", errors.Wrap(err, "apitoken: new id")
	}
	plain := rand.Text()
	r.ID, r.TokenHash = id, hash(plain)
	r.SubjectType, r.SubjectID = string(sub.Type), sub.ID
	r.ImpersonatorType, r.ImpersonatorID = string(sub.Impersonator.Type), sub.Impersonator.ID
	r.CreatedAt, r.LastUsedAt = now, now
	if _, err := s.db.NewInsert().Model(r).Exec(ctx); err != nil {
		return "", errors.Wrap(err, "apitoken: insert")
	}
	return plain, nil
}

// Validate 实现 identity.TokenValidator，校验登录 token、个人访问令牌和代为登录令牌：返回 token 代表的主体，
// Method 为 bearer，Credential 为 token 的 ID，BoundTenant 和 Tenant 为 token 绑定的租户，Impersonator 为签发时的代为登录者。
// 登录 token 的 Scopes 为 nil，个人访问令牌和带权限限制的代为登录令牌的 Scopes 是创建时的限制；两者的 Permissions 都为空，由 Resolve 加载。
// token 不存在、已撤销或已过期时返回 ErrInvalid。
//
// 距上次记录使用超过一分钟时，更新最近使用时间，并延长滑动过期的 token 有效期。
// OAuth 令牌只能用于它的资源（见 ValidateFor），在这里校验返回 ErrInvalid。
func (s *Store) Validate(ctx context.Context, token string) (identity.Subject, error) {
	return s.validate(ctx, token, "")
}

// ValidateFor 返回只接受发给 resource 的令牌的校验器：登录 token、个人访问令牌、代为登录令牌，以及 Resource 为 resource 的 OAuth 令牌。
// 发给其他资源的 OAuth 令牌按无效处理。用于 OAuth 客户端访问的资源，例如 mcpx.HandlerOptions.Validate：
//
//	Validate: tokens.ValidateFor("https://blog.example.com/mcp")
//
// OAuth 令牌得到的主体与个人访问令牌相同，Scopes 是用户同意的范围。resource 为空时 panic。
func (s *Store) ValidateFor(resource string) func(ctx context.Context, token string) (identity.Subject, error) {
	if resource == "" {
		panic("apitoken: ValidateFor needs a resource")
	}
	return func(ctx context.Context, token string) (identity.Subject, error) {
		return s.validate(ctx, token, resource)
	}
}

// validate 校验 token。resource 为空时不接受 OAuth 令牌，否则只接受发给 resource 的 OAuth 令牌。
func (s *Store) validate(ctx context.Context, token, resource string) (identity.Subject, error) {
	if token == "" {
		return identity.Subject{}, ErrInvalid.New()
	}
	now := time.Now().UTC()
	var r row
	q := s.live(s.db.NewSelect().Model(&r), now).Where("token_hash = ?", hash(token))
	if resource == "" {
		q = q.Where("kind <> ?", KindOAuth)
	} else {
		q = q.Where("(kind <> ? OR resource = ?)", KindOAuth, resource)
	}
	// 在主库校验：登录后客户端立即用新 token 发请求，撤销后旧 token 立即失效，都不受读库延迟影响。
	err := q.Scan(database.Primary(ctx))
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Subject{}, ErrInvalid.New()
	}
	if err != nil {
		return identity.Subject{}, errors.Wrap(err, "apitoken: validate")
	}
	if now.Sub(r.LastUsedAt) >= touchInterval {
		if err := s.touch(ctx, &r, now); err != nil {
			return identity.Subject{}, err
		}
	}
	sub := identity.Subject{
		Type:         identity.SubjectType(r.SubjectType),
		ID:           r.SubjectID,
		Method:       "bearer",
		Credential:   r.ID.String(),
		Tenant:       r.Tenant,
		BoundTenant:  r.Tenant,
		Impersonator: r.impersonator(),
	}
	switch r.Kind {
	case KindPersonal, KindOAuth:
		sub.Scopes = strings.Fields(r.Scopes)
	case KindImpersonation:
		if r.Scopes != "" {
			sub.Scopes = strings.Fields(r.Scopes)
		}
	case KindLogin:
		sub.Auth = r.auth()
	}
	return sub, nil
}

// Confirm 记录 sub 本次请求所用的登录（sub.Credential）此刻再次验证了身份，例如用户重新输入了密码（login.Realm.Confirm），
// methods 是这次用到的验证方式，补充到登录的 Auth.Methods 中；之后 rest.Confirmed 的路由在有效期内放行。
// 用二次验证确认后，没有经过二次验证的登录也变为经过了（identity.Auth.TwoFactor），当前的登录继续有效。
//
// 下一个请求就生效。
// sub.Credential 不是 sub 的登录（个人访问令牌、OAuth 令牌）时返回 ErrNotFound；代为登录的 token 返回 identity.ErrForbidden，
// 实际操作的人不是主体本人。methods 必填。
func (s *Store) Confirm(ctx context.Context, sub identity.Subject, methods ...string) error {
	if len(methods) == 0 {
		return errors.New("apitoken: Confirm needs at least one auth method")
	}
	if _, err := joinWords(methods, "auth method"); err != nil {
		return err
	}
	if !sub.Impersonator.IsZero() {
		return identity.ErrForbidden.New()
	}
	id, err := uuid.Parse(sub.Credential)
	if err != nil || sub.ID == "" {
		return ErrNotFound.New()
	}
	now := time.Now().UTC()
	var r row
	err = s.live(s.db.NewSelect().Model(&r), now).Where("id = ?", id).
		Where("subject_type = ? AND subject_id = ?", sub.Type, sub.ID).
		Where("kind = ?", KindLogin).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound.New()
	}
	if err != nil {
		return errors.Wrap(err, "apitoken: confirm")
	}
	merged := strings.Fields(r.AuthMethods)
	for _, m := range methods {
		if !slices.Contains(merged, m) {
			merged = append(merged, m)
		}
	}
	r.AuthMethods, r.AuthTime = strings.Join(merged, " "), now
	_, err = s.db.NewUpdate().Model((*row)(nil)).
		Set("auth_methods = ?", r.AuthMethods).Set("auth_time = ?", r.AuthTime).
		Where("id = ?", r.ID).Exec(ctx)
	return errors.Wrap(err, "apitoken: confirm")
}

// touch 记录 token 在 now 被使用。条件更新保证并发请求中只有一个真正写入。
func (s *Store) touch(ctx context.Context, r *row, now time.Time) error {
	q := s.db.NewUpdate().Model((*row)(nil)).Set("last_used_at = ?", now).
		Where("id = ?", r.ID).Where("last_used_at < ?", now.Add(-touchInterval))
	if r.IdleTimeout > 0 {
		q = q.Set("expires_at = ?", deadline(r.EndsAt, time.Duration(r.IdleTimeout)*time.Second, now))
	}
	_, err := q.Exec(ctx)
	return errors.Wrap(err, "apitoken: touch")
}

// List 返回 sub 未过期的 token，最近使用的在前；本次请求所用的 token（sub.Credential）标为 Current。
func (s *Store) List(ctx context.Context, sub identity.Subject) ([]Token, error) {
	var rows []row
	err := s.live(s.db.NewSelect().Model(&rows), time.Now().UTC()).
		Where("subject_type = ? AND subject_id = ?", sub.Type, sub.ID).OrderExpr("last_used_at DESC").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "apitoken: list")
	}
	out := make([]Token, 0, len(rows))
	for _, r := range rows {
		t := r.token()
		t.Current = t.ID == sub.Credential
		out = append(out, t)
	}
	return out, nil
}

// Revoke 撤销 sub 的 token id；传入 sub.Credential 即登出当前设备或退出代为登录。
// token 不存在或不属于 sub 时返回 ErrNotFound。
func (s *Store) Revoke(ctx context.Context, sub identity.Subject, id string) error {
	tid, err := uuid.Parse(id)
	if err != nil || sub.ID == "" {
		return ErrNotFound.New()
	}
	n, err := affected(s.db.NewDelete().Model((*row)(nil)).
		Where("id = ?", tid).Where("subject_type = ? AND subject_id = ?", sub.Type, sub.ID).Exec(ctx))
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound.New()
	}
	s.recheckCredential(ctx, tid.String())
	return nil
}

// RevokeToken 按明文撤销一个 OAuth 令牌或应用自有客户端的登录令牌，用于 OAuth 的撤销端点（RFC 7009）：客户端断开连接时交回令牌。
// 令牌不存在、已过期，或者是个人访问令牌、代为登录令牌时直接返回 nil。
func (s *Store) RevokeToken(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	var revoked uuid.UUID
	err := s.db.NewDelete().Model((*row)(nil)).
		Where("token_hash = ?", hash(token)).Where("kind IN (?)", bun.List([]Kind{KindOAuth, KindLogin})).
		Returning("id").Scan(ctx, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "apitoken: delete")
	}
	s.recheckCredential(ctx, revoked.String())
	return nil
}

// RevokeOthers 撤销 sub 除本次请求所用 token（sub.Credential）以外的全部登录 token，返回撤销的个数，
// 例如“退出其他设备”。个人访问令牌、OAuth 令牌和代为登录令牌保持有效。sub.Credential 不是 sub 的 token 时，撤销它的全部登录 token。
func (s *Store) RevokeOthers(ctx context.Context, sub identity.Subject) (int, error) {
	if sub.ID == "" {
		return 0, errors.New("apitoken: subject ID is required")
	}
	q := s.db.NewDelete().Model((*row)(nil)).
		Where("subject_type = ? AND subject_id = ?", sub.Type, sub.ID).Where("kind = ?", KindLogin)
	if current, err := uuid.Parse(sub.Credential); err == nil {
		q = q.Where("id <> ?", current)
	}
	n, err := affected(q.Exec(ctx))
	if err == nil && n > 0 {
		s.recheckSubject(ctx, sub.Ref())
	}
	return n, err
}

// RevokeSubject 撤销主体 ref 的全部 token（包括个人访问令牌、OAuth 令牌和代为登录令牌），返回撤销的个数。用于封禁账号、修改密码等。
func (s *Store) RevokeSubject(ctx context.Context, ref identity.Ref) (int, error) {
	if err := ref.Validate(); err != nil {
		return 0, errors.Wrap(err, "apitoken")
	}
	n, err := affected(s.db.NewDelete().Model((*row)(nil)).
		Where("subject_type = ? AND subject_id = ?", ref.Type, ref.ID).Exec(ctx))
	if err == nil && n > 0 {
		s.recheckSubject(ctx, ref)
	}
	return n, err
}

// RevokeByImpersonator 撤销操作者 actor 签发的全部代为登录令牌与未消费的交换票据，返回撤销的令牌个数。
// 用于操作者停用、离职或收回代为登录权限时立即回收全部代登会话。
func (s *Store) RevokeByImpersonator(ctx context.Context, actor identity.Ref) (int, error) {
	if err := actor.Validate(); err != nil {
		return 0, errors.Wrap(err, "apitoken")
	}
	if _, err := s.db.NewDelete().Model((*ticketRow)(nil)).
		Where("impersonator_type = ? AND impersonator_id = ?", actor.Type, actor.ID).Exec(ctx); err != nil {
		return 0, errors.Wrap(err, "apitoken: delete impersonator tickets")
	}
	n, err := affected(s.db.NewDelete().Model((*row)(nil)).
		Where("impersonator_type = ? AND impersonator_id = ?", actor.Type, actor.ID).Exec(ctx))
	if err == nil && n > 0 {
		s.recheckSubject(ctx, actor)
	}
	return n, err
}

// recheckCredential 在提交之后通知所有实例立即复查凭据 id。
func (s *Store) recheckCredential(ctx context.Context, id string) {
	if s.credentials == nil {
		return
	}
	if tx, ok := s.db.(database.Tx); ok {
		tx.AfterCommit(func(ctx context.Context) error { return s.credentials.RecheckCredential(ctx, id) })
		return
	}
	if err := s.credentials.RecheckCredential(ctx, id); err != nil {
		slog.ErrorContext(ctx, "recheck credential failed", "error", err, "component", "apitoken")
	}
}

// recheckSubject 在提交之后通知所有实例立即复查主体 ref 的全部长连接。
func (s *Store) recheckSubject(ctx context.Context, ref identity.Ref) {
	if s.credentials == nil {
		return
	}
	if tx, ok := s.db.(database.Tx); ok {
		tx.AfterCommit(func(ctx context.Context) error { return s.credentials.RecheckSubject(ctx, ref) })
		return
	}
	if err := s.credentials.RecheckSubject(ctx, ref); err != nil {
		slog.ErrorContext(ctx, "recheck subject credentials failed", "error", err, "component", "apitoken")
	}
}

// Erase 在 db 中删除主体 ref 的全部 token 和未消费的代为登录交换票据（包括作为主体和作为代为登录操作者的记录），
// 用于停用或注销账号。db 通常是业务所在的事务，签名与 privacy.Source 的 Erase 相同。
func Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error {
	if err := ref.Validate(); err != nil {
		return errors.Wrap(err, "apitoken")
	}
	if _, err := db.NewDelete().Model((*ticketRow)(nil)).
		Where("(subject_type = ? AND subject_id = ?) OR (impersonator_type = ? AND impersonator_id = ?)", ref.Type, ref.ID, ref.Type, ref.ID).
		Exec(ctx); err != nil {
		return errors.Wrap(err, "apitoken: erase tickets")
	}
	_, err := affected(db.NewDelete().Model((*row)(nil)).
		Where("(subject_type = ? AND subject_id = ?) OR (impersonator_type = ? AND impersonator_id = ?)", ref.Type, ref.ID, ref.Type, ref.ID).
		Exec(ctx))
	return err
}

// Export 返回主体 ref 的全部 token（不含 token 本身和它的哈希），用于导出个人数据；签名与 privacy.Source 的 Export 相同。
// 每个 token 是一个对象：kind、name、scopes、tenant、client、impersonated（是否由别人代为登录签发）、reason、
// ip、user_agent、created_at、last_used_at、expires_at，空的字段省略。
func Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error) {
	if err := ref.Validate(); err != nil {
		return nil, errors.Wrap(err, "apitoken")
	}
	var rows []row
	err := db.NewSelect().Model(&rows).
		Where("subject_type = ? AND subject_id = ?", ref.Type, ref.ID).OrderExpr("created_at, id").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "apitoken: export")
	}
	type exported struct {
		Kind         Kind      `json:"kind"`
		Name         string    `json:"name,omitempty"`
		Scopes       []string  `json:"scopes,omitempty"`
		Tenant       string    `json:"tenant,omitempty"`
		Client       string    `json:"client,omitempty"`
		Impersonated bool      `json:"impersonated,omitempty"`
		Reason       string    `json:"reason,omitempty"`
		IP           string    `json:"ip,omitempty"`
		UserAgent    string    `json:"user_agent,omitempty"`
		CreatedAt    time.Time `json:"created_at"`
		LastUsedAt   time.Time `json:"last_used_at,omitzero"`
		ExpiresAt    time.Time `json:"expires_at,omitzero"`
	}
	out := make([]exported, len(rows))
	for i, r := range rows {
		out[i] = exported{
			Kind: r.Kind, Name: r.Name, Scopes: strings.Fields(r.Scopes), Tenant: r.Tenant, Client: r.ClientName,
			Impersonated: r.ImpersonatorID != "", Reason: r.Reason, IP: r.IP, UserAgent: r.UserAgent,
			CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt, ExpiresAt: r.ExpiresAt,
		}
	}
	return out, nil
}

// Cleanup 返回删除 db 中已过期令牌和代为登录交换票据的清理函数，每次各最多删除 limit 条，返回删除总数，
// 可以直接交给 schedule.Cleanup 定时执行。多套账号体系共用 api_tokens 表，只需注册一次。db 为 nil 时 panic。
func Cleanup(db bun.IDB) func(ctx context.Context, limit int) (int, error) {
	if db == nil {
		panic("apitoken: nil database")
	}
	return func(ctx context.Context, limit int) (int, error) {
		if limit <= 0 {
			return 0, errors.New("apitoken: cleanup limit must be positive")
		}
		now := time.Now().UTC()
		expired := db.NewSelect().Model((*row)(nil)).Column("id").
			Where("expires_at <= ?", now).OrderExpr("expires_at").Limit(int64(limit))
		tokens, err := affected(db.NewDelete().Model((*row)(nil)).
			Where("id IN (?)", expired).Exec(ctx))
		if err != nil {
			return 0, err
		}
		expiredTickets := db.NewSelect().Model((*ticketRow)(nil)).Column("id").
			Where("expires_at <= ?", now).OrderExpr("expires_at").Limit(int64(limit))
		tickets, err := affected(db.NewDelete().Model((*ticketRow)(nil)).
			Where("id IN (?)", expiredTickets).Exec(ctx))
		if err != nil {
			return tokens, err
		}
		return tokens + tickets, nil
	}
}

// Cleanup 删除最多 limit 个已过期的 token，同时清理最多 limit 个已过期的代为登录交换票据，返回删除的记录总数。由应用定时调用。
func (s *Store) Cleanup(ctx context.Context, limit int) (int, error) {
	return Cleanup(s.db)(ctx, limit)
}

// token 把行转换为 Token。
func (r *row) token() Token {
	return Token{
		ID:           r.ID.String(),
		Kind:         r.Kind,
		Name:         r.Name,
		Scopes:       strings.Fields(r.Scopes),
		Tenant:       r.Tenant,
		Impersonator: r.impersonator(),
		Reason:       r.Reason,
		IP:           r.IP,
		UserAgent:    r.UserAgent,
		CreatedAt:    r.CreatedAt,
		LastUsedAt:   r.LastUsedAt,
		ExpiresAt:    r.ExpiresAt,
		ClientID:     r.ClientID,
		ClientName:   r.ClientName,
		Resource:     r.Resource,
	}
}

// auth 返回登录的验证情况。
func (r *row) auth() identity.Auth {
	return identity.Auth{Methods: strings.Fields(r.AuthMethods), Time: r.AuthTime}
}

// impersonator 返回代为操作的主体，没有时为零值。
func (r *row) impersonator() identity.Ref {
	return identity.Ref{Type: identity.SubjectType(r.ImpersonatorType), ID: r.ImpersonatorID}
}

// live 把查询限定在未过期的 token。
func (s *Store) live(q *bun.SelectQuery, now time.Time) *bun.SelectQuery {
	return q.Where("(expires_at IS NULL OR expires_at > ?)", now)
}

// hash 返回 token 的 SHA-256，十六进制。
func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// joinScopes 把 scope 存为空格分隔的字符串；scope 必须非空且不含空白。
func joinScopes(scopes []string) (string, error) { return joinWords(scopes, "scope") }

// joinWords 把 words 存为空格分隔的字符串；每一项必须非空且不含空白。what 用于错误信息。
func joinWords(words []string, what string) (string, error) {
	for _, w := range words {
		if w == "" || strings.ContainsFunc(w, unicode.IsSpace) {
			return "", errors.Newf("apitoken: invalid %s %q", what, w)
		}
	}
	return strings.Join(words, " "), nil
}

// truncate 把 s 截断为最多 n 个字符。
func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// affected 返回删除的行数。
func affected(res sql.Result, err error) (int, error) {
	if err != nil {
		return 0, errors.Wrap(err, "apitoken: delete")
	}
	n, err := res.RowsAffected()
	return int(n), errors.Wrap(err, "apitoken: delete")
}
