// Package authz 是授权：谁能做什么。类似 Laravel 的 Gate 与 Policy，能力（ability）用字符串命名，
// 规则由产品定义：
//
//	gate := authz.New()
//
//	// 不针对具体对象的能力。
//	gate.Define("audit.view", func(ctx context.Context, user uuid.UUID) bool { return isAdmin(ctx, user) })
//
//	// 针对某类对象的能力（类似 Policy），对象类型由泛型参数确定。
//	authz.DefineFor(gate, "order.update", func(ctx context.Context, user uuid.UUID, o *Order) bool {
//		return o.UserID == user
//	})
//
//	// 在所有规则之前执行，例如角色权限（modules/role）、超级管理员。
//	gate.Before(func(ctx context.Context, user uuid.UUID, ability string) (allowed, decided bool) { ... })
//
// 检查：
//
//	if err := gate.Authorize(ctx, "order.update", order); err != nil {
//		return err // 未登录为 auth.ErrUnauthenticated（401），没有权限为 authz.ErrForbidden（403）
//	}
//
// 当前用户取自 auth.Current(ctx)。调用方的 Principal 实现了 auth.ScopeChecker 时（例如限定了 scope 的 API Key），
// 还要求它具备与能力同名的 scope。未定义的能力一律拒绝。
//
// REST 接口在 Operation.Security 中声明能力（rest.RequireAuth("audit.view")），由 rest.Authorize 检查；
// 页面路由用 Require 中间件。
package authz

import (
	"context"
	"embed"
	"fmt"
	"net/http"
	"reflect"
	"uuid"

	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/i18n"
)

//go:embed locales/*.yaml
var locales embed.FS

func init() { i18n.MustLoad(locales) }

// ErrForbidden 表示已登录但没有权限。
var ErrForbidden = httpx.DefineError(http.StatusForbidden, "authz.forbidden")

// BeforeFunc 在规则之前执行，decided 为 true 时直接采用 allowed。
type BeforeFunc func(ctx context.Context, user uuid.UUID, ability string) (allowed, decided bool)

type rule struct {
	typ reflect.Type // nil 表示不针对对象
	fn  func(ctx context.Context, user uuid.UUID, obj any) bool
}

// Gate 保存能力的规则。规则在启动时定义，之后可以被多个 goroutine 并发使用。
type Gate struct {
	rules  map[string]rule
	before []BeforeFunc
}

// New 返回空的 Gate。
func New() *Gate { return &Gate{rules: map[string]rule{}} }

// Define 定义不针对具体对象的能力。重复定义时 panic。
func (g *Gate) Define(ability string, fn func(ctx context.Context, user uuid.UUID) bool) {
	g.define(ability, rule{fn: func(ctx context.Context, user uuid.UUID, _ any) bool { return fn(ctx, user) }})
}

// DefineFor 定义针对 T 类型对象的能力。检查时必须传入 T 类型的对象，否则拒绝。
func DefineFor[T any](g *Gate, ability string, fn func(ctx context.Context, user uuid.UUID, obj T) bool) {
	g.define(ability, rule{typ: reflect.TypeFor[T](), fn: func(ctx context.Context, user uuid.UUID, obj any) bool {
		o, ok := obj.(T)
		return ok && fn(ctx, user, o)
	}})
}

func (g *Gate) define(ability string, r rule) {
	if _, ok := g.rules[ability]; ok {
		panic(fmt.Sprintf("authz: ability %q already defined", ability))
	}
	g.rules[ability] = r
}

// Before 注册在所有规则之前执行的函数，按注册顺序执行，第一个 decided 的结果生效。
func (g *Gate) Before(fn BeforeFunc) { g.before = append(g.before, fn) }

// Allows 报告当前用户是否具有能力 ability；obj 为针对的对象（DefineFor 定义的能力需要）。
func (g *Gate) Allows(ctx context.Context, ability string, obj ...any) bool {
	return g.Authorize(ctx, ability, obj...) == nil
}

// Authorize 检查当前用户是否具有能力 ability：未登录时返回 auth.ErrUnauthenticated，
// 没有权限时返回 ErrForbidden。
func (g *Gate) Authorize(ctx context.Context, ability string, obj ...any) error {
	p, ok := auth.Current(ctx)
	if !ok {
		return auth.ErrUnauthenticated
	}
	if sc, ok := p.(auth.ScopeChecker); ok && !sc.HasScope(ability) {
		return ErrForbidden
	}
	user := p.UserID()
	for _, fn := range g.before {
		if allowed, decided := fn(ctx, user, ability); decided {
			if allowed {
				return nil
			}
			return ErrForbidden
		}
	}
	r, ok := g.rules[ability]
	if !ok {
		return ErrForbidden
	}
	var o any
	if len(obj) > 0 {
		o = obj[0]
	}
	if r.typ != nil && o == nil {
		return ErrForbidden
	}
	if !r.fn(ctx, user, o) {
		return ErrForbidden
	}
	return nil
}

// Require 返回页面路由使用的中间件：当前用户没有能力 ability 时，交给 web.ErrorHandler 输出 401/403。
// 通常挂在 auth.RequireLogin 之后。
func Require(g *Gate, ability string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return web.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			if err := g.Authorize(r.Context(), ability); err != nil {
				return err
			}
			next.ServeHTTP(w, r)
			return nil
		})
	}
}
