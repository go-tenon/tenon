package account

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"html/template"
	"time"
	"uuid"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/config"
	"github.com/ryan053857/go-kit/mail"
	"github.com/ryan053857/go-kit/messaging"
)

// TopicEmailVerification 是需要发送邮箱验证邮件时发布的消息，payload 为 EmailVerificationRequested。
// 使用邮箱验证时产品必须注册它的处理函数（通常是 EmailVerificationMail）。
const TopicEmailVerification = "account_email_verification"

// EmailVerificationRequested 是 TopicEmailVerification 消息的内容。Token 只用于拼接验证链接。
type EmailVerificationRequested struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
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
		UserID: u.ID, Email: u.Email, Name: u.Name, Token: s.emailToken(u.ID, u.Email, exp), ExpiresAt: exp,
	})
}

// VerifyEmail 校验验证链接中的 token 并标记邮箱已验证，返回账号。
// token 无效、过期或邮箱已修改时返回 ErrInvalidToken；已验证过时直接返回账号。
func (s *Service) VerifyEmail(ctx context.Context, token string) (*User, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 16+8+16 {
		return nil, ErrInvalidToken
	}
	id := uuid.UUID(raw[:16])
	exp := time.Unix(int64(binary.BigEndian.Uint64(raw[16:24])), 0)
	if !s.now().Before(exp) {
		return nil, ErrInvalidToken
	}
	u, err := s.User(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	if !hmac.Equal([]byte(s.emailToken(u.ID, u.Email, exp)), []byte(token)) {
		return nil, ErrInvalidToken
	}
	if u.EmailVerified() {
		return u, nil
	}
	u.EmailVerifiedAt = s.now()
	if _, err := s.db.NewUpdate().Model(u).Column("email_verified_at", "updated_at").WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("account: verify email: %w", err)
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

// EmailVerificationMail 是 TopicEmailVerification 的处理函数：发送包含验证链接的邮件。
//
//	err := messaging.Handle(a.Bus, account.TopicEmailVerification, account.EmailVerificationMail{
//		Mailer: a.Mailer,
//		URL:    func(token string) string { return cfg.BaseURL + "/verify-email?token=" + url.QueryEscape(token) },
//	}.Handle)
type EmailVerificationMail struct {
	Mailer mail.Mailer
	// URL 返回验证页面的地址，页面由产品实现，打开时调用 VerifyEmail 或对应的 Endpoint。
	URL func(token string) string
	// Compose 可选，自定义邮件内容；为 nil 时使用默认的中文邮件。
	Compose func(e EmailVerificationRequested, url string) mail.Message
}

// Handle 发送验证邮件，供 messaging.Handle 注册。
func (h EmailVerificationMail) Handle(ctx context.Context, e EmailVerificationRequested) error {
	link := h.URL(e.Token)
	compose := h.Compose
	if compose == nil {
		compose = defaultVerifyMail
	}
	return h.Mailer.Send(ctx, compose(e, link))
}

var verifyHTML = template.Must(template.New("verify").Parse(`<p>{{if .Name}}{{.Name}}，{{end}}您好：</p>
<p>请点击下面的链接验证您的邮箱，链接在 {{.Hours}} 小时内有效：</p>
<p><a href="{{.URL}}">{{.URL}}</a></p>
<p>如果您没有注册账号，请忽略本邮件。</p>`))

func defaultVerifyMail(e EmailVerificationRequested, link string) mail.Message {
	hours := max(int(time.Until(e.ExpiresAt).Round(time.Hour).Hours()), 1)
	data := struct {
		Name, URL string
		Hours     int
	}{e.Name, link, hours}
	var html bytes.Buffer
	_ = verifyHTML.Execute(&html, data)

	greeting := "您好："
	if e.Name != "" {
		greeting = e.Name + "，您好："
	}
	text := fmt.Sprintf("%s\n\n请打开下面的链接验证您的邮箱，链接在 %d 小时内有效：\n\n%s\n\n如果您没有注册账号，请忽略本邮件。\n",
		greeting, hours, link)
	return mail.Message{To: []string{e.Email}, Subject: "验证邮箱", Text: text, HTML: html.String()}
}
