package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/support/random"
)

// Principal 是已认证的调用方：网页会话与 access token 认证得到的都是它。
// 用 auth.From[*account.Principal](ctx) 取出；需要账号详情时用 Service.User 查询。
type Principal struct {
	ID        uuid.UUID // 用户 ID
	SessionID uuid.UUID // 会话 ID
}

// UserID 实现 auth.Principal。
func (p *Principal) UserID() string { return p.ID.String() }

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
func (s *Service) Login(ctx context.Context, email, plainPassword string, remember bool, client ClientInfo) (*Tokens, error) {
	u, err := s.Authenticate(ctx, email, plainPassword)
	if err != nil {
		return nil, err
	}
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
		return nil, ErrInvalidToken
	}
	if err != nil {
		if errors.Is(err, ErrInvalidToken) {
			return nil, err
		}
		return nil, fmt.Errorf("account: refresh: %w", err)
	}
	return tokens, nil
}

// VerifyAccessToken 校验 access token，供 rest.Authorize 使用：
//
//	api.UseMiddleware(rest.Authorize(api, users.VerifyAccessToken))
//
// token 无效或过期时返回 auth.ErrUnauthenticated。Config.CheckSession 为 true 时还会确认会话仍存在。
func (s *Service) VerifyAccessToken(ctx context.Context, token string) (*Principal, error) {
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
			return nil, fmt.Errorf("account: verify session: %w", err)
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
		return nil, fmt.Errorf("account: list sessions: %w", err)
	}
	return list, nil
}

// RevokeSession 注销用户的一个会话（退出登录、踢下线）。会话不存在时返回 ErrNotFound。
// 已签发的 access token 在过期前仍然有效，除非开启了 Config.CheckSession。
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	res, err := s.db.NewDelete().Model((*Session)(nil)).
		Where("id = ? AND user_id = ?", sessionID, userID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("account: revoke session: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// createSession 插入会话，并顺便清理该用户已过期的会话。
func (s *Service) createSession(ctx context.Context, sess *Session, client ClientInfo) error {
	now := s.now()
	sess.IP, sess.UserAgent = client.IP, truncate(client.UserAgent, 255)
	sess.LastSeenAt = now
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewDelete().Model((*Session)(nil)).
			Where("user_id = ? AND expires_at <= ?", sess.UserID, now).Exec(ctx)
		if err != nil {
			return fmt.Errorf("account: create session: %w", err)
		}
		if _, err := tx.NewInsert().Model(sess).Exec(ctx); err != nil {
			return fmt.Errorf("account: create session: %w", err)
		}
		return nil
	})
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
		return nil, fmt.Errorf("account: sign access token: %w", err)
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
