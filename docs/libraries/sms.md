# 短信

用 `sms` 发送登录验证码、订单通知等短信。一条短信是号码、模板名和模板变量：模板名由应用起（例如 `login_code`），签名和各服务商的模板 ID 写在各自的配置中，业务代码与服务商无关。

包：`sms`、`sms/aliyun`、`sms/tencent`、`sms/logsms`、`sms/smstest`。

<a id="example-usage"></a>
## 示例

```go
sender := logsms.New(nil) // 开发时写进日志；生产环境用 aliyun.New 或 tencent.New

err := sender.Send(ctx, sms.Message{
    To:       "+8613800138000",                     // E.164 格式
    Template: "login_code",                         // 应用给模板起的名字
    Params:   map[string]string{"code": "123456"}, // 模板变量
})
```

<a id="assembling"></a>
## 按配置装配发送器

与[邮件](mail.md#assembling-mailer)相同：配置段挂进根配置，入口按 `driver` 创建一次发送器，之后复用。`settings.SMSConfig` 是应用配置中的这一段：

```go
type SMSConfig struct {
    Driver  string         `yaml:"driver"` // log、aliyun 或 tencent
    Aliyun  aliyun.Config  `yaml:"aliyun"`
    Tencent tencent.Config `yaml:"tencent"`
}

func newTexts(cfg settings.SMSConfig) (sms.Sender, error) {
    switch cfg.Driver {
    case "aliyun":
        return aliyun.New(cfg.Aliyun, nil)
    case "tencent":
        return tencent.New(cfg.Tencent, nil)
    default:
        return logsms.New(nil), nil
    }
}
```

- `aliyun.New`、`tencent.New` 校验配置，不访问网络；第二个参数为 `nil` 时用 [`outbound`](outbound.md) 的客户端，超时 10 秒。
- 开发时用 `log`：`logsms` 把每条短信记为一条 Info 级别的 `sms sent` 日志，带着号码、模板和变量，验证码从日志中看。

<a id="sending"></a>
## 用 `Send` 发送短信

例如 [`login`](../security/login.md#codes) 的 `Send` 把验证码交给短信发送器，`texts` 是装配好的 `sms.Sender`：

```go
Send: func(ctx context.Context, code login.Code) error {
    return texts.Send(ctx, sms.Message{
        To:       code.To.Value, // login 中的手机号已经是 E.164 格式
        Template: "login_code",
        Params:   map[string]string{"code": code.Code},
    })
},
```

- `Send` 是同步的，等服务商响应才返回。通知类的短信交给[队列](../background/queue.md#publishing-jobs)发送：`sms.Message` 可以完整地序列化为 JSON。
- 服务商接受请求就返回成功。阿里云和腾讯云都没有幂等键，重试可能让用户收到两条。

<a id="errors"></a>
## 错误

| 错误 | 含义 | 客户端得到 |
| --- | --- | --- |
| `sms.ErrInvalid` | 短信不合法：号码不是 E.164、模板名为空、缺少腾讯云模板需要的变量 | `500`，程序的问题 |
| `sms.ErrRejected` | 服务商拒绝这个号码：格式不被接受、空号停机、黑名单、用户退订 | `422` |
| `sms.ErrLimited` | 触发服务商对这个号码的频率限制 | `429` |
| 其他错误 | 余额不足、签名或模板未审核、密钥错误、模板名没有配置、网络错误 | `500` |

`ErrRejected` 和 `ErrLimited` 是[业务错误](../general/errors.md)，经 `login.SendCode` 原样返回时客户端得到翻译后的提示。

<a id="failover"></a>
## 用 `sms.Failover` 组合多个服务商

一家服务商出问题（故障、余额不足、签名被暂停）时换下一家，验证码照样发出。`primary`、`backup` 是 `aliyun.New`、`tencent.New` 创建的发送器：

```go
texts := sms.Failover([]sms.Named{
    {Name: "aliyun", Sender: primary},
    {Name: "tencent", Sender: backup},
}, sms.FailoverOptions{})
```

- 按顺序尝试，失败的服务商在 `Cooldown`（默认 1 分钟）内排到最后，规则与[邮件的多个发送器](mail.md#failover)相同。
- `ErrInvalid`、`ErrRejected` 和 `ctx` 取消时直接返回：换哪家都一样。
- `ErrLimited` 也直接返回：换一家发会绕过服务商的防刷保护，被批量刷验证码时费用翻倍。
- 每家服务商都要申请同名的模板，签名都要审核通过。

<a id="testing"></a>
## 用 `smstest` 测试

`smstest.New()` 在内存中记录短信，`Sent`、`Last` 读出发送过的短信，`FailWith` 模拟服务商出错：

```go
texts := smstest.New()               // 用 texts 装配 login，调用 SendCode
msg, ok := texts.Last()              // 取出验证码：msg.Params["code"]
texts.FailWith(sms.ErrLimited.New()) // 之后的发送返回这个错误
```

测试 `sms/aliyun`、`sms/tencent` 的配置时，把 [`outboundtest`](outbound.md#testing) 的客户端传给 `New`。

<a id="aliyun"></a>
## 阿里云：`sms/aliyun`

```yaml
sms:
  driver: aliyun
  aliyun:
    access_key_id: ${ALIYUN_ACCESS_KEY_ID}         # RAM 用户的 AccessKey，需要 dysms:SendSms 权限
    access_key_secret: ${ALIYUN_ACCESS_KEY_SECRET}
    sign_name: 示例                                  # 审核通过的签名，必填
    templates:                                       # 模板名 → 模板 Code，至少一个
      login_code: SMS_465000001
    # endpoint: dysmsapi.aliyuncs.com                # 接口域名，默认值
```

模板中的变量名就是 `Params` 的键，例如模板写 `${code}`，就传 `{"code": "123456"}`。

<a id="tencent"></a>
## 腾讯云：`sms/tencent`

```yaml
sms:
  driver: tencent
  tencent:
    secret_id: ${TENCENT_SECRET_ID}
    secret_key: ${TENCENT_SECRET_KEY}
    app_id: "1400000001"                          # 短信应用的 SdkAppId，必填
    sign_name: 示例                                # 签名的文字本身；国内短信必填
    templates:                                     # 模板名 → 模板 ID 和变量顺序
      login_code: {id: "2000001", params: [code]}
    # region: ap-guangzhou                         # 默认值
    # endpoint: sms.tencentcloudapi.com            # 默认值
```

腾讯云的模板变量是按顺序的 `{1}`、`{2}`，`params` 写明依次取 `Params` 中的哪个键，两家因此可以用同一条 `sms.Message`；缺少其中的键时返回 `sms.ErrInvalid`。

<a id="custom-senders"></a>
## 实现 `sms.Sender` 接入其他服务商

接入其他服务商（例如 Twilio）时实现 `sms.Sender` 的 `Send(ctx, msg)`：先调用 `msg.Validate()`，再把服务商表示号码被拒绝、频率受限的错误码包装为 `sms.ErrRejected.Wrap(err)`、`sms.ErrLimited.Wrap(err)`，`Failover` 和客户端才能正确处理。
