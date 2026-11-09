package notification

import (
	"context"

	"github.com/cockroachdb/errors"

	"github.com/ryan053857/go-kit/mail"
)

// Content 是站内信的内容。
type Content struct {
	Title string
	Body  string
	URL   string // 点击后打开的地址
}

// DatabaseNotification 由要发送站内信的通知实现。
type DatabaseNotification interface {
	Notification
	ToDatabase(ctx context.Context, r Recipient) (Content, error)
}

// MailNotification 由要发送邮件的通知实现。
type MailNotification interface {
	Notification
	ToMail(ctx context.Context, r Recipient) (mail.Message, error)
}

// DatabaseChannel 返回站内信渠道（名为 "database"）：保存到 notifications 表，见 List、UnreadCount 等。
func (s *Service) DatabaseChannel() Channel { return databaseChannel{s} }

type databaseChannel struct{ s *Service }

func (databaseChannel) Name() string { return "database" }

func (databaseChannel) Accepts(n Notification) bool {
	_, ok := n.(DatabaseNotification)
	return ok
}

func (c databaseChannel) Deliver(ctx context.Context, r Recipient, n Notification) error {
	content, err := n.(DatabaseNotification).ToDatabase(ctx, r)
	if err != nil {
		return errors.Wrapf(err, "notification: render %s", n.Type())
	}
	data := map[string]any{}
	if raw, err := jsonMap(n); err == nil {
		data = raw
	}
	m := &Message{UserID: r.UserID, Type: n.Type(), Title: content.Title, Body: content.Body, URL: content.URL, Data: data}
	if _, err := c.s.db.NewInsert().Model(m).Exec(ctx); err != nil {
		return errors.Wrapf(err, "notification: store %s", n.Type())
	}
	return nil
}

// MailChannel 返回邮件渠道（名为 "mail"）。ToMail 返回的 To 为空时发给 Recipient.Email，收件人没有邮箱时跳过。
func MailChannel(m mail.Mailer) Channel { return mailChannel{m} }

type mailChannel struct{ m mail.Mailer }

func (mailChannel) Name() string { return "mail" }

func (mailChannel) Accepts(n Notification) bool {
	_, ok := n.(MailNotification)
	return ok
}

func (c mailChannel) Deliver(ctx context.Context, r Recipient, n Notification) error {
	msg, err := n.(MailNotification).ToMail(ctx, r)
	if err != nil {
		return errors.Wrapf(err, "notification: render mail %s", n.Type())
	}
	if len(msg.To) == 0 {
		if r.Email == "" {
			return nil
		}
		msg.To = []string{r.Email}
	}
	return c.m.Send(ctx, msg)
}
