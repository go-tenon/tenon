package identity

import (
	"context"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/errorx"
)

var (
	// ErrUnauthenticated 表示没有有效的凭据：没有携带、已经失效，或主体已失效。
	ErrUnauthenticated = errorx.Define(errorx.Unauthenticated, "auth.unauthenticated")
	// ErrForbidden 表示凭据有效但无权执行操作：凭据绑定了其他租户，或主体缺少所需的权限。
	ErrForbidden = errorx.Define(errorx.PermissionDenied, "auth.forbidden")
	// ErrImpersonationForbidden 表示该操作只能由账号本人执行，拒绝代为登录的凭据。
	ErrImpersonationForbidden = errorx.Define(errorx.PermissionDenied, "auth.impersonation_forbidden")
	// ErrConfirmRequired 表示操作要求最近确认过身份（rest.Confirmed）：客户端让用户再次输入密码或验证码，
	// 确认后（apitoken 的 Confirm）重试。
	ErrConfirmRequired = errorx.Define(errorx.PermissionDenied, "auth.confirm_required")
	// ErrTwoFactorRequired 表示操作要求这次登录经过二次验证（Auth.TwoFactor），例如租户要求成员使用二次验证：
	// 客户端让用户完成二次验证并确认身份后重试；账号还没有启用二次验证时，先去启用。
	ErrTwoFactorRequired = errorx.Define(errorx.PermissionDenied, "auth.two_factor_required")
	// ErrMethodRequired 表示操作要求这次登录使用特定的方式（Auth.Methods），例如租户要求成员用租户的企业 IdP 登录：
	// 公开参数 methods 列出可以用的方式，客户端据此让用户用其中一种重新登录。
	ErrMethodRequired = errorx.Define(errorx.PermissionDenied, "auth.method_required")
	// ErrReadOnly 表示当前凭据处于只读模式（如只读代为登录），禁止执行写操作。
	ErrReadOnly = errorx.Define(errorx.PermissionDenied, "auth.read_only")
)

// Authenticator 识别调用者，返回凭据给出的主体。没有凭据或凭据无效时返回 ID 为空的主体，或分类不是系统故障
// （errorx.Kind.Systemic 为 false）的错误；认证后端故障时返回 errorx.Unavailable、errorx.Internal 类错误或普通错误。
type Authenticator func(ctx context.Context) (Subject, error)

// Resolver 把凭据给出的主体转换为本次请求的主体，例如按 TenantFromContext 加载主体在租户中的权限。
// 返回 errorx.Unauthenticated 类错误或 ID 为空的主体表示主体已失效；其他不是系统故障的错误按其分类处理，
// 例如不是租户成员时返回 errorx.NotFound。返回的主体中 Type、ID、Method、Credential、BoundTenant、Scopes、
// Impersonator、Auth 总是取自凭据，Resolver 只加载权限和租户，主体始终是凭据中的那个人。
// 入口通过 Accounts 为每套账号体系指定 Resolver。
type Resolver func(ctx context.Context, sub Subject) (Subject, error)

// Impersonation 描述一个入口允许代为登录时对操作者的校验规则。
// Actors 列出允许发起代为登录的操作者账号体系及其 Resolver（校验时始终在平台域 "" 中执行），
// Permissions 是操作者必须持有的权限。未配置 Impersonation 的入口默认拒绝所有代为登录凭据。
type Impersonation struct {
	Actors      Accounts // 允许发起代为登录的操作者账号体系和各自的 Resolver，必填
	Permissions []string // 操作者在平台域中必须拥有的权限，例如 tenants:impersonate
}

// Validate 检查 Impersonation 配置：Actors 不能为空且其中的账号体系名称合法。
func (i *Impersonation) Validate() error {
	if i == nil {
		return nil
	}
	if len(i.Actors) == 0 {
		return errors.New("identity: impersonation requires at least one actor account system")
	}
	return i.Actors.Validate()
}

// Policy 是 Establish 校验主体、代为登录者与租户边界的规则。
type Policy struct {
	Accounts      Accounts                                       // 接受的账号体系和各自的 Resolver；为空时直接使用凭据给出的主体
	Impersonation *Impersonation                                 // 非 nil 时允许代为登录并按它校验操作者；nil 时拒绝所有代为登录凭据
	CheckTenant   func(ctx context.Context, tenant string) error // 可选：在确立主体前校验本次请求所在租户仍然可用（例如未被停用）；AccountScope 为 true 时不调用
	DirectOnly    bool                                           // true 时只接受本人直登凭据，拒绝代为登录（返回 ErrImpersonationForbidden）
	AccountScope  bool                                           // true 时为账号自省接口（如 /api/me、/api/auth/logout），放行绑定了租户的凭据并保留 BoundTenant
}

