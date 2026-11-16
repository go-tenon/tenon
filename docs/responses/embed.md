# 嵌入第三方网站

客服访客端这类挂件要放进客户的网站：客户在自己的页面中加一行 `<script>`，页面右下角出现入口按钮，点开是你的应用中的一个页面。`@go-tenon/embed` 提供两端：宿主页上的挂件（入口按钮、面板和 iframe），以及 iframe 中的页面与宿主之间的连接；服务端的 `widget` 包校验宿主网站签名的用户身份，`SPAOptions.FrameAncestorsFunc` 按租户放行嵌入的网站。

- [结构](#structure)
- [挂件脚本](#loader)
- [iframe 中的页面](#frame)
- [宿主网站的用户](#identity)
- [只允许登记的网站嵌入](#frame-ancestors)
- [协议](#protocol)
- [API 参考](#api-reference)

<a id="structure"></a>
## 结构

```text
客户的网站 https://shop.example.com
└── <script src="https://app.example.com/widget.js?id=acme">   挂件脚本：入口按钮、面板（Shadow DOM，样式互不影响）
    └── <iframe src="https://app.example.com/widget/acme">       你的应用中的页面：React + shadcn 的组件
            ⇅ postMessage（锁定双方的窗口和来源）
```

- 挂件脚本是原生 TypeScript，不依赖任何包，打包后约 6 KB（gzip 约 3 KB），短缓存。
- iframe 中的页面与宿主页面的样式和脚本隔离，是一个普通的[单页应用](spa.md)。
- 第三方 cookie 在 iframe 中不可用：iframe 中的页面用请求体和 `Authorization` 头传递访客令牌，保存在 iframe 自己的存储中，不依赖 cookie。

`@go-tenon/embed` 随框架分发。在根目录 `package.json` 的 `workspaces` 中加上 `.tenon/js/embed`，用到它的前端加上依赖：

```json
{ "dependencies": { "@go-tenon/embed": "workspace:*" } }
```

<a id="loader"></a>
## 挂件脚本

挂件脚本的入口读取 `<script>` 的地址和宿主的设置，调用 `createWidget`：

```ts
// apps/widget/src/loader.ts
import { createWidget } from "@go-tenon/embed"

const script = document.currentScript as HTMLScriptElement
const tenant = new URL(script.src).searchParams.get("id") ?? ""
const settings = (window as { AcmeSettings?: { identity?: string } }).AcmeSettings ?? {}

;(window as { Acme?: unknown }).Acme = createWidget({
  src: new URL(`/widget/${encodeURIComponent(tenant)}`, script.src).href,
  identity: settings.identity,
  title: "在线客服",
})
```

用 Bun 打包成一个文件，由应用提供（`Cache-Control` 用几分钟的短缓存，发布后客户的网站很快用上新版本）：

```bash
bun build apps/widget/src/loader.ts --minify --outfile apps/widget/public/widget.js
```

客户的网站：

```html
<script>window.AcmeSettings = { identity: "<宿主后端签名的身份，匿名访客不写>" }</script>
<script src="https://app.example.com/widget.js?id=acme" async></script>
```

挂件做了这些事：

- 入口按钮和面板放在 Shadow DOM 中；窄屏时面板占满屏幕。
- iframe 发来 `ready` 后，发出宿主页面的地址、标题、来源页、语言和签名的身份；宿主是单页应用时，切换路由后重新发送。
- 只接受这个 iframe、这个来源发来的消息，发出的消息也只发给这个来源。
- iframe 报告的未读数显示在按钮上；面板收起时，iframe 发来的提示显示在按钮旁，点击后展开。

`createWidget` 返回的对象通常挂在 `window` 上，客户的网站可以调用：

| 方法 | 说明 |
| --- | --- |
| `open()`、`close()`、`toggle()`、`isOpen()` | 展开、收起；`launcher: false` 时不显示入口按钮，由网站自己的按钮调用 `open` |
| `login(identity)`、`logout()` | 宿主的用户登录或退出后调用，重新加载 iframe，丢弃上一个身份的状态 |
| `track(event, properties)` | 把宿主页的事件转给 iframe，例如“加入购物车” |
| `on(name, listener)` | `ready`、`open`、`close`、`unread`、`toast`、`identity-expired` |
| `destroy()` | 移除挂件，恢复 `history` |

`identity-expired` 表示 iframe 认为身份已经失效（例如过期），宿主应当重新签发并调用 `login`。

<a id="frame"></a>
## iframe 中的页面

iframe 中的页面启动时连接宿主：

```tsx
import { useHost } from "@go-tenon/embed/react"

function Widget() {
  const host = useHost()
  const ctx = host.state.context // 宿主页面、签名的身份、语言、附带的数据；连接之前为 null
  // 有新消息：host.setUnread(n)；面板收起时提示：host.toast("客服回复了你")；关闭：host.close()
}
```

不用 React 时调用 `connectHost()`，它返回同样的方法和 `subscribe`。

- 页面启动时发出 `ready`（不带任何数据），宿主随后发来上下文。
- 只接受**直接父窗口**的消息，第一条合法消息锁定宿主的来源，之后只接受这个来源，发出的消息也只发给它。宿主页面中其他 iframe 发来的同样形状的消息不能锁定来源。
- `origins` 选项可以进一步限定宿主的来源，例如租户登记的域名。
- 直接打开页面（不在 iframe 中）时也可以运行，状态保持未连接。

<a id="identity"></a>
## 宿主网站的用户

客户网站上的用户已经登录时，挂件应当知道他是谁，而访客不能冒充其他人。做法是：客户的**后端**用租户的密钥对用户身份签名，页面把签名交给挂件，iframe 中的页面把它交给你的接口，换取访客令牌。

签名是 HS256 的 JWT，客户的后端可以用任何语言的 JWT 库签发：`sub` 是用户在客户网站中的 ID，必须有 `iat` 和 `exp`，有效期不超过 24 小时，可以附带 `name`、`email` 和 `attributes`。后端是 Go 时：

```go
token, err := widget.Sign(tenantSecret, widget.Identity{ID: user.ID, Name: user.Name, Email: user.Email}, 10*time.Minute)
```

你的应用中，换取访客令牌的接口校验它：

```go
id, err := widget.Verify(tenant.WidgetSecret, in.Body.Identity)
if err != nil {
    return nil, err // widget.ErrInvalid：401
}
// id.ID、id.Name……找到或创建这个租户中的访客，签发访客令牌
```

`Verify` 只接受 HS256，要求 `sub`、`iat`、`exp`，拒绝过期的、尚未生效的和有效期超过 24 小时的签名，允许一分钟的时钟偏差。密钥至少 32 字节，每个租户一个，保存在你的数据库中，在后台中展示给租户。

<a id="frame-ancestors"></a>
## 只允许登记的网站嵌入

iframe 中的页面默认不允许被其他网站嵌入（[`frame-ancestors 'none'`](spa.md)）。挂件的页面按租户放行它登记的域名：

```go
router.Handle("/widget/*", httpx.NewSPA(widgetapp.Dist, httpx.SPAOptions{
    Base: "/widget/",
    FrameAncestorsFunc: func(r *http.Request) []string {
        tenant := strings.Split(strings.TrimPrefix(r.URL.Path, "/widget/"), "/")[0]
        return domains.Of(r.Context(), tenant) // 例如 ["https://shop.example.com"]
    },
}))
```

没有登记的网站嵌入这个页面时，浏览器拒绝显示。`FrameAncestorsFunc` 为每个 HTML 响应调用一次，不合法的来源被忽略，没有合法的来源时不允许嵌入。

挂件用到的接口只开放 iframe 用到的那些，并且只接受访客令牌。

<a id="protocol"></a>
## 协议

消息都带着命名空间和版本 `{ ns: "tenon:embed", v: 1, type, payload }`，与宿主页面中其他脚本的消息区分开；收到的内容逐个字段检查，不符合协议的消息被忽略。

| 方向 | 类型 | 内容 |
| --- | --- | --- |
| iframe → 宿主 | `ready` | 页面已经可以接收消息 |
| | `open`、`close` | 请宿主展开、收起面板 |
| | `unread` | `{ count }`，按钮上的未读数 |
| | `toast` | `{ message }`，面板收起时的提示 |
| | `identity-expired` | 身份已经失效 |
| 宿主 → iframe | `context` | `{ page: { url, title, referrer }, identity, locale, data }` |
| | `visibility` | `{ open }` |
| | `track` | `{ event, properties }` |
| | `shutdown` | 挂件被移除 |

<a id="api-reference"></a>
## API 参考

| 名称 | 说明 |
| --- | --- |
| `createWidget(options)` | 宿主页上的挂件：`src`、`identity`、`data`、`title`、`launcher`、`position`、`color`、`width`、`height`、`container` |
| `connectHost(options)` | iframe 中的页面与宿主的连接：`origins` |
| `useHost(options)`、`hostConnection(options)` | `@go-tenon/embed/react`：React 中的连接，页面中只连接一次 |
| `readFrameMessage`、`readHostMessage`、`envelope` | 协议的消息 |
| `widget.Sign`、`widget.Verify`、`widget.ErrInvalid` | Go：宿主网站签名的用户身份 |
| `httpx.SPAOptions.FrameAncestorsFunc` | Go：按请求放行嵌入的网站 |
