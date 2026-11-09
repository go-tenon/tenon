package mail

import (
	"context"
	"log/slog"
)

// Log 把邮件写入 slog.Default()（Info 级别，含正文），不会真正发出。用于开发环境。
type Log struct{}

func (Log) Send(ctx context.Context, m Message) error {
	if err := validate(m); err != nil {
		return err
	}
	body := m.Text
	if body == "" {
		body = m.HTML
	}
	slog.InfoContext(ctx, "mail (not sent, log driver)", "to", m.To, "subject", m.Subject, "body", body)
	return nil
}
