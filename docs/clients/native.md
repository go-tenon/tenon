# 原生客户端

桌面端和 App 安装在用户的设备上，和网页有几点不同：它们不能用浏览器的会话 cookie 登录，要自己保存 token；它们的版本由用户决定什么时候升级，服务端要能要求太旧的版本先升级；它们还能收到系统通知、被链接打开。这一页说明所有安装在设备上的客户端共用的做法，不管是 Electron 这样的网页宿主，还是 SwiftUI、Flutter 这样的原生界面。

阅读前需要了解：[客户端](README.md)；[OAuth 授权服务器](../security/oauth-server.md)。

- [登记客户端](#registering)
- [在系统浏览器中登录](#sign-in)
- [保存 token 和退出](#tokens)
- [版本和强制升级](#versions)
- [实时频道和通知](#realtime)
- [深度链接和网页](#links)
- [常见问题](#troubleshooting)

<a id="registering"></a>
## 登记客户端

客户端在系统浏览器中登录，登录用的是应用的 [OAuth 授权服务器](../security/oauth-server.md)。先在授权服务器中登记客户端，设置 `FirstParty`，表示它是应用自己的客户端：

```go
oauth := oauthserver.New(a.db, tokens, oauthserver.Options{
    Issuer: "https://example.com",
    Clients: []oauthserver.Client{{
        ID:           "admin-desktop",                       // 与 clients 中的目录名相同
        Name:         "运营后台",                              // 显示在授权页上
        RedirectURIs: []string{"http://127.0.0.1/callback"}, // 桌面端：本机地址，端口每次不同
        FirstParty:   true,
    }},
    ConsentURL: "/admin/oauth/consent", // 前端中的授权页
})
router.Handle(oauth.MetadataPath(), oauth.Metadata())
oauth.Routes(router)
oauth.Register(api, rest.RequireToken(api, tokens.Validate))
```

`a.db` 是[连接数据库](../database/database.md#connecting)时得到的连接，`tokens` 是 [API 令牌](../security/api-tokens.md)的存储，`router` 和 `api` 是应用的路由和 [REST API](../http/routing.md)。只给自己的客户端登录、不对第三方开放时，`Resources` 和 `Scopes` 都不设置。

`ConsentURL` 指向前端中的授权页。用户在浏览器中登录后，在这一页上点“继续”，确认在这个客户端上登录。`tenon add client` 生成网页宿主时，会在它显示的前端中加上这一页（`src/routes/oauth/consent.tsx`）。

回调地址按客户端的种类选择：

| 客户端 | 回调地址 |
| --- | --- |
| 桌面端 | `http://127.0.0.1/callback`：客户端在随机端口上临时监听，端口不用登记 |
| App | 自定义 scheme（例如 `com.example.admin:/oauth/callback`），或者 Universal Link / App Link 的 https 地址 |

<a id="sign-in"></a>
## 在系统浏览器中登录

登录放在系统浏览器中完成，而不是在客户端里做一个账号密码表单：密码、第三方登录、企业单点登录、二次验证都在网页上，客户端不用各做一遍，也接触不到用户的密码。这是 OAuth 为原生应用规定的做法（RFC 8252）。

一次登录分四步，线路上的细节见[客户端协议：原生客户端](../http/protocol.md#native-clients)：

1. 客户端生成随机的 `code_verifier` 和 `state`，在系统浏览器中打开授权地址。
2. 用户在网页上登录（已经登录时跳过），在授权页上点“继续”。
3. 浏览器跳回客户端的回调地址，带着授权码。
4. 客户端用授权码和 `code_verifier` 换到 token。

换到的是**登录 token**，与用户在网页上用密码登录得到的相同：权限不受限制，可以访问整个 API；它代表一台设备，出现在用户的[设备列表](../security/api-tokens.md#listing-tokens)中，“退出其他设备”会撤销它。

TypeScript 写的客户端（Electron 的主进程、React Native）可以直接用 `@go-tenon/client` 中的函数：

```ts
import { createAuthorization, exchangeCode, readCallback } from "@go-tenon/client"

const issuer = "https://example.com"
const auth = await createAuthorization({ issuer, clientId: "admin-desktop", redirectUri })
openInBrowser(auth.url) // 在系统浏览器中打开
const code = readCallback(callbackUrl, auth, issuer) // 收到回调后：检查 state 和 iss，取出授权码
const token = await exchangeCode({ issuer, clientId: "admin-desktop", auth, code })
```

`redirectUri` 是本次的回调地址，`openInBrowser` 和 `callbackUrl` 由客户端所在的平台提供。Electron 桌面端已经由 `@go-tenon/electron` 做好了这些，见 [Electron 桌面端：登录](electron.md#sign-in)。

> [!WARNING]
> 不要在客户端内嵌的网页视图中登录。内嵌视图中输入的密码，应用能读到；Google 等第三方登录也会直接拒绝内嵌视图。登录一律在系统浏览器中完成。

<a id="tokens"></a>
## 保存 token 和退出

- **保存**：token 放在系统的安全存储中，例如 macOS 和 iOS 的钥匙串、Windows 的凭据管理器、Android Keystore。请求时放在 `Authorization: Bearer` 请求头中。
- **失效**：收到 `401` 时这次登录已经失效（过期、在别的设备上被退出、修改了密码），删除保存的 token，回到登录。
- **退出**：先调用应用的退出接口撤销这次登录，或者把 token 交给 `POST /oauth/revoke`，再删除本地的 token。只删本地的 token 时，服务端的这次登录要等过期才失效。

<a id="versions"></a>
## 版本和强制升级

每个请求带上请求头 `X-App-Version`，值是“平台名/版本”。平台名用客户端的名字，版本是安装包的版本：

```http
X-App-Version: admin-desktop/1.2.0
```

服务端安装了 [App 最低版本](../http/middleware.md#app-version)中间件后，可以按客户端设置最低版本：

```yaml
http:
  app_version:
    minimum:
      admin-desktop: 1.2.0
      ios: 1.4.0
```

版本低于最低版本的请求返回 `426`，错误码是 `app.upgrade_required`。客户端收到后停下来，让用户升级。Electron 桌面端会自动下载新版本并安装，见[打包、签名与更新](electron-release.md#forced-upgrade)。

<a id="realtime"></a>
## 实时频道和通知

- **实时频道**：连接时传入同一个 token，用 SDK 的 token 参数，或者放在连接请求的 `Authorization` 请求头中，见[客户端协议：实时频道](../http/protocol.md#realtime)。
- **通知**：客户端在运行时，从[站内通知](../features/notifications.md)的事件流收到新通知，再弹出系统通知。
- **离线推送**：App 在后台或者被关掉后，用[移动推送](../features/push.md)送达。桌面端没有可靠的系统推送服务，应用退出后收不到通知，所以桌面端通常常驻托盘，配合开机自启。

<a id="links"></a>
## 深度链接和网页

- **深度链接**：客户端中的页面使用与网页相同的路径，例如 `/admin/orders/123`。邮件和通知中只生成一种链接，各客户端把它对应到自己的页面，例如 Electron 桌面端打开 `admin-desktop://open/admin/orders/123`。
- **自己网站的页面**：在系统浏览器中打开，不放进客户端内嵌的网页视图。浏览器中没有登录时，用户在浏览器中登录一次。
- **第三方网页**（支付页、帮助文档）：可以在内嵌视图中打开，但不给它任何原生能力，并且与客户端自己的页面隔离存储。

<a id="troubleshooting"></a>
## 常见问题

**浏览器中显示“无法授权（oauth.invalid_client）”**

授权服务器中没有登记这个 client ID，或者它没有设置 `FirstParty`，而服务器又没有设置 `Resources`。检查 `Clients` 中的 `ID` 与客户端的名字相同。

**浏览器中显示“无法授权（oauth.invalid_redirect_uri）”**

回调地址不在 `RedirectURIs` 中。桌面端登记 `http://127.0.0.1/callback`，不要写端口，也不要写成 `localhost`：本机地址只忽略端口，主机名要完全相同。

**换取 token 时返回 `invalid_grant`**

授权码 10 分钟内有效，只能用一次；`code_verifier` 和 `redirect_uri` 必须与这次授权请求中的相同。重新登录一次即可。
