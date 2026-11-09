// Package mail 发送邮件：一个 Mailer 接口，三种实现。
//
//	log      只把邮件写入日志，开发环境默认使用，不会真正发出
//	smtp     通过 SMTP 发送（github.com/wneessen/go-mail）
//	resend   通过 Resend 的 HTTP API 发送（github.com/resend/resend-go）
//
// 由配置选择实现：
//
//	mail:
//	  driver: smtp
//	  from: "MyApp <no-reply@example.com>"
//	  smtp:
//	    host: smtp.example.com
//	    port: 587
//	    username: no-reply@example.com
//	    password: ${SMTP_PASSWORD}
//
// 业务代码通常不直接同步发信，而是在事务中发布一条消息，由消息处理函数调用 Mailer 发送，
// 失败时自动重试，见 messaging 包。测试中用 mailtest.Recorder 代替。
package mail

import (
	"context"

	"github.com/cockroachdb/errors"

	"github.com/ryan053857/go-kit/config"
)

// Message 是一封邮件。Text 与 HTML 至少有一个；两者都有时邮件同时包含两种格式。
type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
}

// Mailer 发送邮件。
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// 驱动名。
const (
	DriverLog    = "log"
	DriverSMTP   = "smtp"
	DriverResend = "resend"
)

// Config 是邮件配置。
type Config struct {
	Driver string `yaml:"driver"` // log（默认）| smtp | resend
	From   string `yaml:"from"`   // 发件人，如 "MyApp <no-reply@example.com>"，smtp 与 resend 必填

	SMTP struct {
		Host     string        `yaml:"host"`
		Port     int           `yaml:"port"` // 默认 587；465 时使用隐式 TLS
		Username string        `yaml:"username"`
		Password config.Secret `yaml:"password"`
		// TLS 为 mandatory（默认，必须 STARTTLS 或隐式 TLS）| opportunistic（服务器支持时加密）| none（本地测试服务器）
		TLS string `yaml:"tls"`
	} `yaml:"smtp"`

	Resend struct {
		APIKey config.Secret `yaml:"api_key"`
	} `yaml:"resend"`
}

// New 按 cfg.Driver 返回 Mailer。
func New(cfg Config) (Mailer, error) {
	switch cfg.Driver {
	case "", DriverLog:
		return Log{}, nil
	case DriverSMTP:
		return newSMTP(cfg)
	case DriverResend:
		return newResend(cfg)
	}
	return nil, errors.Newf("mail: unknown driver %q", cfg.Driver)
}

func validate(m Message) error {
	if len(m.To) == 0 {
		return errors.New("mail: no recipients")
	}
	if m.Text == "" && m.HTML == "" {
		return errors.New("mail: empty body")
	}
	return nil
}