// Establish 得到本次调用的主体，JSON API、页面、MCP、RPC 和实时频道的入口都用它。依次：
//
//   - authenticate 识别调用者，没有有效的凭据时返回 ErrUnauthenticated；
//   - 凭据带 Impersonator 时：DirectOnly 为 true 返回 ErrImpersonationForbidden；policy.Impersonation 为 nil 返回 ErrUnauthenticated；
//     否则在平台域（""）按 Impersonation.Actors 校验操作者是否仍然有效并拥有 Impersonation.Permissions，失败返回 ErrUnauthenticated；
//   - 凭据绑定了租户（BoundTenant，为空时回退读 cred.Tenant）且非 AccountScope 时，与本次请求的租户（TenantFromContext）不同则返回 ErrForbidden；
//   - 调用 policy.Accounts.Resolve（非空时），错误的处理见 Resolver；
//   - 主体缺少 permissions 中的某项权限（Subject.Can）时返回 ErrForbidden，公开参数 permission 是这项权限。
//
// 返回的错误分三类，由入口各自呈现：认证后端或 Resolver 故障时原样返回（errorx.Unavailable、errorx.Internal
// 类错误或普通错误，Failed 报告为 true）；ErrUnauthenticated；其他拒绝调用者的错误，例如 ErrForbidden。
func Establish(ctx context.Context, authenticate Authenticator, policy Policy, permissions ...string) (Subject, error) {
	cred, err := authenticate(ctx)
	if Failed(err) {
		return Subject{}, err
	}
	if err != nil || cred.ID == "" {
		return Subject{}, ErrUnauthenticated.New()
	}
	if cred.Impersonated() {
		if policy.DirectOnly {
			return Subject{}, ErrImpersonationForbidden.New()
		}
		if policy.Impersonation == nil {
			return Subject{}, ErrUnauthenticated.New()
		}
		if err := verifyImpersonator(ctx, cred.Impersonator, policy.Impersonation); err != nil {
			return Subject{}, err
		}
	}
	bound := cred.BoundTenant
	if bound == "" {
		bound = cred.Tenant
	}
	if bound != "" && !policy.AccountScope && bound != TenantFromContext(ctx) {
		return Subject{}, ErrForbidden.New()
	}
	if policy.CheckTenant != nil && !policy.AccountScope {
		if err := policy.CheckTenant(ctx, TenantFromContext(ctx)); err != nil {
			return Subject{}, err
		}
	}
	sub := cred
	sub.BoundTenant = bound
	if len(policy.Accounts) > 0 {
		sub, err = policy.Accounts.Resolve(ctx, sub)
		switch {
		case Failed(err):
			return Subject{}, err
		case err != nil && errorx.KindOf(err) == errorx.Unauthenticated, err == nil && sub.ID == "":
			return Subject{}, ErrUnauthenticated.New()
		case err != nil:
			return Subject{}, err
		}
		// resolve 加载权限和请求租户；主体本身、认证方式、凭据、租户边界、凭据的限制、代为登录者、只读标记和验证情况由凭据决定，不随 resolve 改变。
		sub.Type, sub.ID = cred.Type, cred.ID
		sub.Method, sub.Credential, sub.BoundTenant, sub.Scopes, sub.Impersonator, sub.ReadOnly = cred.Method, cred.Credential, bound, cred.Scopes, cred.Impersonator, cred.ReadOnly
		sub.Auth = cred.Auth
	}
	for _, perm := range permissions {
		if !sub.Can(perm) {
			return Subject{}, ErrForbidden.New(errorx.Public("permission", perm))
		}
	}
	return sub, nil
}

// verifyImpersonator 在平台域（""）校验代为登录的操作者仍然有效并拥有所需权限。
func verifyImpersonator(ctx context.Context, actor Ref, imp *Impersonation) error {
	actorCtx := WithTenant(ctx, "")
	resolved, err := imp.Actors.Resolve(actorCtx, Subject{Type: actor.Type, ID: actor.ID})
	if Failed(err) {
		return err
	}
	if err != nil || resolved.ID == "" {
		return ErrUnauthenticated.New()
	}
	for _, perm := range imp.Permissions {
		if !resolved.Can(perm) {
			return ErrUnauthenticated.New()
		}
	}
	return nil
}

// Reestablish 在长连接期间重新执行 Establish，确认 was 仍然有效，返回重新加载的主体（权限可能已经变化）。
// 凭据被撤销、凭据换了主体或代为登录者、操作者失效、主体退出租户或失去权限时返回拒绝调用者的错误（Failed 报告为 false），连接应当结束；
// Failed 报告为 true 的错误表示暂时无法判断，连接保持。authenticate 和 resolve 收到的 ctx 中 Rechecking 报告为 true。
func Reestablish(ctx context.Context, was Subject, authenticate Authenticator, policy Policy, permissions ...string) (Subject, error) {
	sub, err := Establish(context.WithValue(ctx, recheckingKey{}, true), authenticate, policy, permissions...)
	if err != nil {
		return Subject{}, err
	}
	if sub.Ref() != was.Ref() || sub.Impersonator != was.Impersonator {
		return Subject{}, ErrUnauthenticated.New()
	}
	return sub, nil
}

// recheckingKey 是复查标记在 context 中的键。
type recheckingKey struct{}

// Rechecking 报告 ctx 是否来自 Reestablish，即长连接期间对已建立连接的复查。
// 不访问存储的凭据（例如 apitoken.JWT 的 access token）据此在复查时确认凭据没有被撤销。
func Rechecking(ctx context.Context) bool {
	v, _ := ctx.Value(recheckingKey{}).(bool)
	return v
}

// Failed 报告 err 是否表示认证后端或 Resolver 的故障（errorx.Unavailable、errorx.Internal 类错误或普通错误），
// 与对调用者的拒绝相区分。err 为 nil 时返回 false。
func Failed(err error) bool {
	if err == nil {
		return false
	}
	return errorx.KindOf(err).Systemic()
}
