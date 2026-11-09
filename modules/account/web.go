package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
	"uuid"

	"github.com/alexedwards/scs/v2"

	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/httpx/auth"
)

const webSessionKey = "account.session_id"

// 网页会话的最近活跃时间最多每隔这么久更新一次，避免每个请求都写库。
const touchInterval = 5 * time.Minute

// WebLogin 为已通过 Authenticate 的用户创建网页会话：在 user_sessions 中记录一行，
// 更换 Cookie 会话的 token（防会话固定攻击）并保存会话 ID。会话有效期与 sm.Lifetime 相同。
//
//	u, err := users.Authenticate(ctx, email, password)
//	...
//	err = users.WebLogin(ctx, a.Sessions, u, r)
func (s *Service) WebLogin(ctx context.Context, sm *scs.SessionManager, u *User, r *http.Request) error {
	sess := &Session{UserID: u.ID, Kind: KindWeb, ExpiresAt: s.now().Add(sm.Lifetime)}
	if err := s.createSession(ctx, sess, ClientInfo{IP: httpx.ClientIP(r), UserAgent: r.UserAgent()}); err != nil {
		return err
	}
	if err := sm.RenewToken(ctx); err != nil {
		return fmt.Errorf("account: web login: %w", err)
	}
	sm.Put(ctx, webSessionKey, sess.ID.String())
	return nil
}

// WebLogout 注销当前网页会话并销毁 Cookie 会话。
func (s *Service) WebLogout(ctx context.Context, sm *scs.SessionManager) error {
	if p, ok := auth.From[*Principal](ctx); ok {
		if err := s.RevokeSession(ctx, p.ID, p.SessionID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if err := sm.Destroy(ctx); err != nil {
		return fmt.Errorf("account: web logout: %w", err)
	}
	return nil
}

// LoadWebSession 返回全局中间件：Cookie 会话中有有效的网页会话时，把 *Principal 放入 context。
// 会话已被注销（踢下线、修改密码）、过期或账号已停用时按未登录处理。挂在 sm.LoadAndSave 之后：
//
//	a.Router.Use(users.LoadWebSession(a.Sessions))
func (s *Service) LoadWebSession(sm *scs.SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			raw := sm.GetString(ctx, webSessionKey)
			if raw == "" {
				next.ServeHTTP(w, r)
				return
			}
			id, _ := uuid.Parse(raw)
			sess := new(Session)
			err := s.db.NewSelect().Model(sess).
				Where("s.id = ? AND s.kind = ? AND s.expires_at > ?", id, KindWeb, s.now()).
				Where("EXISTS (SELECT 1 FROM users WHERE users.id = s.user_id AND users.disabled_at IS NULL)").
				Limit(1).Scan(ctx)
			if err != nil {
				if !isNoRows(err) {
					slog.ErrorContext(ctx, "load web session", "err", err)
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					return
				}
				sm.Remove(ctx, webSessionKey)
				next.ServeHTTP(w, r)
				return
			}
			if now := s.now(); now.Sub(sess.LastSeenAt) > touchInterval {
				sess.LastSeenAt = now
				if _, err := s.db.NewUpdate().Model(sess).Column("last_seen_at").WherePK().Exec(ctx); err != nil {
					slog.WarnContext(ctx, "touch web session", "err", err)
				}
			}
			ctx = auth.WithPrincipal(ctx, &Principal{ID: sess.UserID, SessionID: sess.ID})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
