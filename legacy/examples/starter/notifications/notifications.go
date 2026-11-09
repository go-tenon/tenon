// Package notifications 是本产品的通知类型（go-kit 的 notification 模块负责分发与渠道）。
// 每种通知实现 ToDatabase（站内信）和/或 ToMail（邮件），文字通过 i18n 按收件人的语言生成。
package notifications

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"time"

	"github.com/ryan053857/go-kit/i18n"
	"github.com/ryan053857/go-kit/mail"
	"github.com/ryan053857/go-kit/modules/notification"
)

//go:embed locales/*.yaml
var locales embed.FS

func init() { i18n.MustLoad(locales) }

// Register 登记本产品的全部通知类型。
func Register(s *notification.Service) {
	notification.Register[Welcome](s)
	notification.Register[ResetPassword](s)
	notification.Register[VerifyEmail](s)
	notification.Register[ExportDone](s)
	notification.Register[ExportFailed](s)
}

// Welcome 是注册后的欢迎站内信。
type Welcome struct{}

func (Welcome) Type() string { return "welcome" }

func (Welcome) ToDatabase(ctx context.Context, _ notification.Recipient) (notification.Content, error) {
	return notification.Content{Title: i18n.T(ctx, "notify.welcome.title"), Body: i18n.T(ctx, "notify.welcome.body"),
		URL: "/settings/security"}, nil
}

// ResetPassword 是重置密码邮件。
type ResetPassword struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (ResetPassword) Type() string { return "reset_password" }

func (n ResetPassword) ToMail(ctx context.Context, r notification.Recipient) (mail.Message, error) {
	minutes := max(int(time.Until(n.ExpiresAt).Round(time.Minute).Minutes()), 1)
	return linkMail(ctx, r, i18n.T(ctx, "notify.reset.subject"),
		i18n.T(ctx, "notify.reset.intro", map[string]any{"Minutes": minutes}), n.URL, i18n.T(ctx, "notify.reset.ignore"))
}

// VerifyEmail 是邮箱验证邮件。
type VerifyEmail struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (VerifyEmail) Type() string { return "verify_email" }

func (n VerifyEmail) ToMail(ctx context.Context, r notification.Recipient) (mail.Message, error) {
	hours := max(int(time.Until(n.ExpiresAt).Round(time.Hour).Hours()), 1)
	return linkMail(ctx, r, i18n.T(ctx, "notify.verify.subject"),
		i18n.T(ctx, "notify.verify.intro", map[string]any{"Hours": hours}), n.URL, i18n.T(ctx, "notify.verify.ignore"))
}

// ExportDone 是后台导出完成的站内信。
type ExportDone struct {
	File string `json:"file"`
	URL  string `json:"url"`
}

func (ExportDone) Type() string { return "export.done" }

func (n ExportDone) ToDatabase(ctx context.Context, _ notification.Recipient) (notification.Content, error) {
	return notification.Content{Title: i18n.T(ctx, "notify.export_done.title", map[string]any{"File": n.File}), URL: n.URL}, nil
}

// ExportFailed 是后台导出失败的站内信。
type ExportFailed struct {
	File string `json:"file"`
}

func (ExportFailed) Type() string { return "export.failed" }

func (n ExportFailed) ToDatabase(ctx context.Context, _ notification.Recipient) (notification.Content, error) {
	return notification.Content{Title: i18n.T(ctx, "notify.export_failed.title", map[string]any{"File": n.File}),
		Body: i18n.T(ctx, "notify.export_failed.body")}, nil
}

var linkHTML = template.Must(template.New("link").Parse(`<p>{{.Greeting}}</p>
<p>{{.Intro}}</p>
<p><a href="{{.URL}}">{{.URL}}</a></p>
<p>{{.Ignore}}</p>`))

// linkMail 生成带一个链接的邮件。
func linkMail(ctx context.Context, r notification.Recipient, subject, intro, url, ignore string) (mail.Message, error) {
	greeting := i18n.T(ctx, "notify.greeting", map[string]any{"Name": r.Name})
	var html bytes.Buffer
	err := linkHTML.Execute(&html, map[string]string{"Greeting": greeting, "Intro": intro, "URL": url, "Ignore": ignore})
	if err != nil {
		return mail.Message{}, err
	}
	text := greeting + "\n\n" + intro + "\n\n" + url + "\n\n" + ignore + "\n"
	return mail.Message{Subject: subject, Text: text, HTML: html.String()}, nil
}
