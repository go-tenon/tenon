# 网页宿主的约定

网页宿主是装着网页的原生程序，例如 Electron、Tauri、Wails。`apps` 中的前端在宿主中运行时，前端代码和在浏览器中完全相同，宿主替它做网页做不到的事。这一页是宿主和页面之间的约定：宿主要做到哪些事，页面能通过 `tenonHost` 调用什么。用 Electron 写桌面端时不需要读它，`@go-tenon/electron` 已经按它实现；要为另一种宿主（例如 Tauri）写实现，或者排查宿主中的问题时再读。

阅读前需要了解：[客户端](README.md)；[原生客户端](native.md)。

- [宿主要做到的事](#host-duties)
- [页面看到的 tenonHost](#tenon-host)
- [为什么这样约定](#why)
- [实现一个新的宿主](#new-host)

<a id="host-duties"></a>
## 宿主要做到的事

1. **页面来自本地。** 前端按 SPA 模式构建（设置 `TENON_RENDER=csr` 后运行前端的构建，见[打包](electron-release.md#packaging)），文件放在安装包中。页面的地址是 `https://<客户端名>.localhost/<挂载路径>`，宿主拦截这个主机的请求：
   - 构建出的文件原样返回；
   - 前端路由中的页面（没有扩展名、找不到文件的路径）返回页面外壳 `_shell.html`；
   - 带扩展名但找不到的文件、越出前端目录的路径返回 `404`。
2. **页面外壳带 CSP。** 外壳中的内联脚本带着占位的 nonce `__tenon_csp_nonce__`。每次返回外壳时换成新的随机 nonce，并带上与 Go 托管前端时相同的内容安全策略（见[托管前端：安全](../frontend/hosting.md#security)），`connect-src` 另外允许实时频道的 `wss://` 地址。
3. **接口由宿主转发。** 同源的 `/api/*` 转发给服务器：
   - 去掉页面的 `Origin`、`Referer`、`Cookie` 和 `Authorization`；
   - 加上宿主保存的 `Authorization: Bearer <token>` 和 `X-App-Version: <客户端名>/<版本>`；
   - 请求体和响应体流式传递，事件流照常工作；
   - 不跟随重定向，token 不会被带到其他主机；
   - 响应去掉 `Set-Cookie`。
4. **token 只在宿主中。** 宿主在系统浏览器中完成登录（见[原生客户端](native.md#sign-in)），把 token 保存在系统的安全存储中。转发的请求收到 `401` 时，删除发出请求时用的 token。
5. **实时频道带认证。** 页面直接连接服务器的 WebSocket，宿主在连接请求上加上 `Authorization: Bearer`。服务端对带这个请求头的连接不检查来源。
6. **其他网页没有原生能力。** 外链交给系统浏览器打开；只有宿主自己的页面能调用 `tenonHost`；不允许嵌入 `<webview>`。

<a id="tenon-host"></a>
## 页面看到的 tenonHost

宿主在页面的全局放一个 `tenonHost`。页面用 `@go-tenon/host` 的 `host()` 取得它，在浏览器中得到 `undefined`：

```ts
import { host } from "@go-tenon/host"

const desktop = host()
if (desktop) console.log(desktop.kind, desktop.platform, desktop.appVersion) // electron linux 1.2.0
```

| 成员 | 说明 |
| --- | --- |
| `contract` | 约定的版本，目前是 1 |
| `kind` | 宿主的种类，例如 `electron` |
| `platform` | `windows`、`macos`、`linux`、`ios` 或 `android` |
| `appVersion` | 客户端的版本，与 `X-App-Version` 中的相同 |
| `auth.signIn()` | 在系统浏览器中登录，token 保存好之后完成；用户取消或超时时抛出错误 |
| `auth.signOut()` | 删除保存的 token |
| `notify?({ title, body, path })` | 系统通知，点击后发出 `navigate` 事件 |
| `badge?(count)` | 未读角标，0 表示清除 |
| `update?.check()`、`update?.install()` | 检查更新；安装更新并重启 |
| `autostart?.get()`、`autostart?.set(on)` | 开机自启 |
| `on(event, listener)` | 订阅事件，返回取消订阅的函数：`navigate`（要跳转的页面路径）、`update`（更新的状态） |

带问号的成员是可选的，宿主不支持这项能力时就没有这个成员，例如移动端的宿主没有 `autostart`。页面调用前先判断，例如 `host()?.badge?.(3)`。

<a id="why"></a>
## 为什么这样约定

- **为什么从本地加载，而不是打开服务器上的网页**：打开服务器上的网页，桌面端就只是一个套着窗口的浏览器：离线打不开；远程的内容运行在有原生能力的窗口里；用的是浏览器的 cookie 会话。从本地加载时，界面的版本和安装包一致，原生能力只给自己的页面。
- **为什么接口由宿主转发**：页面直接跨域请求服务器，就要开放跨域，token 也要交给页面。由宿主转发时，页面看到的接口是同源的，前端代码与网页上相同；token 只在宿主中，页面中的脚本即使被注入也拿不到。
- **为什么页面来自 `https://<名字>.localhost`，而不是 `app://` 这样的自定义协议**：前端路由只把 http(s) 的同源地址当作站内跳转，例如没有登录时跳到登录页；在自定义协议上，这样的跳转会被当作不安全的地址拒绝。`.localhost` 是保留的域名，不会解析到外部，宿主拦截它的请求，不会访问网络。
- **为什么每个方法都是异步的、参数是 JSON**：宿主可以用任何语言实现，Electron 用 TypeScript，Tauri 用 Rust，Wails 用 Go。页面和宿主之间只能传可以序列化的数据。

<a id="new-host"></a>
## 实现一个新的宿主

新的宿主需要：

1. 按[宿主要做到的事](#host-duties)实现本地加载、转发、登录、保存 token 和实时频道的认证。登录中与界面无关的部分（PKCE、检查回调、换取 token）用 `@go-tenon/client` 的 `createAuthorization`、`readCallback`、`exchangeCode`，或者用宿主的语言按[客户端协议](../http/protocol.md#native-clients)实现。
2. 在页面的全局放一个 `tenonHost`，形状与 `@go-tenon/host` 的 `TenonHost` 类型相同，`kind` 是新宿主的种类。
3. 用 Electron 的实现对照检查：`js/electron/src` 中 `routes.ts`、`forward.ts`、`csp.ts` 是与 Electron 无关的规则，可以照着移植，它们的测试说明了每条规则的边界。
