package identity

import "context"

// TokenValidator 校验调用者出示的 token（例如 Authorization: Bearer 中的值）并返回凭据给出的主体。
// ctx 是本次调用的 context。token 无效时返回 ID 为空的主体或 4xx 类错误，只是过期时返回匹配 ErrExpired 的错误；
// 认证后端故障时返回 Failed 报告为 true 的错误。
//
// 它与传输方式无关：rest.RequireBearer、mcpx.Handler 和 realtime 的连接使用同一个校验器，
// 例如 apitoken.Store 的 Validate。
type TokenValidator func(ctx context.Context, token string) (Subject, error)

// CredentialCheck 在长连接期间重新校验调用者的凭据。
//
// 凭据已经失效（token 被撤销、用户被停用、失去所需的 scope）时返回 errorx.Unauthenticated、
// errorx.PermissionDenied 这类拒绝调用者的错误；暂时无法判断（例如数据库故障）时返回 Failed 报告为 true 的错误，
// 连接保持，下次再查。
type CredentialCheck func(ctx context.Context) error

type credentialKey struct{}

// WithCredentialCheck 返回带有 check 的 context。入口的认证中间件在认证通过后调用它，
// 例如 rest.RequireAuth 和 web 的页面路由；SSE 等长连接据此定期重新校验，凭据失效后结束连接。
func WithCredentialCheck(ctx context.Context, check CredentialCheck) context.Context {
	checks, _ := ctx.Value(credentialKey{}).([]CredentialCheck)
	return context.WithValue(ctx, credentialKey{}, append(checks[:len(checks):len(checks)], check))
}

// CheckCredential 运行 ctx 中的全部 CredentialCheck，返回第一个表示凭据失效的错误；
// 没有校验、全部通过，或只是暂时无法判断（Failed）时返回 nil。
func CheckCredential(ctx context.Context) error {
	checks, _ := ctx.Value(credentialKey{}).([]CredentialCheck)
	for _, check := range checks {
		if err := check(ctx); err != nil && !Failed(err) {
			return err
		}
	}
	return nil
}

// CredentialWatcher 在凭据可能已经变化时通知长连接立即重新校验。
// broadcast.Credentials 是基于广播的实现，多个实例之间也能互相通知。
type CredentialWatcher interface {
	// Watch 返回一个通道：sub 所用的凭据（sub.Credential）、sub 本身（sub.Ref()），
	// 或调用所在的租户（TenantFromContext）被要求复查时，通道收到信号。ctx 结束时停止关注。
	Watch(ctx context.Context, sub Subject) <-chan struct{}
}

type watcherKey struct{}

// WithCredentialWatcher 返回带有 w 的 context。认证中间件在安装 CredentialCheck 的同时调用它，
// 例如设置了 rest.Options.Credentials 时。长连接开始时用 WatchCredential 关注调用者，收到信号就立即执行 CheckCredential。
func WithCredentialWatcher(ctx context.Context, w CredentialWatcher) context.Context {
	return context.WithValue(ctx, watcherKey{}, w)
}

// WatchCredential 用 ctx 中的 CredentialWatcher 关注 ctx 中主体的凭据，
// 没有 CredentialWatcher 或主体时返回 nil（从 nil 通道接收会一直阻塞，select 中可以直接使用）。
func WatchCredential(ctx context.Context) <-chan struct{} {
	w, _ := ctx.Value(watcherKey{}).(CredentialWatcher)
	sub, ok := FromContext(ctx)
	if w == nil || !ok {
		return nil
	}
	return w.Watch(ctx, sub)
}
