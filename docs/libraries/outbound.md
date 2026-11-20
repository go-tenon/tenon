# 出站请求

请求用户或租户填写的地址（webhook 端点、链接预览、AI 抓取的网页）时用 `outbound` 创建的 `*http.Client`，它只连接公网地址，防止 SSRF：有人填 `http://169.254.169.254/` 或 `http://10.0.0.5:6379/`，借服务器读取云服务器的元数据或访问内网服务。

包：`outbound`、`outbound/outboundtest`。

- 检查的是解析之后真正连接的 IP，DNS 重绑定、重定向到内网都被拒绝。拒绝的范围是 IANA 的特殊用途地址（[code.dny.dev/ssrf](https://pkg.go.dev/code.dny.dev/ssrf)），包括内网、回环、链路本地、运营商级 NAT（含阿里云元数据 `100.100.100.200`）。
- 客户端不使用 `HTTP_PROXY` 等环境变量中的代理：经过代理时检查不到真正的目标。部署要求经代理访问外网时，在代理上做同样的限制，例如 [Smokescreen](https://github.com/stripe/smokescreen)。

<a id="example-usage"></a>
## 示例

```go
client := outbound.New(outbound.Options{Timeout: 5 * time.Second})

resp, err := client.Get(userURL)
if errors.Is(err, outbound.ErrForbidden) {
    return ErrBadLink.New() // 地址指向内网、回环或元数据接口，重试也没有用
}
```

`ErrBadLink` 是应用定义的业务错误。被拒绝的请求不会到达目标。

<a id="creating-clients"></a>
## 用 `outbound.New` 创建客户端

客户端可以复用：在入口创建一次，交给业务动作。它就是普通的 `*http.Client`，例如取链接预览，`a.client` 是入口传入的客户端：

```go
func (a *Preview) fetch(ctx context.Context, link string) (*LinkPreview, error) {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
    if err != nil {
        return nil, ErrInvalidLink.Wrap(err)
    }
    resp, err := a.client.Do(req)
    if errors.Is(err, outbound.ErrForbidden) {
        return nil, ErrInvalidLink.Wrap(err) // 400：这个链接无法预览
    }
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    return parsePreview(io.LimitReader(resp.Body, 1<<20)) // 最多读 1 MiB
}
```

- `Timeout` 是整个请求的上限，包括连接、重定向和读取响应体；零值是 30 秒，负数不限制（流式响应用）。
- 客户端不限制响应的大小，用 `io.LimitReader` 限制读取的字节数，防止有人让服务器下载巨大的文件。

<a id="allowed-destinations"></a>
## 允许的地址与端口：`Ports`、`AnyPort`、`Allow`

默认只连接公网地址的 80 和 443 端口。

| 选项 | 作用 |
| --- | --- |
| `Ports` | 允许的端口，替换默认的 80 和 443，例如 `[]uint16{443, 8443}` |
| `AnyPort` | 允许任何端口，`Ports` 被忽略，地址检查仍然生效 |
| `Allow` | 额外允许的网段，先于内置的拒绝规则检查，用于有意访问的内部服务 |

客户端跟随重定向（最多 10 次），每一跳都经过同样的检查；要关闭跟随时设置返回的客户端的 `CheckRedirect`。

<a id="local-development"></a>
### 本地开发放行回环地址

开发时 webhook 的接收方往往在本机，用 `Allow` 放行回环地址，`cfg` 是应用的配置：

```go
opts := outbound.Options{AnyPort: true}
if cfg.Env == "local" {
    opts.Allow = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
}
client := outbound.New(opts)
```

<a id="retrying"></a>
## 用 `outbound.Retry` 重试临时失败

用 `Retry` 包装客户端的传输层，对方返回 503、连接被重置时自动重试：

```go
client := outbound.New(outbound.Options{Timeout: 5 * time.Second})
client.Transport = outbound.Retry(client.Transport, outbound.RetryOptions{})
```

- 只重试可以安全重复的请求，避免一次失败变成两次扣款：方法是 GET、HEAD、OPTIONS、TRACE、PUT、DELETE，或者带 `Idempotency-Key`（`X-Idempotency-Key`）请求头；并且请求体可以重新读取（用 `bytes`、`strings` 创建的可以，直接传入的流不行）。
- 只在连接失败或响应为 429、502、503、504 时重试；`ErrForbidden`、域名不存在和证书错误直接返回。
- `RetryOptions` 零值可用：最多 3 次（`Attempts`），首次等待 200ms 后翻倍加抖动（`Backoff`），单次最长等 5s（`MaxWait`），`Retry-After` 超过它时直接返回这个响应。
- 客户端的 `Timeout` 和请求的 context 限制包括重试在内的总时长。

要保证送达的请求（例如通知第三方订单已支付）放进[队列](../background/queue.md)的任务，由队列重试；`Retry` 只用于当前请求就要结果的场景。

<a id="using-with-other-components"></a>
## 交给其他组件和 SDK

- [Webhook](../features/webhooks.md#private-addresses) 的投递默认使用 `outbound.New(outbound.Options{AnyPort: true})`。
- [AI 助手](../ai/chat.md)的模型地址默认允许任何地址（内网自建的模型服务是正常需求）。允许租户填写模型地址时把客户端传给模型；`openai` 是 trpc-agent-go 的 `model/openai`，`option` 是 openai-go 的 `option`，`aiopenai` 是框架的 `ai/openai`：

  ```go
  client := outbound.New(outbound.Options{AnyPort: true, Timeout: -1}) // 流式响应关闭总超时
  llm := chat.NewModel(cfg, openai.WithOpenAIOptions(option.WithHTTPClient(client)))
  media := aiopenai.New(cfg, aiopenai.WithHTTPClient(client))
  ```

- 其他接收 `*http.Client` 的 SDK（例如 `WithHTTPClient` 选项）同样传入它。

<a id="testing"></a>
## 用 `outboundtest` 测试外部请求

`outboundtest.New(t)` 是离线的 `http.RoundTripper`：按方法和地址登记响应，记录发出的每个请求。把它的 `Client()` 交给被测代码：

```go
func TestPreview(t *testing.T) {
    fake := outboundtest.New(t)
    fake.On("GET https://example.com/post").Reply(
        outboundtest.Text(200, `<title>Hello</title>`).Header("Content-Type", "text/html"),
    )
    preview := &Preview{client: fake.Client()}

    got, err := preview.fetch(t.Context(), "https://example.com/post")
    require.NoError(t, err)
    assert.Equal(t, "Hello", got.Title)
    assert.Len(t, fake.Sent("GET https://example.com/post"), 1)
}
```

- `On` 的参数是可选的方法加地址：省略方法匹配任何方法，`*` 匹配任意字符，省略协议时 http 和 https 都匹配，不写 `?` 时忽略查询参数。多个登记都匹配时后登记的优先。
- `Reply` 的第 n 个响应给第 n 个请求，用完后一直返回最后一个；没有调用 `Reply` 时是 `Status(200)`。响应有 `JSON`、`Text`、`Status`、`Fail(err)`（模拟连接失败、超时）、`Handler`，用 `.Header(k, v)` 加响应头。
- 没有登记的请求返回错误，并在测试结束时让测试失败。
- `Sent(pattern)`、`Requests()` 返回记录的请求，请求体用 `JSON(&v)` 或 `Form()` 解析。
- 它替换了传输层，地址检查随之跳过。测试被拒绝的链接时登记 `outboundtest.Fail(outbound.ErrForbidden)`。
- 接收 `*http.Client` 的第三方 SDK 和框架的驱动（`sms/aliyun`、`push/aliyun` 等的 `New`）也用它测试。
