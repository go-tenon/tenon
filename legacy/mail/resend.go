package mail

import (
	"context"

	"github.com/cockroachdb/errors"

	"github.com/resend/resend-go/v4"
)

type resendMailer struct {
	from   string
	client *resend.Client
}

func newResend(cfg Config) (*resendMailer, error) {
	if cfg.Resend.APIKey == "" || cfg.From == "" {
		return nil, errors.New("mail: resend requires api_key and from")
	}
	return &resendMailer{from: cfg.From, client: resend.NewClient(string(cfg.Resend.APIKey))}, nil
}

func (r *resendMailer) Send(ctx context.Context, m Message) error {
	if err := validate(m); err != nil {
		return err
	}
	_, err := r.client.Emails.SendWithContext(ctx, &resend.SendEmailRequest{
		From:    r.from,
		To:      m.To,
		Subject: m.Subject,
		Text:    m.Text,
		Html:    m.HTML,
	})
	if err != nil {
		return errors.Wrap(err, "mail: resend")
	}
	return nil
}
