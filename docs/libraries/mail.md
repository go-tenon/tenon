# 邮件

发送邮件是大多数应用都需要的功能：验证码、通知、找回密码。Tenon 的 `mail` 包定义了邮件消息 `mail.Message`，以及只有一个方法的发送接口：

```go
type Mailer interface {
    Send(ctx context.Context, msg Message) error
}
```

具体怎么发送由驱动决定：

| 包 | 用途 |
| --- | --- |
| `mail/logmail` | 开发时把邮件写进日志，不真正发送 |
| `mail/smtp` | 通过 SMTP 服务器发送，基于 [go-mail](https://github.com/wneessen/go-mail) |
| `mail/resend` | 通过 [Resend](https://resend.com) 发送，基于官方 SDK [resend-go](https://github.com/resend/resend-go) |
| `mail/ses` | 通过 [Amazon SES](https://aws.amazon.com/ses/) 发送，基于 [AWS SDK for Go v2](https://github.com/aws/aws-sdk-go-v2) |
| `mail/mailtest` | 测试中记录发出的邮件 |

业务代码只依赖 `mail.Mailer`，换驱动只需要改入口。

这一页介绍怎样装配发送器、编写模板和发送邮件。SMTP、Resend、SES 的配置，以及多个服务商之间自动切换，见[邮件服务商](mail-providers.md)。

阅读前需要了解：[配置](../general/configuration.md)；[依赖装配](../concepts/dependencies.md)；Go 标准库的 `html/template`（邮件模板用它编写）。

- [示例用法](#example-usage)
- [装配发送器](#assembling-mailer)
- [编写邮件模板](#writing-templates)
    - [布局](#layouts)
    - [翻译](#translations)
- [发送邮件](#sending-mail)
- [邮件消息](#messages)
- [测试](#testing)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面用模板渲染一封邮件，然后发送它：

```go
mailer := logmail.New(nil) // 开发时写进日志；生产环境换成 smtp.New、resend.New 或 ses.New

templates, err := mail.NewTemplates(emails.FS)
if err != nil {
    return err
}
msg, err := templates.Render(ctx, "article_published", map[string]any{"Title": "Hello"})
if err != nil {
    return err
}
msg.To = []mail.Address{{Name: "编辑部", Email: "editors@example.com"}}
err = mailer.Send(ctx, msg)
```

开发时用 `logmail` 把邮件写进日志，你可以直接从日志中复制邮件里的链接；上线时换成 SMTP、Resend 或 SES，业务代码保持原样。`Send` 是同步的，只发送一次，需要可靠送达的邮件交给[队列](../background/queue.md#publishing-jobs)发送。

<a id="assembling-mailer"></a>
## 装配发送器

串起来看：把邮件段挂进根配置 → 启动时 `Load` 校验嵌套的 `smtp` / `resend` / `ses` 配置 → 在入口按 `driver` 创建一次发送器 → 业务动作复用它发信。

在应用配置中加入邮件段。选哪个驱动由应用自己决定，这里用一个 `driver` 字段区分：

```go
import (
    "github.com/go-tenon/tenon/mail"
    "github.com/go-tenon/tenon/mail/logmail"
    "github.com/go-tenon/tenon/mail/resend"
    "github.com/go-tenon/tenon/mail/ses"
    "github.com/go-tenon/tenon/mail/smtp"
)

type MailConfig struct {
    Driver  string        `yaml:"driver"`  // log、smtp、resend 或 ses
    Editors mail.Address  `yaml:"editors"` // 编辑部邮箱
    SMTP    smtp.Config   `yaml:"smtp"`
    Resend  resend.Config `yaml:"resend"`
    SES     ses.Config    `yaml:"ses"`
}

type Config struct {
    // ...
    Mail MailConfig `yaml:"mail"`
}
```

```yaml
mail:
  driver: log
  editors: 编辑部 <editors@example.com>
```

`mail.Address` 在配置中写成一个字符串，`"名字 <邮箱>"` 或只写邮箱都可以。嵌套的 `smtp.Config` 等框架类型自带 `Validate`：挂进根配置后，[`config.Load`](../general/configuration.md#loading-configuration) 会在启动时检查它们。`driver` 本身是应用字段——想在启动时拒绝未知驱动，给 `MailConfig` 也写一个 `Validate`。

然后在入口创建发送器，传给需要发邮件的业务动作：

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
        return logmail.New(nil), nil
    }
}
```

```go
mailer, err := newMailer(cfg.Mail)
if err != nil {
    return err
}
publish := article.NewPublish(db, mailer, emails, cfg.Mail.Editors)
```

`emails` 是下一节加载的邮件模板。`logmail.New(nil)` 使用 `slog.Default()`，每封邮件记录为一条 `mail sent` 日志，包含收件人、主题和正文，开发时可以直接从日志里复制邮件中的链接。

发送器只持有配置，创建后直接使用即可。

<a id="writing-templates"></a>
## 编写邮件模板

邮件模板用 Go 标准库的 [html/template](https://pkg.go.dev/html/template) 和 [text/template](https://pkg.go.dev/text/template) 编写。一封名为 `article_published` 的邮件由三个文件组成：

```
internal/emails/
├── embed.go
├── _layout.html
├── article_published.subject.txt
├── article_published.html
└── article_published.txt
```

`.subject.txt` 是主题，必须提供；`.html` 和 `.txt` 是正文，至少提供一个，两个都有时收件人的邮件客户端会选择合适的版本显示。邮件可以放在子目录中，名称就是相对路径，例如 `auth/reset_password`。

```
{{/* article_published.subject.txt */}}
新文章：{{.Title}}
```

```
{{/* article_published.txt */}}
《{{.Title}}》已经发布：{{.URL}}
```

用 `embed` 把模板嵌入二进制，在入口中加载一次：

```go
package emails

import "embed"

//go:embed *.html *.txt
var Files embed.FS
```

```go
emails, err := mail.NewTemplates(emails.Files)
if err != nil {
    return err
}
```

`NewTemplates` 在启动时解析全部模板，缺少主题、缺少正文或语法错误都会在启动时报错。返回的 `*mail.Templates` 可以被多个请求同时使用。

<a id="layouts"></a>
### 布局

文件名以 `_` 开头的 `.html` 或 `.txt` 文件是公共片段，会被加入每封邮件的同类模板。用它写布局：布局通过 `{{template "content" .}}` 引用邮件内容，邮件先定义 `content`，再调用布局：

```html
{{/* _layout.html */}}
<!doctype html>
<html>
<body style="font-family: sans-serif">
  {{template "content" .}}
  <p style="color: #888">编辑部</p>
</body>
</html>
```

```html
{{/* article_published.html */}}
{{define "content"}}
<p>《{{.Title}}》已经发布。</p>
<p><a href="{{.URL}}">查看文章</a></p>
{{end}}
{{template "_layout.html" .}}
```

`.html` 模板会按 HTML 规则转义数据，文章标题中的 `<` 按文字显示。

<a id="translations"></a>
### 翻译

所有模板都可以调用 `t` 翻译消息，它就是 [i18n.T](../general/localization.md)，语言取自渲染时的 `ctx`：

```
{{/* article_published.subject.txt */}}
{{t "mail.article_published.subject" .}}
```

```yaml
# locales/zh-CN.yaml
mail.article_published.subject: "新文章：{{.Title}}"
```

在 HTTP 请求中渲染时，`ctx` 已经带着请求语言。在请求之外，用 `i18n.WithLocale` 指定收件人的语言。

<a id="sending-mail"></a>
## 发送邮件

`Render` 渲染一封邮件，返回已经填好 `Subject`、`HTML` 和 `Text` 的 `mail.Message`，再补上收件人交给发送器：

```go
type Publish struct {
    db      *bun.DB
    mailer  mail.Mailer
    emails  *mail.Templates
    editors mail.Address
}

func (a *Publish) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Article, error) {
    item := new(Article)
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        // ...
        tx.AfterCommit(func(ctx context.Context) error { return a.notifyEditors(ctx, item) })
        return nil
    })
    if err != nil {
        return nil, err
    }
    return item, nil
}

func (a *Publish) notifyEditors(ctx context.Context, item *Article) error {
    msg, err := a.emails.Render(ctx, "article_published", map[string]any{
        "Title": item.Title,
        "URL":   "https://example.com/" + item.Slug,
    })
    if err != nil {
        return errors.Wrapf(err, "render notification for article %s", item.ID)
    }
    msg.To = []mail.Address{a.editors}
    return errors.Wrapf(a.mailer.Send(ctx, msg), "notify editors of article %s", item.ID)
}
```

`Publish` 的事务部分见[授权](../security/authorization.md#checking-in-transactions)。这里有两点要注意：

- **在事务提交之后发送**。在事务里发送，事务回滚时邮件已经发出去了。[AfterCommit](../database/database.md#after-commit) 登记的回调在提交成功之后才执行。
- **发送失败时发布照样成功**。文章已经发布成功，回调返回的错误只记录日志，接口照常返回。

> [!NOTE]
> `Send` 是同步的：接口要等邮件服务器响应才返回，并且只发送一次。需要可靠送达的邮件可以交给[队列](../background/queue.md#publishing-jobs)发送，`mail.Message` 可以完整地序列化为 JSON。

<a id="messages"></a>
## 邮件消息

除了模板填好的字段，`mail.Message` 还可以设置：

```go
msg.From = mail.Address{Name: "示例", Email: "news@example.com"} // 为空时使用驱动配置的发件人
msg.Cc = []mail.Address{{Email: "chief@example.com"}}
msg.Bcc = []mail.Address{{Email: "archive@example.com"}}
msg.ReplyTo = mail.Address{Email: "support@example.com"}
msg.Attachments = []mail.Attachment{
    {Filename: "report.pdf", ContentType: "application/pdf", Content: pdf},
}
msg.Headers = map[string]string{"X-Entity-Ref-ID": item.ID.String()}
```

每个驱动在发送前都会调用 `msg.Validate()`：至少一个 `To` 收件人、主题非空且只有一行、正文至少有一种、地址合法、头部不含换行。不满足时返回的错误可以用 `errors.Is(err, mail.ErrInvalid)` 判断。

附件会整个放进内存。大文件更适合先用[文件存储](filesystem.md)上传，在邮件中放公开链接。

<a id="testing"></a>
## 测试

测试中用 `mailtest.New()` 代替真实的发送器，它记录每封通过校验的邮件：

```go
func TestPublishNotifiesEditors(t *testing.T) {
    mailer := mailtest.New()
    emails, err := mail.NewTemplates(emails.Files)
    if err != nil {
        t.Fatal(err)
    }
    editors := mail.Address{Email: "editors@example.com"}
    publish := article.NewPublish(db, mailer, emails, editors)

    // ... 创建并发布一篇文章

    msg, ok := mailer.Last()
    if !ok || msg.To[0] != editors || !strings.Contains(msg.Subject, "Hello") {
        t.Fatalf("sent = %+v", mailer.Sent())
    }
}
```

`mailer.FailWith(err)` 让之后的发送都返回 `err`，用来测试发送失败时的行为；`Reset` 清空记录。

<a id="troubleshooting"></a>
## 常见问题

**开发时收不到邮件**

开发时用的是 `log` 驱动，邮件只写进日志。想在浏览器中查看邮件，见[邮件服务商](mail-providers.md#smtp)中的 Mailpit。

**邮件模板中的变量没有替换**

检查模板中写的字段名与传入的数据是否一致：区分大小写，结构体的字段要导出（首字母大写），见[编写邮件模板](#writing-templates)。

**通知邮件发送失败后丢了**

同步发送的邮件失败时只返回错误。通知类的邮件交给[队列](../background/queue.md#publishing-jobs)发送，失败时按退避重试。

<a id="api-reference"></a>
## API 参考

### mail.Mailer

```go
type Mailer interface {
    Send(ctx context.Context, msg Message) error
}
```

包 `github.com/go-tenon/tenon/mail`。消息不合法时返回 `ErrInvalid`。

### 发送器

| 构造函数 | 说明 |
| --- | --- |
| `smtp.New(cfg smtp.Config)` | 包 `mail/smtp`，通过 SMTP 服务器发送 |
| `resend.New(cfg resend.Config)` | 包 `mail/resend`，通过 Resend API 发送；`NewWithClient` 用已有的客户端 |
| `ses.New(cfg ses.Config)` | 包 `mail/ses`，通过 Amazon SES 发送；`NewWithClient` 用已有的客户端 |
| `logmail.New(logger *slog.Logger)` | 包 `mail/logmail`，记录为日志，开发时使用 |
| `mailtest` | 包 `mail/mailtest`，在内存中记录发出的邮件，见[测试](#testing) |

### Failover

```go
func Failover(mailers []Named, opts FailoverOptions) Mailer
```

按顺序尝试多个发送器，失败的在冷却时间内跳过；遇到 `ErrInvalid` 或 `ctx` 取消时直接返回。见[故障切换](mail-providers.md#failover)。

### Templates

```go
func NewTemplates(fsys fs.FS) (*Templates, error)
func (t *Templates) Render(ctx context.Context, name string, data any) (Message, error)
```

- **参数**
    - `fsys`（`NewTemplates`）：存放模板的文件系统，通常是 `embed.FS`
    - `name`（`Render`）：邮件的名称，例如 `article_published`
    - `data`（`Render`）：模板数据
- **返回**：渲染好主题和正文的邮件，收件人由你填写；模板有错时返回错误

`NewTemplates` 在创建时加载并检查全部模板。`Render` 用 `ctx` 中的语言翻译模板中的文案，见[编写邮件模板](#writing-templates)。

### Message

| 字段 | 说明 |
| --- | --- |
| `From`、`ReplyTo Address` | 发件人和回复地址，`From` 为空时用驱动配置的默认发件人 |
| `To`、`Cc`、`Bcc []Address` | 收件人 |
| `Subject`、`HTML`、`Text string` | 主题和正文 |
| `Attachments []Attachment` | 附件 |
| `Headers map[string]string` | 额外的邮件头 |

`Message` 可以完整地编码为 JSON，便于交给队列发送。

### WithIdempotencyKey

```go
func WithIdempotencyKey(ctx context.Context, key string) context.Context
```

包 `mail/resend`。重试同一封邮件时，Resend 按这个键去重。
