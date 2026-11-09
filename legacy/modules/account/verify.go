package account

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"time"
	"uuid"

	"github.com/cockroachdb/errors"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/config"
	"github.com/ryan053857/go-kit/i18n"
	"github.com/ryan053857/go-kit/messaging"
)

// TopicEmailVerification 是需要发送邮箱验证邮件时发布的消息，payload 为 EmailVerificationRequested。
// 使用邮箱验证时产品必须注册它的处理函数，通常是发送一条包含验证链接的通知（示例中有默认实现）。
const TopicEmailVerification = "account_email_verification"

// EmailVerificationRequested 是 TopicEmailVerification 消息的内容。Token 只用于拼接验证链接。
type EmailVerificationRequested struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	Locale    string    `json:"locale"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RequestEmailVerification 发布 TopicEmailVerification 消息，向用户的邮箱发送验证链接。
// 邮箱已验证时什么也不做。验证链接不保存在数据库中（签名 token），修改邮箱后旧链接自动失效。
// 本方法不限频，对应的接口需要在产品中限流。
func (s *Service) RequestEmailVerification(ctx context.Context, userID uuid.UUID) error {
	u, err := s.User(ctx, userID)
	if err != nil {
		return err
	}
	if u.EmailVerified() {
		return nil
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return s.publishVerification(ctx, tx, u)
	})
}

func (s *Service) publishVerification(ctx context.Context, tx bun.Tx, u *User) error {
	exp := s.now().Add(s.cfg.VerifyTTL).Truncate(time.Second)
	return messaging.PublishTx(ctx, tx.Tx, TopicEmailVerification, EmailVerificationRequested{
		UserID: u.ID, Email: u.Email, Locale: cmpOr(u.Locale, i18n.Locale(ctx)), Token: s.emailToken(u.ID, u.Email, exp), ExpiresAt: exp,
	})
}

// VerifyEmail 校验验证链接中的 token 并标记邮箱已验证，返回账号。
// token 无效、过期或邮箱已修改时返回 ErrInvalidLink；已验证过时直接返回账号。
func (s *Service) VerifyEmail(ctx context.Context, token string) (*User, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 16+8+16 {
		return nil, ErrInvalidLink
	}
	id := uuid.UUID(raw[:16])
	exp := time.Unix(int64(binary.BigEndian.Uint64(raw[16:24])), 0)
	if !s.now().Before(exp) {
		return nil, ErrInvalidLink
	}
	u, err := s.User(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidLink
	}
	if err != nil {
		return nil, err
	}
	if !hmac.Equal([]byte(s.emailToken(u.ID, u.Email, exp)), []byte(token)) {
		return nil, ErrInvalidLink
	}
	if u.EmailVerified() {
		return u, nil
	}
	u.EmailVerifiedAt = s.now()
	if _, err := s.db.NewUpdate().Model(u).Column("email_verified_at", "updated_at").WherePK().Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "account: verify email")
	}
	s.emit(ctx, Event{Type: EventEmailVerified, UserID: u.ID})
	return u, nil
}

// emailToken = base64url(用户 ID | 过期时间 | HMAC(用户 ID | 过期时间 | 邮箱) 的前 16 字节)。
func (s *Service) emailToken(userID uuid.UUID, email string, exp time.Time) string {
	buf := make([]byte, 0, 40)
	buf = append(buf, userID[:]...)
	buf = binary.BigEndian.AppendUint64(buf, uint64(exp.Unix()))
	mac := hmac.New(sha256.New, deriveKey(s.cfg.JWTSecret, "verify-email"))
	mac.Write(buf)
	mac.Write([]byte(email))
	buf = append(buf, mac.Sum(nil)[:16]...)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// deriveKey 从主密钥派生出某个用途的 32 字节子密钥，不同用途的签名互不通用。
func deriveKey(secret config.Secret, purpose string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("go-kit/account/" + purpose))
	return mac.Sum(nil)
}
