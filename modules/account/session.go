package account

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/cockroachdb/errors"

	"github.com/golang-jwt/jwt/v5"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/support/random"
)

// Principal 是已认证的调用方：网页会话、access token 与 API Key 认证得到的都是它。
// 用 auth.From[*account.Principal](ctx) 取出；需要账号详情时用 Service.User 查询。
type Principal struct {
	ID        uuid.UUID // 用户 ID
	SessionID uuid.UUID // 会话 ID；API Key 认证时为零值
	APIKeyID  uuid.UUID // API Key 认证时为 Key 的 ID，否则为零值
	Scopes    []string  // API Key 的 scope，为空表示不限
}

// UserID 实现 auth.Principal。
func (p *Principal) UserID() uuid.UUID { return p.ID }

// HasScope 实现 auth.ScopeChecker（scope 即 authz 的能力）：网页会话与 access token 不受限制；
// API Key 未限定 scope 时不受限制，否则只能使用创建时指定的能力。用户是否具有该能力仍由 authz 判断。
func (p *Principal) HasScope(scope string) bool {
	return len(p.Scopes) == 0 || slices.Contains(p.Scopes, scope)
}

// IsAPIKey 报告调用方是否用 API Key 认证。修改密码、管理 API Key 等敏感接口不接受 API Key。
func (p *Principal) IsAPIKey() bool { return p.APIKeyID != uuid.Nil() }

// Tokens 是登录或刷新后发给客户端的凭证。
type Tokens struct {
	UserID           uuid.UUID
	SessionID        uuid.UUID
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// ClientInfo 是登录、刷新时记录在会话上的客户端信息。
type ClientInfo struct {
	IP        string
	UserAgent string
}

// Login 校验邮箱密码并创建一个 API 会话，返回 access token 与 refresh token。
// remember 为 true 时 refresh token 有效期为 RememberTTL，否则为 RefreshTTL。
// 账号启用了两步验证时返回 *MFARequiredError，客户端需再调用 LoginMFA。
func (s *Service) Login(ctx context.Context, email, plainPassword string, remember bool, client ClientInfo) (*Tokens, error) {
	u, err := s.Authenticate(ctx, email, plainPassword)
	if err != nil {
		return nil, err
	}
	st, err := s.MFAStatus(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if st.Enabled {
		challenge, err := s.mfaChallenge(u.ID, remember)
		if err != nil {
			return nil, errors.Wrap(err, "account: login")
		}
		return nil, &MFARequiredError{UserID: u.ID, Challenge: challenge}
	}
	return s.startAPISession(ctx, u, remember, client)
}

// startAPISession 为已通过认证的用户创建 API 会话并签发凭证。
func (s *Service) startAPISession(ctx context.Context, u *User, remember bool, client ClientInfo) (*Tokens, error) {
	refresh := random.Token(32)
	sess := &Session{
		UserID:    u.ID,
		Kind:      KindAPI,
		TokenHash: random.HashToken(refresh),
		Remember:  remember,
		ExpiresAt: s.now().Add(s.refreshTTL(remember)),
	}
	if err := s.createSession(ctx, sess, client); err != nil {
		return nil, err
	}
	return s.issue(sess, refresh)
}

// Refresh 用 refresh token 换取新的 access token 与 refresh token（旧 refresh token 随之失效）。
//
// 旧 token 在 RefreshGrace 内仍可再换一次，应对响应丢失后的重试；超过宽限期再使用旧 token
// 视为 token 泄露，整个会话被注销。token 无效、过期或账号已停用时返回 ErrInvalidToken。
// 同一客户端应串行调用 Refresh。
func (s *Service) Refresh(ctx context.Context, refreshToken string, client ClientInfo) (*Tokens, error) {
	hash := random.HashToken(refreshToken)
	now := s.now()
	var (
		tokens *Tokens
		reused *Session
	)
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		sess := new(Session)
		err := tx.NewSelect().Model(sess).Where("token_hash = ?", hash).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.NewSelect().Model(sess).Where("prev_token_hash = ?", hash).For("UPDATE").Scan(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrInvalidToken
			}
			if err == nil && now.Sub(sess.RotatedAt) > s.cfg.RefreshGrace {
				reused = sess
				_, err = tx.NewDelete().Model(sess).WherePK().Exec(ctx)
				return err
			}
		}
		if err != nil {
			return err
		}
		if !now.Before(sess.ExpiresAt) {
			return ErrInvalidToken
		}
		active, err := tx.NewSelect().Model((*User)(nil)).
			Where("id = ? AND disabled_at IS NULL", sess.UserID).Exists(ctx)
		if err != nil {
			return err
		}
		if !active {
			return ErrInvalidToken
		}

		refresh := random.Token(32)
		if sess.TokenHash == hash { // 正常轮换；宽限期内的重试保留原来的 prev_token_hash
			sess.PrevTokenHash = hash
		}
		sess.TokenHash = random.HashToken(refresh)
		sess.RotatedAt = now
		sess.LastSeenAt = now
		sess.ExpiresAt = now.Add(s.refreshTTL(sess.Remember))
		sess.IP, sess.UserAgent = client.IP, truncate(client.UserAgent, 255)
		_, err = tx.NewUpdate().Model(sess).
			Column("token_hash", "prev_token_hash", "rotated_at", "last_seen_at", "expires_at", "ip", "user_agent", "updated_at").
			WherePK().Exec(ctx)
		if err != nil {
			return err
		}
		tokens, err = s.issue(sess, refresh)
		return err
	})
	if reused != nil {
		slog.WarnContext(ctx, "refresh token reused after rotation, session revoked",
			"user_id", reused.UserID, "session_id", reused.ID)
		s.emit(ctx, Event{Type: EventTokenReused, UserID: reused.UserID, SessionID: reused.ID})
		return nil, ErrInvalidToken
	}
	if err != nil {
		if errors.Is(err, ErrInvalidToken) {
			return nil, err
		}
		return nil, errors.Wrap(err, "account: refresh")
	}
	return tokens, nil
}

