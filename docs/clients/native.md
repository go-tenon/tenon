# 原生客户端

安装在设备上的客户端（网页宿主和原生界面）共用的做法：登记客户端、在系统浏览器中登录、保存 token、版本和强制升级、实时频道、深度链接。线路上的细节见[客户端协议：原生客户端](../http/protocol.md#native-clients)。

包：`@go-tenon/client`。

<a id="registering"></a>
## 登记客户端

客户端用应用的 [OAuth 授权服务器](../security/oauth-server.md)登录。在 `oauthserver.Options` 的 `Clients` 中登记它并设置 `FirstParty`，`ConsentURL` 指向前端中的授权页：

```go
oauthserver.Options{
    Issuer: "https://example.com",
    Clients: []oauthserver.Client{{
        ID:           "admin-desktop",                       // 与 clients 中的目录名相同
        Name:         "运营后台",                              // 显示在授权页上
        RedirectURIs: []string{"http://127.0.0.1/callback"}, // 桌面端：本机地址，端口每次不同
        FirstParty:   true,
    }},
    ConsentURL: "/admin/oauth/consent",
}
```

- 装配授权服务器见 [OAuth 授权服务器：装配](../security/oauth-server.md#assembling)；只给自己的客户端登录时不设置 `Resources` 和 `Scopes`。
- 授权页由 `tenon add client` 加在网页宿主显示的前端中（`src/routes/oauth/consent.tsx`），用户在上面点“继续”确认登录。
- App 的回调地址用自定义 scheme（例如 `com.example.admin:/oauth/callback`）或 Universal Link / App Link。

<a id="sign-in"></a>
## 在系统浏览器中登录

客户端在系统浏览器中打开授权地址，用户在网页上登录并确认，浏览器带着授权码跳回回调地址，客户端用授权码和 PKCE 换到 token。TypeScript 写的客户端（Electron 的主进程、React Native）用 `@go-tenon/client`：

```ts
import { createAuthorization, exchangeCode, readCallback } from "@go-tenon/client"

const issuer = "https://example.com"
const auth = await createAuthorization({ issuer, clientId: "admin-desktop", redirectUri })
openInBrowser(auth.url)
const code = readCallback(callbackUrl, auth, issuer) // 检查 state 和 iss，取出授权码
const token = await exchangeCode({ issuer, clientId: "admin-desktop", auth, code })
```

`redirectUri` 是本次的回调地址，`openInBrowser` 和 `callbackUrl` 由平台提供。Electron 桌面端已经做好，见 [Electron 桌面端：登录](electron.md#sign-in)。

换到的是**登录 token**，与密码登录得到的相同：可以访问整个 API，出现在用户的[设备列表](../security/api-tokens.md#listing-tokens)中。

> [!WARNING]
> 登录只在系统浏览器中完成，不放进内嵌的网页视图：应用能读到其中输入的密码，Google 等第三方登录也拒绝内嵌视图。

<a id="tokens"></a>
## 保存 token 和退出

- token 放在系统的安全存储中（钥匙串、Windows 凭据管理器、Android Keystore），请求时放在 `Authorization: Bearer` 中。
- 收到 `401` 时删除 token，回到登录。
- 退出时先调用应用的退出接口或 `POST /oauth/revoke` 撤销这次登录，再删除本地的 token。

<a id="versions"></a>
## 版本和强制升级

每个请求带上 `X-App-Version: <客户端名>/<版本>`，例如 `admin-desktop/1.2.0`。服务端安装 [App 最低版本](../http/middleware.md#app-version)中间件后，按客户端设置最低版本：

```yaml
http:
  app_version:
    minimum:
      admin-desktop: 1.2.0
      ios: 1.4.0
```

低于最低版本的请求返回 `426`，错误码 `app.upgrade_required`，客户端收到后让用户升级。Electron 桌面端自动更新，见[强制升级](electron.md#forced-upgrade)。

<a id="realtime"></a>
## 实时频道和通知

- 实时频道用同一个 token 连接，见[客户端协议：实时频道](../http/protocol.md#realtime)。
- 客户端运行时从[站内通知](../features/notifications.md)的事件流收到新通知，再弹出系统通知。
- App 在后台或被关掉后用[移动推送](../features/push.md)送达。桌面端退出后收不到通知，通常常驻托盘并开机自启。

<a id="links"></a>
## 深度链接和网页

- 客户端中的页面使用与网页相同的路径，例如 `/admin/orders/123`。邮件和通知中只生成一种链接，各客户端把它对应到自己的页面，例如 `admin-desktop://open/admin/orders/123`。
- 自己网站的页面在系统浏览器中打开。第三方网页（支付页）可以在内嵌视图中打开，不给它原生能力，存储与客户端的页面隔离。

<a id="troubleshooting"></a>
## 排查授权失败

| 现象 | 原因 |
| --- | --- |
| 浏览器中显示 `oauth.invalid_client` | `Clients` 中没有这个 ID，或者它没有设置 `FirstParty` 而服务器又没有设置 `Resources` |
| 浏览器中显示 `oauth.invalid_redirect_uri` | 回调地址不在 `RedirectURIs` 中。本机地址只忽略端口：`127.0.0.1` 和 `localhost` 不能混用 |
| 换取 token 时返回 `invalid_grant` | 授权码 10 分钟内有效，只能用一次；`code_verifier` 和 `redirect_uri` 要与授权请求中的相同 |
