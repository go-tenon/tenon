# Electron 桌面端

[Electron](https://www.electronjs.org) 把一个网页装进原生的桌面程序。Tenon 用它把 `apps` 中的前端做成 Windows、macOS、Linux 上的桌面端：界面就是你的前端，一行不用改；桌面端另外有系统通知、托盘、开机自启、深度链接和自动更新。用户在系统浏览器中登录，接口调用由桌面端转发给服务器。

阅读前需要了解：[客户端](README.md)；[原生客户端](native.md)。

- [最小的例子](#minimal-example)
- [生成的文件](#generated-files)
- [它怎样工作](#how-it-works)
- [登录](#sign-in)
- [桌面能力](#capabilities)
- [在页面中调用桌面能力](#from-the-page)
- [自己加原生方法](#native-methods)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="minimal-example"></a>
## 最小的例子

下面为运营后台 `apps/admin` 生成一个桌面端。`admin` 要有登录页（用 `tenon add frontend` 生成时默认就有），桌面端经它登录。在项目根目录运行：

```bash
go tool tenon add client admin-desktop --kind electron --frontend admin --title "运营后台"
```

它生成 `clients/admin-desktop`，在 `apps/admin` 中加上授权页，把新的包加入 Bun workspace 并安装依赖。

然后在服务端登记这个客户端，ID 与目录名相同，见[原生客户端：登记客户端](native.md#registering)。

最后运行它：

```bash
go tool tenon dev --client admin-desktop
```

`tenon dev` 照常启动你的程序和前端的开发服务器，再打开桌面端的窗口（第一次运行时下载 Electron，约 100 MB）。窗口中是 `admin` 的登录页，上面只有一个“在浏览器中登录”按钮。点击后系统浏览器打开登录页面，登录并确认后回到桌面端，进入后台。修改前端代码后，窗口中的页面立刻更新；修改 `clients/admin-desktop/src` 中的主进程代码后，关掉窗口重新运行。

<a id="generated-files"></a>
## 生成的文件

| 文件 | 内容 |
| --- | --- |
| `tenon.json` | 种类和显示的前端：`{ "kind": "electron", "frontend": "admin" }` |
| `src/main.ts` | 主进程：创建宿主，逐行加上桌面能力 |
| `src/preload.ts` | 在页面中放好 `tenonHost` |
| `electron-builder.yml` | 打包的配置：应用 ID、名称、安全开关、更新源、深度链接的协议、各平台的安装包格式 |
| `assets/tray.png`、`tray@2x.png` | 托盘图标，换成你自己的 |
| `build/icon.png` | 应用图标（至少 512×512），换成你自己的 |
| `package.json` | 版本号，以及 `dev`、`bundle`、`package` 脚本 |

`apps/admin/src/routes/oauth/consent.tsx` 是加在前端中的授权页，见[原生客户端：登记客户端](native.md#registering)。

<a id="how-it-works"></a>
## 它怎样工作

一个 Electron 程序有两种进程：**主进程**运行 Node.js，能调用系统的能力；**渲染进程**是窗口中的网页，和浏览器中的一样，不能直接碰系统。桌面端的主进程由 `@go-tenon/electron` 做好了这几件事：

- **页面来自本地**：前端按 SPA 模式构建后打进安装包，窗口打开 `https://admin-desktop.localhost/admin/`。这个地址不会访问网络，请求由主进程从安装包中返回。离线时也能打开界面。
- **接口由主进程转发**：页面照常请求 `/api/...`，主进程转发给服务器，加上 token 和 `X-App-Version`。对页面来说接口和在网页上一样是同源的，前端代码不用改；token 只在主进程中，页面上的脚本拿不到。
- **网页上的渲染方式不影响桌面端**：前端在网页上是否在服务端渲染都可以，桌面端一律在窗口中渲染。

这些规则写在[网页宿主的约定](host.md)中。

<a id="sign-in"></a>
## 登录

前端的登录页（`src/routes/login.tsx`）在桌面端中显示 `BrowserSignIn`，在网页中显示原来的 `LoginForm`：

```tsx
const desktop = host()
return (
  <CenteredAuthLayout brand="运营后台" title="登录">
    {desktop ? <BrowserSignIn signIn={desktop.auth.signIn} redirect={redirect} /> : <LoginForm login={login} redirect={redirect} />}
  </CenteredAuthLayout>
)
```

`host()` 来自 `@go-tenon/host`，在桌面端中返回 `tenonHost`，在浏览器中返回 `undefined`。`desktop.auth.signIn` 打开系统浏览器，等用户登录并确认，换到 token 后保存在系统的安全存储中（`safeStorage`）。之后 `BrowserSignIn` 读取当前用户，跳回登录前的页面。

退出时，`src/auth.ts` 的 `logout` 先调用退出接口撤销这次登录，再让桌面端删除保存的 token：

```ts
export async function logout(): Promise<void> {
  await send("POST", "/auth/logout")
  await host()?.auth.signOut()
}
```

接口返回 `401` 时，桌面端删除 token，页面照常回到登录页。

<a id="capabilities"></a>
## 桌面能力

生成的 `src/main.ts` 中，每项桌面能力占一行，不需要的删掉那一行：

```ts
logs() // 主进程的日志写入文件，收集崩溃报告

const host = createHost({ id: "admin-desktop", server, base: "/admin/", renderer, preload, dev, window })

windowState(host) // 记住窗口的位置和大小
standardMenu(host) // 应用菜单和右键菜单
autoUpdate(host) // 自动更新，服务端要求升级时立即更新
notifications(host, { appId }) // 系统通知
badge(host) // 未读角标
tray(host, { title: "运营后台", icon }) // 关闭窗口时隐藏到托盘
autostart(host, { name: "admin-desktop" }) // 开机自启
const openLink = deepLinks(host, { scheme: "admin-desktop", base: "/admin/" }) // 深度链接

if (singleInstance(host, openLink)) {
  host.start()
}
```

| 能力 | 作用 |
| --- | --- |
| `logs` | 主进程的 `console` 输出同时写入日志目录中的 `main.log`，超过 5 MB 换成 `main.log.old`；崩溃报告保存在本地，设置 `crashReportURL` 时上传 |
| `windowState` | 记住窗口的位置、大小和是否最大化；显示器拔掉后不恢复到屏幕之外 |
| `standardMenu` | macOS 的标准菜单（没有“编辑”菜单时 ⌘C、⌘V 不起作用），Windows 和 Linux 没有菜单栏；右键菜单有剪切、复制、粘贴和拼写建议 |
| `autoUpdate` | 后台检查和下载新版本，退出时安装，见[打包、签名与更新](electron-release.md#auto-update) |
| `notifications` | 页面调用 `notify` 弹出系统通知，点击后回到窗口并跳到指定的页面 |
| `badge` | 页面调用 `badge` 设置未读数：macOS 的 Dock、Linux 的启动器显示数字 |
| `tray` | 关闭窗口时隐藏到托盘，应用继续运行，实时连接和通知不断开；从托盘菜单“退出”才真正退出 |
| `autostart` | 页面调用 `autostart.set(true)` 开机自启 |
| `deepLinks` | `admin-desktop://open/admin/orders/123` 打开桌面端并跳到这个页面，路径与网页上的相同 |
| `singleInstance` | 只运行一个实例：再次打开（包括点击深度链接）时回到已有的窗口 |

`appId` 与 `electron-builder.yml` 中的 `appId` 相同。Windows 按它把通知关联到应用，两处不同时通知不显示。

<a id="from-the-page"></a>
## 在页面中调用桌面能力

页面通过 `host()` 调用桌面能力。在浏览器中 `host()` 返回 `undefined`，所以每次调用都写成可选的，同一份代码在网页和桌面端中都能运行：

```ts
import { host } from "@go-tenon/host"

// 收到新通知时（例如事件流推送了未读数）
host()?.badge?.(unread)
host()?.notify?.({ title: "新订单", body: "订单 123 已付款", path: "/admin/orders/123" })

// 设置页中的“开机自启”开关
const enabled = (await host()?.autostart?.get()) ?? false
await host()?.autostart?.set(!enabled)
```

`unread` 是未读数，通常来自[站内通知](../features/notifications.md)的未读数推送。`notify` 的 `path` 是用户点击通知后跳转的页面。

前端的 `src/router.tsx` 已经订阅了桌面端的 `navigate` 事件：点击通知、打开深度链接时，页面跳到对应的路径。

有新版本下载好时，桌面端发出 `update` 事件，页面可以提示用户重启：

```ts
host()?.on("update", (status) => {
  if (status.state === "ready") showRestartNotice(status.version) // 你的提示，点击后调用 host()?.update?.install()
})
```

<a id="native-methods"></a>
## 自己加原生方法

需要框架没有提供的原生能力时（例如读写本地文件、调用打印机），在主进程中用 `host.handle` 注册一个方法，页面通过 `tenonHost` 调用它。方法的参数和返回值都是 JSON。下面加一个选择文件夹的方法：

```ts
// src/main.ts，在 host.start() 之前
import { dialog } from "electron"

host.handle("files.pickFolder", async () => {
  const win = host.window()
  const result = win ? await dialog.showOpenDialog(win, { properties: ["openDirectory"] }) : undefined
  return result?.canceled ? null : (result?.filePaths[0] ?? null)
})
```

页面中调用它。`TenonHost` 的类型中没有应用自己的方法，用交叉类型补上：

```ts
import { host, type TenonHost } from "@go-tenon/host"

type AppHost = TenonHost & { files?: { pickFolder(): Promise<string | null> } }

const folder = await (host() as AppHost | undefined)?.files?.pickFolder()
```

方法名中的点表示层级：`files.pickFolder` 在页面中是 `tenonHost.files.pickFolder`。桌面端只接受来自自己页面的调用，其他网页调用不到这些方法。

<a id="troubleshooting"></a>
## 常见问题

**Linux 上启动时报错 “The SUID sandbox helper binary was found, but is not configured correctly”**

Electron 的沙箱需要 root 所有的 `chrome-sandbox`，或者系统允许普通用户创建用户命名空间；Ubuntu 24.04 之后的 AppArmor 默认不允许。开发时给 `chrome-sandbox` 设置权限即可（路径在报错中）：

```bash
sudo chown root:root <路径>/chrome-sandbox && sudo chmod 4755 <路径>/chrome-sandbox
```

安装包（AppImage、deb）由 electron-builder 处理，用户不会遇到。

**Linux 上看不到托盘图标**

托盘依赖桌面环境的 AppIndicator 支持，GNOME 默认没有，需要安装 AppIndicator 扩展。看不到托盘时，关掉窗口后再次打开应用，就会回到已有的窗口。

**Linux 上提示 “no system keyring”**

系统中没有钥匙串服务（gnome-keyring、KWallet），token 改用固定的密钥加密，与没有钥匙串时的 Chromium 相同。安装钥匙串服务后重新登录即可。

**点击“在浏览器中登录”后，浏览器显示“无法授权”**

服务端没有登记这个客户端，或者回调地址不对，见[原生客户端：常见问题](native.md#troubleshooting)。

**页面一直停在加载中，没有跳到登录页**

开发时检查 `tenon dev` 的输出中前端的开发服务器是否启动了：桌面端最多等它 60 秒。打包后检查 `src/main.ts` 中服务器的地址。

<a id="api-reference"></a>
## API 参考

包 `@go-tenon/electron/main`。

### createHost

```ts
function createHost(options: HostOptions): Host
```

| `HostOptions` 字段 | 说明 |
| --- | --- |
| `id` | 客户端的名字：页面的主机名、OAuth 的 client ID、`X-App-Version` 的平台名 |
| `server` | 服务器的地址：转发的目标，也是授权服务器的地址 |
| `base` | 前端的挂载路径，例如 `/admin/` |
| `renderer` | 前端按 SPA 模式构建的目录 |
| `preload` | 构建好的 preload 脚本 |
| `proxy` | 转发到服务器的路径前缀，默认 `["/api/"]` |
| `dev` | 开发时前端开发服务器的地址 |
| `window` | 主窗口的其他选项，例如大小 |

| `Host` 成员 | 说明 |
| --- | --- |
| `start()` | 等应用就绪后打开主窗口 |
| `window()` | 主窗口 |
| `handle(name, fn)` | 注册页面可以调用的方法，在 `start` 之前调用 |
| `emit(event, data)` | 向页面发出事件 |
| `whenWindow(fn)` | 主窗口创建后、加载页面前调用 `fn` |
| `onResponse(fn)` | 每个转发的请求得到响应后调用 `fn` |

### 桌面能力

```ts
function logs(options?: { crashReportURL?: string; maxSize?: number }): void
function windowState(host: Host, file?: string): void
function standardMenu(host: Host): void
function notifications(host: Host, options: { appId: string }): void
function badge(host: Host, options?: { windowsOverlay?: NativeImage }): void
function tray(host: Host, options: { title: string; icon: NativeImage; menu?: MenuItemConstructorOptions[] }): Promise<Tray>
function autostart(host: Host, options: { name: string }): void
function deepLinks(host: Host, options: { scheme: string; base: string }): (argv: string[]) => void
function singleInstance(host: Host, onSecondInstance?: (argv: string[]) => void): boolean
```

`autoUpdate` 在单独的入口 `@go-tenon/electron/update` 中，只有用它的应用需要安装 `electron-updater`：

```ts
function autoUpdate(host: Host, options?: { interval?: number }): void
```

`@go-tenon/electron/preload` 没有导出，导入它就会在页面中放好 `tenonHost`。页面一侧的 `tenonHost` 见[网页宿主的约定](host.md#tenon-host)。