// VerifyAccessToken 校验 Bearer token（access token 或 API Key），供 rest.Authorize 使用：
//
//	api.UseMiddleware(rest.Authorize(api, users.VerifyAccessToken, a.Gate))
//
// token 无效或过期时返回 auth.ErrUnauthenticated。Config.CheckSession 为 true 时还会确认会话仍存在。
func (s *Service) VerifyAccessToken(ctx context.Context, token string) (*Principal, error) {
	if strings.HasPrefix(token, s.cfg.APIKeyPrefix+"_") {
		return s.verifyAPIKey(ctx, token)
	}
	claims := new(accessClaims)
	_, err := jwt.ParseWithClaims(token, claims,
		func(*jwt.Token) (any, error) { return []byte(s.cfg.JWTSecret), nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(s.cfg.Issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil {
		return nil, auth.ErrUnauthenticated
	}
	p := new(Principal)
	if p.ID, err = uuid.Parse(claims.Subject); err != nil {
		return nil, auth.ErrUnauthenticated
	}
	if p.SessionID, err = uuid.Parse(claims.SID); err != nil {
		return nil, auth.ErrUnauthenticated
	}
	if s.cfg.CheckSession {
		ok, err := s.db.NewSelect().Model((*Session)(nil)).
			Where("id = ? AND user_id = ? AND expires_at > ?", p.SessionID, p.ID, s.now()).Exists(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "account: verify session")
		}
		if !ok {
			return nil, auth.ErrUnauthenticated
		}
	}
	return p, nil
}

// Sessions 返回用户未过期的会话（网页与 API），最近活跃的在前。
func (s *Service) Sessions(ctx context.Context, userID uuid.UUID) ([]Session, error) {
	list := []Session{}
	err := s.db.NewSelect().Model(&list).
		Where("user_id = ? AND expires_at > ?", userID, s.now()).
		Order("last_seen_at DESC").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "account: list sessions")
	}
	return list, nil
}

// RevokeSession 注销用户的一个会话（踢下线）。会话不存在时返回 ErrNotFound。
// 已签发的 access token 在过期前仍然有效，除非开启了 Config.CheckSession。
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	return s.revokeSession(ctx, userID, sessionID, EventSessionRevoked)
}

// Logout 注销调用方自己的当前会话（退出登录）。会话已不存在时也返回 nil。
func (s *Service) Logout(ctx context.Context, p *Principal) error {
	if p.SessionID == uuid.Nil() {
		return nil
	}
	err := s.revokeSession(ctx, p.ID, p.SessionID, EventLogout)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (s *Service) revokeSession(ctx context.Context, userID, sessionID uuid.UUID, event string) error {
	res, err := s.db.NewDelete().Model((*Session)(nil)).
		Where("id = ? AND user_id = ?", sessionID, userID).Exec(ctx)
	if err != nil {
		return errors.Wrap(err, "account: revoke session")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.emit(ctx, Event{Type: event, UserID: userID, SessionID: sessionID})
	return nil
}

// createSession 插入会话，并顺便清理该用户已过期的会话。
func (s *Service) createSession(ctx context.Context, sess *Session, client ClientInfo) error {
	now := s.now()
	sess.IP, sess.UserAgent = client.IP, truncate(client.UserAgent, 255)
	sess.LastSeenAt = now
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewDelete().Model((*Session)(nil)).
			Where("user_id = ? AND expires_at <= ?", sess.UserID, now).Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "account: create session")
		}
		if _, err := tx.NewInsert().Model(sess).Exec(ctx); err != nil {
			return errors.Wrap(err, "account: create session")
		}
		return nil
	})
	if err == nil {
		s.emit(ctx, Event{Type: EventLogin, UserID: sess.UserID, SessionID: sess.ID})
	}
	return err
}

type accessClaims struct {
	jwt.RegisteredClaims
	SID string `json:"sid"`
}

func (s *Service) issue(sess *Session, refresh string) (*Tokens, error) {
	now := s.now()
	exp := now.Add(s.cfg.AccessTTL)
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.Issuer,
			Subject:   sess.UserID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
		SID: sess.ID.String(),
	}).SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		return nil, errors.Wrap(err, "account: sign access token")
	}
	return &Tokens{
		UserID:           sess.UserID,
		SessionID:        sess.ID,
		AccessToken:      token,
		AccessExpiresAt:  exp,
		RefreshToken:     refresh,
		RefreshExpiresAt: sess.ExpiresAt,
	}, nil
}

func (s *Service) refreshTTL(remember bool) time.Duration {
	if remember {
		return s.cfg.RememberTTL
	}
	return s.cfg.RefreshTTL
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// LoadAccessToken 返回全局中间件：请求带有有效的 Authorization: Bearer access token 时，
// 把 *Principal 放入 context，使 huma 之外的普通路由（如文件下载）也能识别 App 的登录状态。
// token 无效时按未登录处理，不返回错误。
func (s *Service) LoadAccessToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, ok := auth.BearerToken(r.Header.Get("Authorization")); ok {
			if _, loaded := auth.From[*Principal](r.Context()); !loaded {
				if p, err := s.VerifyAccessToken(r.Context(), token); err == nil {
					r = r.WithContext(auth.WithPrincipal(r.Context(), p))
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
