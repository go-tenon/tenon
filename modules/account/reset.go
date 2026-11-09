package account

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"time"
	"uuid"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/mail"
	"github.com/ryan053857/go-kit/messaging"
	"github.com/ryan053857/go-kit/support/password"
	"github.com/ryan053857/go-kit/support/random"
)

// TopicPasswordReset 是请求重置密码后发布的消息，payload 为 PasswordResetRequested。
// 产品必须注册它的处理函数（通常是 PasswordResetMail），否则 RequestPasswordReset 会失败：
//
//	messaging.Handle(a.Bus, account.TopicPasswordReset, account.PasswordResetMail{...}.Handle)
const TopicPasswordReset = "account_password_reset"

// PasswordResetRequested 是 TopicPasswordReset 消息的内容。Token 为明文，只用于拼接重置链接。
type PasswordResetRequested struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// 同一账号两次重置请求的最小间隔，期间的请求被忽略，防止被用来轰炸邮箱。
const resetThrottle = time.Minute

// RequestPasswordReset 为邮箱对应的账号生成重置令牌，并在同一事务中发布 TopicPasswordReset 消息。
// 邮箱不存在、账号已停用或请求过于频繁时什么也不做并返回 nil，调用方无法据此判断邮箱是否已注册。
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	u := new(User)
	err := s.db.NewSelect().Model(u).Where("email = ? AND disabled_at IS NULL", normalizeEmail(email)).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("account: request password reset: %w", err)
	}

	now := s.now()
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		recent, err := tx.NewSelect().Model((*resetToken)(nil)).
			Where("user_id = ? AND created_at > ?", u.ID, now.Add(-resetThrottle)).Exists(ctx)
		if err != nil {
			return fmt.Errorf("account: request password reset: %w", err)
		}
		if recent {
			return nil
		}
		_, err = tx.NewDelete().Model((*resetToken)(nil)).
			Where("user_id = ? AND (expires_at <= ? OR used_at IS NOT NULL)", u.ID, now).Exec(ctx)
		if err != nil {
			return fmt.Errorf("account: request password reset: %w", err)
		}

		token := random.Token(32)
		rt := &resetToken{UserID: u.ID, TokenHash: random.HashToken(token), ExpiresAt: now.Add(s.cfg.ResetTTL)}
		rt.CreatedAt = now // 与限频判断使用同一个时钟
		if _, err := tx.NewInsert().Model(rt).Exec(ctx); err != nil {
			return fmt.Errorf("account: request password reset: %w", err)
		}
		return messaging.PublishTx(ctx, tx.Tx, TopicPasswordReset, PasswordResetRequested{
			UserID: u.ID, Email: u.Email, Name: u.Name, Token: token, ExpiresAt: rt.ExpiresAt,
		})
	})
}

// ResetPassword 用重置令牌设置新密码，令牌随即失效，并注销该账号的全部会话。
// 令牌无效、已使用或已过期时返回 ErrInvalidToken；密码过短时返回 ErrWeakPassword。
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if err := s.checkPassword(newPassword); err != nil {
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
			return ErrInvalidToken
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
			return ErrInvalidToken
		}
		rt.UsedAt = now
		userID = rt.UserID
		if _, err := tx.NewUpdate().Model(rt).Column("used_at").WherePK().Exec(ctx); err != nil {
			return err
		}
		_, err = tx.NewDelete().Model((*Session)(nil)).Where("user_id = ?", rt.UserID).Exec(ctx)
		return err
	})
	if err != nil && !errors.Is(err, ErrInvalidToken) {
		return fmt.Errorf("account: reset password: %w", err)
	}
	if err == nil {
		s.emit(ctx, Event{Type: EventPasswordReset, UserID: userID})
	}
	return err
}

// PasswordResetMail 是 TopicPasswordReset 的处理函数：发送包含重置链接的邮件。
//
//	err := messaging.Handle(a.Bus, account.TopicPasswordReset, account.PasswordResetMail{
//		Mailer: a.Mailer,
//		URL:    func(token string) string { return cfg.BaseURL + "/reset-password?token=" + url.QueryEscape(token) },
//	}.Handle)
type PasswordResetMail struct {
	Mailer mail.Mailer
	// URL 返回重置密码页面的地址，页面由产品实现，提交时调用 ResetPassword 或对应的 Endpoint。
	URL func(token string) string
	// Compose 可选，自定义邮件内容；为 nil 时使用默认的中文邮件。
	Compose func(e PasswordResetRequested, url string) mail.Message
}

// Handle 发送重置邮件，供 messaging.Handle 注册。
func (h PasswordResetMail) Handle(ctx context.Context, e PasswordResetRequested) error {
	link := h.URL(e.Token)
	compose := h.Compose
	if compose == nil {
		compose = defaultResetMail
	}
	return h.Mailer.Send(ctx, compose(e, link))
}

var resetHTML = template.Must(template.New("reset").Parse(`<p>{{if .Name}}{{.Name}}，{{end}}您好：</p>
<p>我们收到了重置您账号密码的请求。请点击下面的链接设置新密码，链接在 {{.Minutes}} 分钟内有效：</p>
<p><a href="{{.URL}}">{{.URL}}</a></p>
<p>如果这不是您本人的操作，请忽略本邮件，您的密码不会改变。</p>`))

func defaultResetMail(e PasswordResetRequested, link string) mail.Message {
	minutes := int(time.Until(e.ExpiresAt).Round(time.Minute).Minutes())
	if minutes < 1 {
		minutes = 1
	}
	data := struct {
		Name, URL string
		Minutes   int
	}{e.Name, link, minutes}
	var html bytes.Buffer
	_ = resetHTML.Execute(&html, data)

	greeting := "您好："
	if e.Name != "" {
		greeting = e.Name + "，您好："
	}
	text := fmt.Sprintf("%s\n\n我们收到了重置您账号密码的请求。请打开下面的链接设置新密码，链接在 %d 分钟内有效：\n\n%s\n\n如果这不是您本人的操作，请忽略本邮件，您的密码不会改变。\n",
		greeting, minutes, link)
	return mail.Message{To: []string{e.Email}, Subject: "重置密码", Text: text, HTML: html.String()}
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
