# 网页宿主的约定

网页宿主（Electron、Tauri、Wails）在原生窗口中运行 `apps` 中的前端。这一页是宿主和页面之间的约定：宿主要做到的事，和页面能通过 `tenonHost` 调用的能力。`@go-tenon/electron` 已经按它实现。

包：`@go-tenon/host`。

<a id="host-duties"></a>
## 宿主要做到的事

1. **页面来自本地。** 前端按 SPA 模式构建（`TENON_RENDER=csr`，见[打包](electron.md#packaging)），文件放在安装包中。页面的地址是 `https://<客户端名>.localhost/<挂载路径>`，宿主拦截这个主机的请求：
   - 构建出的文件原样返回；
   - 前端路由中的页面（没有扩展名、找不到文件的路径）返回页面外壳 `_shell.html`；
   - 带扩展名但找不到的文件、越出前端目录的路径返回 `404`。
2. **页面外壳带 CSP。** 外壳中的内联脚本带着占位的 nonce `__tenon_csp_nonce__`。每次返回外壳时换成新的随机 nonce，并带上与 Go 托管前端时相同的内容安全策略（见[托管前端：安全](../frontend/hosting.md#security)），`connect-src` 另外允许实时频道的 `wss://` 地址。
3. **接口由宿主转发。** 同源的 `/api/*` 转发给服务器：
   - 去掉页面的 `Origin`、`Referer`、`Cookie` 和 `Authorization`；
   - 加上宿主保存的 `Authorization: Bearer <token>` 和 `X-App-Version: <客户端名>/<版本>`；
   - 请求体和响应体流式传递，事件流照常工作；
   - 不跟随重定向：token 不会被带到其他主机；
   - 响应去掉 `Set-Cookie`。
4. **token 只在宿主中。** 宿主在系统浏览器中完成登录（见[在系统浏览器中登录](native.md#sign-in)），把 token 保存在系统的安全存储中。转发的请求收到 `401` 时，删除发出请求时用的 token。
5. **实时频道带认证。** 页面直接连接服务器的 WebSocket，宿主在握手请求上加上 `Authorization: Bearer`。服务端对带这个请求头的连接不检查来源。
6. **原生能力只给自己的页面。** 外链交给系统浏览器打开；只有宿主自己的页面能调用 `tenonHost`；不允许嵌入 `<webview>`。

页面中的接口调用因此在网页和各个宿主中完全相同，页面拿不到 token。

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
| `contract` | 约定的版本，目前是 1（`HOST_CONTRACT`） |
| `kind` | 宿主的种类，例如 `electron` |
| `platform` | `windows`、`macos`、`linux`、`ios` 或 `android` |
| `appVersion` | 客户端的版本，与 `X-App-Version` 中的相同 |
| `auth.signIn()` | 在系统浏览器中登录，token 保存好之后完成；用户取消或超时时抛出错误 |
| `auth.signOut()` | 删除保存的 token；先调用应用的退出接口，再调用它 |
| `notify?({ title, body, path })` | 系统通知，点击后发出 `navigate` 事件 |
| `badge?(count)` | 未读角标，0 表示清除 |
| `update?.check()`、`update?.install()` | 检查更新；安装更新并重启 |
| `autostart?.get()`、`autostart?.set(on)` | 开机自启 |
| `on(event, listener)` | 订阅事件，返回取消订阅的函数：`navigate`（要跳转的页面路径）、`update`（更新的状态） |

带问号的成员是可选的：宿主不支持这项能力时没有这个成员，例如移动端的宿主没有 `autostart`。页面调用前先判断，例如 `host()?.badge?.(3)`。每个方法都是异步的，参数和返回值都是 JSON。
