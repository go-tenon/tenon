package account

import (
	"context"
	"database/sql"
	"time"
	"uuid"

	"github.com/cockroachdb/errors"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/i18n"
	"github.com/ryan053857/go-kit/messaging"
	"github.com/ryan053857/go-kit/support/password"
	"github.com/ryan053857/go-kit/support/random"
)

// TopicPasswordReset 是请求重置密码后发布的消息，payload 为 PasswordResetRequested。
// 产品必须注册它的处理函数，通常是发送一条包含重置链接的通知（示例中有默认实现），
// 否则 RequestPasswordReset 会失败：
//
//	messaging.Handle(a.Bus, account.TopicPasswordReset, func(ctx context.Context, e account.PasswordResetRequested) error {
//		u, err := loadUser(ctx, e.UserID)
//		...
//		return notifier.Send(ctx, u.Recipient(), notifications.ResetPassword{Token: e.Token, ExpiresAt: e.ExpiresAt})
//	})
const TopicPasswordReset = "account_password_reset"

// PasswordResetRequested 是 TopicPasswordReset 消息的内容。Token 为明文，只用于拼接重置链接。
// Locale 为用户的语言（未设置时为发起请求时的语言）。
type PasswordResetRequested struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	Locale    string    `json:"locale"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// 同一账号两次重置请求的最小间隔，期间的请求被忽略，防止被用来轰炸邮箱。
const resetThrottle = time.Minute

// RequestPasswordReset 为邮箱对应的账号生成重置令牌，并在同一事务中发布 TopicPasswordReset 消息。
// 邮箱不存在、账号已停用或请求过于频繁时什么也不做并返回 nil，调用方无法据此判断邮箱是否已注册。
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	u := new(User)
	err := s.db.NewSelect().Model(u).Where("email = ? AND disabled_at IS NULL", NormalizeEmail(email)).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "account: request password reset")
	}

	now := s.now()
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		recent, err := tx.NewSelect().Model((*resetToken)(nil)).
			Where("user_id = ? AND created_at > ?", u.ID, now.Add(-resetThrottle)).Exists(ctx)
		if err != nil {
			return errors.Wrap(err, "account: request password reset")
		}
		if recent {
			return nil
		}
		_, err = tx.NewDelete().Model((*resetToken)(nil)).
			Where("user_id = ? AND (expires_at <= ? OR used_at IS NOT NULL)", u.ID, now).Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "account: request password reset")
		}

		token := random.Token(32)
		rt := &resetToken{UserID: u.ID, TokenHash: random.HashToken(token), ExpiresAt: now.Add(s.cfg.ResetTTL)}
		rt.CreatedAt = now // 与限频判断使用同一个时钟
		if _, err := tx.NewInsert().Model(rt).Exec(ctx); err != nil {
			return errors.Wrap(err, "account: request password reset")
		}
		return messaging.PublishTx(ctx, tx.Tx, TopicPasswordReset, PasswordResetRequested{
			UserID: u.ID, Email: u.Email, Locale: cmpOr(u.Locale, i18n.Locale(ctx)), Token: token, ExpiresAt: rt.ExpiresAt,
		})
	})
}

// ResetPassword 用重置令牌设置新密码，令牌随即失效，并注销该账号的全部会话。
// 令牌无效、已使用或已过期时返回 ErrInvalidLink；密码过短时返回 ErrWeakPassword。
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if err := s.CheckPassword(newPassword); err != nil {
		return err
	}
	hash := password.Hash(newPassword)
	now := s.now()
	var userID uuid.UUID
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		rt := new(resetToken)
		err := tx.NewSelect().Model(rt).
			Where("token_hash = ? AND used_at IS NULL AND expires_at > ?", random.HashToken(token), now).
			For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidLink
		}
		if err != nil {
			return err
		}
		res, err := tx.NewUpdate().Model((*User)(nil)).
			Set("password_hash = ?", hash).Set("updated_at = ?", now).
			Where("id = ? AND disabled_at IS NULL", rt.UserID).Exec(ctx)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrInvalidLink
		}
		rt.UsedAt = now
		userID = rt.UserID
		if _, err := tx.NewUpdate().Model(rt).Column("used_at").WherePK().Exec(ctx); err != nil {
			return err
		}
		_, err = tx.NewDelete().Model((*Session)(nil)).Where("user_id = ?", rt.UserID).Exec(ctx)
		return err
	})
	if err != nil && !errors.Is(err, ErrInvalidLink) {
		return errors.Wrap(err, "account: reset password")
	}
	if err == nil {
		s.emit(ctx, Event{Type: EventPasswordReset, UserID: userID})
	}
	return err
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// cmpOr 返回第一个非空字符串。
func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
