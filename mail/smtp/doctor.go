package smtp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
	gomail "github.com/wneessen/go-mail"

	"github.com/go-tenon/tenon/doctor"
)

const docMail = "docs/libraries/mail-providers.md"

// Doctor 返回检查 SMTP 服务器的一组自检，组名为 smtp：connection 连接服务器，按配置升级到 TLS 并登录，然后断开，
// 不发送邮件。失败时区分连不上（地址、端口、防火墙）、TLS 方式与端口不符、证书不受信任和用户名密码被拒绝。
// m 在检查时才使用。
func Doctor(m *Mailer) doctor.Group {
	return doctor.Group{Name: "smtp", Checks: []doctor.Check{
		{Name: "connection", Run: func(ctx context.Context) error {
			client, err := gomail.NewClient(m.cfg.Host, m.options()...)
			if err != nil {
				return err
			}
			if err := client.DialWithContext(ctx); err != nil {
				return dialProblem(m.cfg, err)
			}
			_ = client.Close()
			return nil
		}},
	}}
}

// dialProblem 按失败的环节给出修改方法。
func dialProblem(cfg Config, err error) error {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	var (
		protoErr  *textproto.Error
		opErr     *net.OpError
		dnsErr    *net.DNSError
		recordErr tls.RecordHeaderError
		unknownCA x509.UnknownAuthorityError
		hostErr   x509.HostnameError
		certErr   *tls.CertificateVerificationError
	)
	switch {
	case errors.As(err, &protoErr) && (protoErr.Code == 535 || protoErr.Code == 534 || protoErr.Code == 530):
		return doctor.Fail(
			fmt.Sprintf("%s rejected the username or password: %d %s", addr, protoErr.Code, protoErr.Msg),
			"check the username and password; many providers need an app password or SMTP authorization code\n"+
				"instead of the account password",
		).See(docMail)
	case strings.Contains(err.Error(), "does not support STARTTLS"):
		return doctor.Fail(
			addr+" does not offer STARTTLS",
			"port 465 uses implicit TLS: set tls: tls; port 587 uses tls: starttls;\n"+
				"tls: none only for a local test server such as Mailpit",
		).See(docMail)
	case errors.As(err, &recordErr):
		return doctor.Fail(
			addr+" does not speak TLS directly",
			"tls: tls is for port 465; port 587 and 25 need tls: starttls",
		).See(docMail)
	case errors.As(err, &unknownCA), errors.As(err, &hostErr), errors.As(err, &certErr):
		return doctor.Fail(
			"the certificate of "+addr+" is not trusted: "+err.Error(),
			"set host to the name in the server's certificate (not an IP address or an alias)",
		).See(docMail)
	case errors.As(err, &dnsErr), errors.As(err, &opErr) && opErr.Op == "dial":
		return doctor.Fail(
			"cannot connect to "+addr+": "+err.Error(),
			"check host and port and that this host may reach them; many cloud providers block outgoing port 25,\n"+
				"use port 587 (starttls) or 465 (tls) instead",
		).See(docMail)
	}
	return err
}
