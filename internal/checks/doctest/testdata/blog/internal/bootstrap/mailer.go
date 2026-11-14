package bootstrap

import (
	"example.com/blog/internal/settings"
	"github.com/go-tenon/tenon/mail"
	"github.com/go-tenon/tenon/mail/logmail"
	"github.com/go-tenon/tenon/mail/resend"
	"github.com/go-tenon/tenon/mail/ses"
	"github.com/go-tenon/tenon/mail/smtp"
)

func newMailer(cfg settings.MailConfig) (mail.Mailer, error) {
	switch cfg.Driver {
	case "smtp":
		return smtp.New(cfg.SMTP)
	case "resend":
		return resend.New(cfg.Resend)
	case "ses":
		return ses.New(cfg.SES)
	default:
		return logmail.New(nil), nil
	}
}
