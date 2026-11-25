# 邮件

用 `html/template` 编写邮件模板，渲染成 `mail.Message`，交给 `mail.Mailer` 发送。业务代码只依赖 `mail.Mailer`，换服务商只改入口。

包：`mail`、`mail/smtp`、`mail/resend`、`mail/ses`、`mail/logmail`、`mail/mailtest`。

`Send` 同步发送，只发一次。必须送达的邮件交给[队列](../background/queue.md#publishing-jobs)发送，失败时按退避重试；`mail.Message` 可以完整地编码为 JSON。

<a id="example-usage"></a>
## 示例：渲染模板并发送邮件

```go
templates, err := mail.NewTemplates(emails.Files) // 启动时加载一次
if err != nil {
    return err
}
msg, err := templates.Render(ctx, "article_published", map[string]any{"Title": "Hello", "URL": url})
if err != nil {
    return err
}
msg.To = []mail.Address{{Name: "编辑部", Email: "editors@example.com"}}
err = mailer.Send(ctx, msg)
```

`emails.Files` 是嵌入模板的 `embed.FS`，见[编写邮件模板](#writing-templates)；`mailer` 是入口创建的 `mail.Mailer`，见[装配发送器](#assembling-mailer)。邮件中的绝对链接使用 [`http.public_url`](../http/middleware.md#public-url)（`cfg.HTTP.BaseURL()` / `cfg.HTTP.URL`）拼接。

<a id="assembling-mailer"></a>
## 装配发送器

| 包 | 发送方式 |
| --- | --- |
| `mail/logmail` | 写一条 `mail sent` 日志，含收件人、主题和正文，开发时用 |
| `mail/smtp` | SMTP 服务器，基于 [go-mail](https://github.com/wneessen/go-mail) |
| `mail/resend` | [Resend](https://resend.com) API |
| `mail/ses` | [Amazon SES](https://aws.amazon.com/ses/) API |

邮件段放进根配置，`driver` 是应用自己的字段：

```go
type MailConfig struct {
    Driver  string        `yaml:"driver"`  // log、smtp、resend 或 ses
    Editors mail.Address  `yaml:"editors"` // 写成 "编辑部 <editors@example.com>" 或只写邮箱
    SMTP    smtp.Config   `yaml:"smtp"`
    Resend  resend.Config `yaml:"resend"`
    SES     ses.Config    `yaml:"ses"`
}
```

入口按 `driver` 创建一次发送器，交给需要发邮件的业务动作：

```go
func newMailer(cfg MailConfig) (mail.Mailer, error) {
    switch cfg.Driver {
    case "smtp":
        return smtp.New(cfg.SMTP)
    case "resend":
        return resend.New(cfg.Resend)
    case "ses":
        return ses.New(cfg.SES)
    default:
        return logmail.New(nil), nil // nil 表示 slog.Default()
    }
}
```

`smtp.Config` 等自带 `Validate`，[`config.Load`](../general/configuration.md#loading-configuration) 在启动时检查。

<a id="config"></a>
## 配置 SMTP、Resend、Amazon SES

```yaml
mail:
  driver: smtp
  smtp:
    from: 示例 <no-reply@example.com> # 默认发件人，各驱动都必填
    host: smtp.example.com
    port: 587                  # 默认 587
    username: ${SMTP_USERNAME} # 为空时不认证
    password: ${SMTP_PASSWORD}
    tls: starttls              # 默认 starttls；tls 用于 465 端口；none 不加密
    timeout: 10s               # 默认 10s
  resend:
    from: 示例 <no-reply@example.com> # 域名先在 Resend 中验证
    api_key: ${RESEND_API_KEY}
  ses:
    from: 示例 <no-reply@example.com> # 域名先在 SES 中验证
    region: us-east-1    # 为空时读 AWS_REGION，都没有时 ses.New 返回错误
    # access_key、secret_key 同时填写或都省略（用 AWS 默认凭证链）；configuration_set 可选
```

- SMTP 设置了 `username` 时用 `starttls` 或 `tls`：go-mail 只在加密的连接上发送密码。本地用 [Mailpit](https://mailpit.axllent.org) 查看邮件：`host: localhost`、`port: 1025`、`tls: none`。
- `smtp.Doctor(mailer)` 连接服务器并登录，不发邮件，见[自检](../installation/doctor.md#framework-checks)。
- SES 部署在 AWS 上时省略密钥，给 IAM 角色授予 `ses:SendEmail` 和 `ses:SendRawEmail`。新账号处于沙箱，只能发给已验证的地址。
- Resend 的幂等键：`ctx = resend.WithIdempotencyKey(ctx, "article-published/"+id)` 之后发送，同一个键只发出一封。SES 和 SMTP 没有幂等键，重试可能重复发送。
- Resend、SES 发送器的 `Client()` 返回底层 SDK 客户端；自定义 `http.Client` 时自己创建 SDK 客户端，交给 `resend.NewWithClient` 或 `ses.NewWithClient`。

<a id="writing-templates"></a>
## 编写邮件模板

一封名为 `article_published` 的邮件由几个文件组成：

```
internal/emails/
├── embed.go
├── _layout.html
├── article_published.subject.txt   # 主题，必须有
├── article_published.html          # HTML 正文
└── article_published.txt           # 纯文本正文
```

- `.html` 和 `.txt` 至少有一个。`.html` 用 `html/template` 按 HTML 规则转义数据，其余用 `text/template`。
- 邮件可以放在子目录中，名称是相对路径，例如 `auth/reset_password`。

```
{{/* article_published.subject.txt */}}
新文章：{{.Title}}
```

在入口中用 `mail.NewTemplates(emails.Files)` 加载一次，缺少主题、缺少正文、语法错误都在启动时报错；返回的 `*mail.Templates` 可以并发使用。

```go
package emails

import "embed"

//go:embed *.html *.txt
var Files embed.FS
```

<a id="layouts"></a>
### 布局

文件名以 `_` 开头的 `.html` 或 `.txt` 是公共片段，加入每封邮件的同类模板。布局用 `{{template "content" .}}` 引用邮件内容，邮件先定义 `content` 再调用布局：

```html
{{/* _layout.html */}}
<!doctype html>
<html><body>{{template "content" .}}</body></html>
```

```html
{{/* article_published.html */}}
{{define "content"}}
<p>《{{.Title}}》已经发布。<a href="{{.URL}}">查看文章</a></p>
{{end}}
{{template "_layout.html" .}}
```

<a id="translations"></a>
### 翻译

模板中用 `t` 翻译消息，它就是 [`i18n.T`](../general/localization.md#retrieving-translations)，语言取自 `Render` 的 `ctx`：

```
{{/* article_published.subject.txt */}}
{{t "mail.article_published.subject" .}}
```

```yaml
# locales/zh-CN.yaml
mail.article_published.subject: "新文章：{{.Title}}"
```

HTTP 请求中的 `ctx` 已经带着请求的语言；请求之外用 `i18n.WithLocale` 指定收件人的语言，见[请求之外](../general/localization.md#outside-http)。

<a id="sending-mail"></a>
## 在事务提交之后发送邮件

在事务中发送，事务回滚时邮件已经发出，所以用 [`tx.AfterCommit`](../database/database.md#after-commit) 在提交之后发送。`a.mailer`、`a.emails`、`a.editors` 由构造函数传入，`item` 是事务中发布的文章，模板中用 `{{.Title}}` 取它的字段：

```go
err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
    // ……更新文章
    tx.AfterCommit(func(ctx context.Context) error {
        msg, err := a.emails.Render(ctx, "article_published", item)
        if err != nil {
            return errors.Wrapf(err, "render mail for article %s", item.ID)
        }
        msg.To = []mail.Address{a.editors}
        return errors.Wrapf(a.mailer.Send(ctx, msg), "notify editors of article %s", item.ID)
    })
    return nil
})
```

回调的错误只记录日志，`RunInTx` 仍返回 `nil`。回调只在内存中，必须送达的邮件改为在事务中发布队列任务。

<a id="messages"></a>
## 抄送、附件和邮件头：`mail.Message`

`Render` 只填好 `Subject`、`HTML` 和 `Text`，其余字段自己设置。`data` 是附件的字节，`item` 是业务数据：

```go
msg.From = mail.Address{Name: "示例", Email: "news@example.com"} // 为空时用发送器配置的 from
msg.Cc = []mail.Address{{Email: "chief@example.com"}}
msg.Bcc = []mail.Address{{Email: "archive@example.com"}}
msg.ReplyTo = mail.Address{Email: "support@example.com"}
msg.Attachments = []mail.Attachment{
    {Filename: "report.pdf", ContentType: "application/pdf", Content: data}, // ContentType 为空时按文件名推断
}
msg.Headers = map[string]string{"X-Entity-Ref-ID": item.ID.String()}
```

发送前校验：至少一个 `To`，主题非空且只有一行，`HTML` 和 `Text` 至少一个，地址合法，附件有文件名，头部不含换行；不满足时返回 `mail.ErrInvalid`。附件整个放在内存中，大文件先上传到[文件存储](filesystem.md)，邮件中放链接。

<a id="failover"></a>
## 多个发送器：`mail.Failover`

[登录验证码](../security/login.md#codes)、找回密码这类同步发送的邮件，服务一出问题（故障、限流、额度用完）用户就登录不了。`mail.Failover` 把几个发送器组合成一个 `mail.Mailer`，前一个失败时换下一个。`primary`、`backup` 是 `resend.New`、`ses.New` 创建的发送器：

```go
mailer := mail.Failover([]mail.Named{
    {Name: "resend", Mailer: primary},
    {Name: "ses", Mailer: backup},
}, mail.FailoverOptions{}) // Cooldown 默认 1 分钟，Logger 默认 slog.Default()
```

- 按顺序尝试，第一个成功的为准；全部失败时返回合并的错误，带着各发送器的名称。邮件不合法或 `ctx` 已经取消时直接返回。
- 失败的发送器在 `Cooldown` 内排到最后，过后重新先试它。状态只在本进程中。
- 换用后面的发送器时记录 `Warn` 日志 `mail sent by a fallback mailer`，恢复时记录 `Info` 日志 `mailer recovered`。

> [!WARNING]
> 前一个服务可能已经收下邮件、只是响应超时，收件人会收到两封。每个发送器用自己配置的 `from`，备用服务的发件域名同样要配置 SPF、DKIM，否则换过去的邮件可能进垃圾箱。

<a id="custom-mailers"></a>
## 自定义发送器

接入其他服务商时实现 `mail.Mailer`，发送前补上默认发件人并校验：

```go
func (m *PostmarkMailer) Send(ctx context.Context, msg mail.Message) error {
    msg = msg.WithDefaultFrom(m.from)
    if err := msg.Validate(); err != nil {
        return err
    }
    // 调用 Postmark API
}
```

<a id="testing"></a>
## 测试发送的邮件：`mailtest`

`mailtest.New()` 记录每封通过校验的邮件。`db` 是 [`dbtest.Open`](../testing/testing.md#creating-test-databases) 创建的临时库，`article.NewPublish` 是要测试的业务动作：

```go
mailer := mailtest.New()
templates, err := mail.NewTemplates(emails.Files)
require.NoError(t, err)
publish := article.NewPublish(db, mailer, templates, mail.Address{Email: "editors@example.com"})

// ……创建并发布一篇文章

msg, ok := mailer.Last()
require.True(t, ok)
assert.Contains(t, msg.Subject, "Hello")
```

`Sent()` 返回全部邮件；`FailWith(err)` 让之后的 `Send` 都返回 `err`；`Reset()` 清空记录并取消 `FailWith`。
