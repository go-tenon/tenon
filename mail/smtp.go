package mail

import (
	"context"
	"errors"
	"fmt"

	gomail "github.com/wneessen/go-mail"
)

type smtpMailer struct {
	from   string
	client *gomail.Client
}

func newSMTP(cfg Config) (*smtpMailer, error) {
	c := cfg.SMTP
	if c.Host == "" || cfg.From == "" {
		return nil, errors.New("mail: smtp requires host and from")
	}
	port := c.Port
	if port == 0 {
		port = 587
	}
	opts := []gomail.Option{gomail.WithPort(port)}
	switch c.TLS {
	case "", "mandatory":
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	case "opportunistic":
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSOpportunistic))
	case "none":
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	default:
		return nil, fmt.Errorf("mail: unknown smtp tls %q", c.TLS)
	}
	if port == 465 {
		opts = append(opts, gomail.WithSSL())
	}
	if c.Username != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(c.Username),
			gomail.WithPassword(string(c.Password)),
		)
	}
	client, err := gomail.NewClient(c.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("mail: %w", err)
	}
	return &smtpMailer{from: cfg.From, client: client}, nil
}

func (s *smtpMailer) Send(ctx context.Context, m Message) error {
	if err := validate(m); err != nil {
		return err
	}
	msg := gomail.NewMsg()
	if err := msg.From(s.from); err != nil {
		return fmt.Errorf("mail: from: %w", err)
	}
	if err := msg.To(m.To...); err != nil {
		return fmt.Errorf("mail: to: %w", err)
	}
	msg.Subject(m.Subject)
	switch {
	case m.Text != "" && m.HTML != "":
		msg.SetBodyString(gomail.TypeTextPlain, m.Text)
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	case m.HTML != "":
		msg.SetBodyString(gomail.TypeTextHTML, m.HTML)
	default:
		msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	}
	if err := s.client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("mail: smtp: %w", err)
	}
	return nil
}
