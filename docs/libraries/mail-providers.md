# 邮件服务商

[邮件](mail.md)一页介绍了怎样编写和发送邮件，发送器（`mail.Mailer`）在开发时只把邮件写进日志。上线时要换成真正发送邮件的服务。这一页介绍框架内置的三种发送器、怎样在一个服务商出问题时自动切换到另一个，以及怎样接入其他服务商。

几个名词：

- **SMTP**：发送邮件的标准协议，几乎所有邮件服务都支持，包括企业邮箱。
- **Resend**、**Amazon SES**：专门发送应用邮件的服务，用 HTTP 接口发送，提供送达率、退信统计等功能。

阅读前需要了解：[邮件](mail.md)中的[装配发送器](mail.md#assembling-mailer)。

- [使用 SMTP](#smtp)
- [使用 Resend](#resend)
- [使用 Amazon SES](#ses)
- [多个发送器](#failover)
- [自定义发送器](#custom-mailers)
- [常见问题](#troubleshooting)

<a id="smtp"></a>
## 使用 SMTP

```yaml
mail:
  driver: smtp
  smtp:
    from: 示例 <no-reply@example.com>
    host: smtp.example.com
    port: 587
    username: ${SMTP_USERNAME}
    password: ${SMTP_PASSWORD}
    tls: starttls
```

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `from` | | 默认发件人，必填 |
| `host` | | 服务器地址，必填 |
| `port` | `587` | 端口 |
| `username`、`password` | | 为空时不认证 |
| `tls` | `starttls` | `starttls`：连接后必须升级到 TLS；`tls`：直接建立 TLS 连接，通常用于 465 端口；`none`：不加密 |
| `timeout` | `10s` | 连接和每次读写的超时 |

每次 `Send` 都会建立一个新连接。设置了 `username` 时必须使用 `starttls` 或 `tls`，go-mail 只在加密的连接上发送密码。

`smtp.Doctor(mailer)` 连接服务器、按配置升级到 TLS 并登录，不发送邮件，失败时说明是地址、端口、TLS 方式还是用户名密码的问题，见[自检](../installation/doctor.md#framework-checks)。

本地开发可以用 [Mailpit](https://mailpit.axllent.org) 在浏览器里查看发出的邮件，配置为 `host: localhost`、`port: 1025`、`tls: none`。

<a id="resend"></a>
## 使用 Resend

```yaml
mail:
  driver: resend
  resend:
    from: 示例 <no-reply@example.com>
    api_key: ${RESEND_API_KEY}
```

发件人的域名需要先在 Resend 中验证。

Resend 支持幂等键：在同一个 key 下重复发送，Resend 只会发出一封。重试可能重复的发送时，用业务 ID 作为 key：

```go
ctx = resend.WithIdempotencyKey(ctx, "article-published/"+item.ID.String())
err = mailer.Send(ctx, msg)
```

需要 Resend 特有的功能（批量发送、定时发送、标签）时，`Client()` 返回底层的 SDK 客户端。需要自定义 `http.Client` 时，自己用 SDK 创建客户端，再交给 `resend.NewWithClient`：

```go
client := resendsdk.NewCustomClient(httpClient, cfg.Mail.Resend.APIKey)
mailer, err := resend.NewWithClient(client, cfg.Mail.Resend.From)
```

<a id="ses"></a>
## 使用 Amazon SES

```yaml
mail:
  driver: ses
  ses:
    from: 示例 <no-reply@example.com>
    region: us-east-1
```

| 字段 | 说明 |
| --- | --- |
| `from` | 默认发件人，必填，域名或地址需要先在 SES 中验证 |
| `region` | 为空时从 `AWS_REGION` 或共享配置读取 |
| `access_key`、`secret_key` | 为空时使用 AWS 默认凭证链 |
| `configuration_set` | 可选，发送时使用的配置集 |

部署在 EC2、ECS 或 EKS 上时省略密钥，给实例或任务的 IAM 角色授予 `ses:SendEmail` 和 `ses:SendRawEmail` 即可。SES 也提供 SMTP 端点，可以用 `mail/smtp` 发送，但需要另外生成 SMTP 密码。

驱动把邮件生成 MIME 原文后交给 SES，附件和自定义头与 SMTP 驱动一致；Bcc 只作为收件人传给 SES，对其他收件人保密。SES 没有幂等键，重试可能重复发送。

新账号处于沙箱模式，只能发给已验证的地址，正式使用前需要申请移出沙箱。退信和投诉通过配置集的事件目标发到 SNS 或 EventBridge，由应用自己处理。

需要 SES 特有的功能（模板、抑制列表）时，`Client()` 返回底层的 SDK 客户端。需要自定义 `http.Client` 或端点时，自己创建客户端，再交给 `ses.NewWithClient`：

```go
client := sesv2.NewFromConfig(awsCfg, func(o *sesv2.Options) { o.HTTPClient = httpClient })
mailer, err := ses.NewWithClient(client, cfg.Mail.SES.From)
```

<a id="failover"></a>
## 多个发送器

交给[队列](../background/queue.md#publishing-jobs)发送的邮件，服务暂时故障时由任务按退避重试，恢复后照样发出。[登录验证码](../security/login.md#codes)、找回密码这类邮件不一样：同步发送，用户在页面上等着，邮件服务一出问题（故障、限流、额度用完、账号被暂停），用户就登录不了。`mail.Failover` 把几个发送器组合成一个，前一个失败时换下一个：

```go
primary, err := resend.New(cfg.Mail.Resend)
if err != nil {
    return err
}
backup, err := ses.New(cfg.Mail.SES)
if err != nil {
    return err
}
mailer := mail.Failover([]mail.Named{
    {Name: "resend", Mailer: primary},
    {Name: "ses", Mailer: backup},
}, mail.FailoverOptions{})
```

它本身也是 `mail.Mailer`，交给业务动作和 `login` 的方式不变。

- 按顺序尝试，第一个成功的为准；全部失败时返回合并的错误，每个都带着发送器的名称。
- 邮件本身不合法（`mail.ErrInvalid`），或者 `ctx` 已经取消、超时时，直接返回错误。
- 一个发送器失败后，`Cooldown`（默认 1 分钟）内排到最后，这期间的邮件直接从下一个开始；冷却期过后重新先试它，恢复了就继续用。这个状态只在本进程中。
- 换用后面的发送器时记录一条 `Warn` 日志（`mail sent by a fallback mailer`），带着失败的发送器和原因；冷却后恢复时记录 `Info`。

两点需要注意：

- **可能收到两封**。前一个服务其实已经收下了邮件，只是响应超时，后一个又发了一次。验证码重复一封没有关系；Resend 的[幂等键](#resend)也会交给后面的发送器，但 SES 和 SMTP 不支持幂等键。
- **每个发送器使用自己配置的默认发件人**。备用服务的发件域名同样要配置 SPF、DKIM，否则换过去的邮件可能进垃圾箱。上线前用备用服务单独发一封试试。

<a id="custom-mailers"></a>
## 自定义发送器

接入其他邮件服务时，实现 `mail.Mailer` 即可。发送前用 `WithDefaultFrom` 补上默认发件人，再调用 `Validate`：

```go
func (m *PostmarkMailer) Send(ctx context.Context, msg mail.Message) error {
    msg = msg.WithDefaultFrom(m.from)
    if err := msg.Validate(); err != nil {
        return err
    }
    // 调用 Postmark API
}
```

<a id="troubleshooting"></a>
## 常见问题

**SMTP 发送时报错认证失败或连接被拒绝**

检查端口和 `tls` 是否匹配：587 端口通常用 `starttls`，465 端口用 `tls`。设置了 `username` 时不能用 `none`，go-mail 只在加密的连接上发送密码。

**本地开发时不想真的发出邮件**

用 [Mailpit](https://mailpit.axllent.org) 接收，在浏览器中查看发出的邮件，见[使用 SMTP](#smtp)。

**用户收到了两封同样的邮件**

使用 `mail.Failover` 时，前一个服务可能已经收下邮件、只是响应超时，后一个又发了一次，见[多个发送器](#failover)。

**换到备用的发送器后，一直没有切回来**

一个发送器失败后，在 `Cooldown`（默认 1 分钟）内排到最后，冷却期过后才会再先试它。这个状态只在本进程中，重启后恢复默认顺序。

