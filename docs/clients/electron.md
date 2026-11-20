# Electron 桌面端

用 Electron 把 `apps` 中的一个前端做成 Windows、macOS、Linux 上的桌面端：页面从安装包加载，接口由主进程转发并加上 token（[网页宿主的约定](host.md)），用户在系统浏览器中登录。这一页还包括打包、签名、自动更新和强制升级。

包：`@go-tenon/electron`、`@go-tenon/host`。

<a id="minimal-example"></a>
## 示例

为前端 `apps/admin`（要有登录页，`tenon add frontend` 默认生成）生成桌面端，在项目根目录运行：

```bash
go tool tenon add client admin-desktop --kind electron --frontend admin --title "运营后台"
go tool tenon dev --client admin-desktop
```

- `add client` 生成 `clients/admin-desktop`，在 `apps/admin` 中加上授权页 `src/routes/oauth/consent.tsx`，并加入 Bun workspace。`--title` 是窗口标题和安装包的名称，默认是客户端的名字。
- 运行前在服务端登记这个客户端，ID 与目录名相同，见[登记客户端](native.md#registering)。
- `tenon dev` 启动程序和前端的开发服务器，再打开桌面端的窗口（第一次运行时下载 Electron）。前端代码的修改立刻生效；`src` 中主进程代码修改后重新运行。窗口最多等前端的开发服务器 60 秒。
- Linux 上报 “The SUID sandbox helper binary … is not configured correctly” 时，给报错中的 `chrome-sandbox` 设置权限：`sudo chown root:root <路径>/chrome-sandbox && sudo chmod 4755 <路径>/chrome-sandbox`。只在开发时出现。

<a id="generated-files"></a>
## 生成的文件

| 文件 | 内容 |
| --- | --- |
| `src/main.ts` | 主进程：创建宿主，每项桌面能力占一行，见[桌面能力](#capabilities) |
| `src/preload.ts` | 导入 `@go-tenon/electron/preload`，在页面中放好 `tenonHost` |
| `electron-builder.yml` | 打包的配置，见[打包](#packaging) |
| `assets/tray.png`、`build/icon.png` | 托盘图标和应用图标（至少 512×512），换成应用自己的 |
| `package.json` | 版本号，以及 `dev`、`bundle`、`package` 脚本 |

<a id="sign-in"></a>
## 登录

前端的登录页 `src/routes/login.tsx` 在桌面端中显示 `BrowserSignIn`，在网页中显示 `LoginForm`：

```tsx
const desktop = host()
return (
  <CenteredAuthLayout brand="运营后台" title="登录">
    {desktop ? <BrowserSignIn signIn={desktop.auth.signIn} redirect={redirect} /> : <LoginForm login={login} redirect={redirect} />}
  </CenteredAuthLayout>
)
```

`host()` 来自 `@go-tenon/host`，在浏览器中返回 `undefined`；`login` 是 `src/auth.ts` 中的登录函数，`redirect` 是登录前想去的页面。`desktop.auth.signIn` 打开系统浏览器，最多等 10 分钟，换到 token 后用 `safeStorage` 加密保存。

- 退出：`src/auth.ts` 的 `logout` 先调用退出接口，再调用 `host()?.auth.signOut()` 删除 token。
- 接口返回 `401` 时，桌面端删除 token，页面照常回到登录页。
- 浏览器显示“无法授权”：见[排查授权失败](native.md#troubleshooting)。
- Linux 上没有钥匙串服务（gnome-keyring、KWallet）时，token 用固定的密钥加密，日志中有 “no system keyring”。

<a id="capabilities"></a>
## 桌面能力

生成的 `src/main.ts` 中每项桌面能力占一行，不需要的删掉那一行。`autoUpdate` 来自 `@go-tenon/electron/update`，其余来自 `@go-tenon/electron/main`：

```ts
logs() // console 写入 main.log（超过 5 MB 换成 main.log.old），崩溃报告保存在本地，设置 crashReportURL 时上传

const host = createHost({ id: "admin-desktop", server, base: "/admin/", renderer, preload, dev, window })

windowState(host) // 记住窗口的位置、大小和是否最大化
standardMenu(host) // macOS 的标准菜单（⌘C、⌘V 需要它）和右键菜单
autoUpdate(host) // 自动更新，服务端返回 426 时立即更新
notifications(host, { appId }) // 页面调用 notify 弹出系统通知
badge(host) // 页面调用 badge 设置未读数（macOS 的 Dock、Linux 的启动器）
tray(host, { title: "运营后台", icon }) // 关闭窗口时隐藏到托盘，从托盘菜单“退出”才退出
autostart(host, { name: "admin-desktop" }) // 页面调用 autostart.set(true) 开机自启
const openLink = deepLinks(host, { scheme: "admin-desktop", base: "/admin/" }) // admin-desktop://open/admin/orders/123

if (singleInstance(host, openLink)) { // 再次打开（包括点击深度链接）时回到已有的窗口
  host.start()
}
```

- `createHost` 的 `id` 是客户端的名字，`server` 是服务器的地址（`tenon dev` 运行时取 `TENON_SERVER`），`dev` 是前端开发服务器的地址，生成的文件中都已写好。
- `appId` 要与 `electron-builder.yml` 中的 `appId` 相同：Windows 按它把通知关联到应用，不同时通知不显示。
- GNOME 要装 AppIndicator 扩展才显示托盘。

<a id="from-the-page"></a>
## 在页面中调用桌面能力

页面用 `@go-tenon/host` 的 `host()` 调用桌面能力。浏览器中 `host()` 返回 `undefined`，调用都写成可选的，同一份代码在网页和桌面端中都能运行：

```ts
import { host } from "@go-tenon/host"

host()?.badge?.(unread)
host()?.notify?.({ title: "新订单", body: "订单 123 已付款", path: "/admin/orders/123" })

const enabled = (await host()?.autostart?.get()) ?? false
await host()?.autostart?.set(!enabled)

host()?.on("update", (status) => {
  if (status.state === "ready") showRestartNotice(status.version) // 应用自己的提示，点击后调用 host()?.update?.install()
})
```

- `unread` 是未读数，通常来自[站内通知](../features/notifications.md)的推送。
- `notify` 的 `path` 是点击通知后跳转的页面：前端的 `src/router.tsx` 已经订阅了 `navigate` 事件，点击通知、打开深度链接时页面跳到对应的路径。
- 全部成员见[页面看到的 tenonHost](host.md#tenon-host)。

<a id="native-methods"></a>
## 用 `host.handle` 加原生方法

需要框架没有提供的原生能力时，在主进程中用 `host.handle` 注册方法，参数和返回值都是 JSON：

```ts
// src/main.ts，在 host.start() 之前
import { dialog } from "electron"

host.handle("files.pickFolder", async () => {
  const win = host.window()
  const result = win ? await dialog.showOpenDialog(win, { properties: ["openDirectory"] }) : undefined
  return result?.canceled ? null : (result?.filePaths[0] ?? null)
})
```

页面中用交叉类型补上应用自己的方法：

```ts
import { host, type TenonHost } from "@go-tenon/host"

type AppHost = TenonHost & { files?: { pickFolder(): Promise<string | null> } }

const folder = await (host() as AppHost | undefined)?.files?.pickFolder()
```

方法名中的点表示层级：`files.pickFolder` 在页面中是 `tenonHost.files.pickFolder`。只有桌面端自己的页面能调用。

<a id="packaging"></a>
## 打包

```bash
go tool tenon build --client admin-desktop
```

它安装依赖（`bun install --frozen-lockfile`），设置 `TENON_RENDER=csr` 把前端按 SPA 模式构建到 `clients/admin-desktop/dist/renderer`，再在客户端目录中运行 `bun run package`，由 [electron-builder](https://www.electron.build) 生成安装包。

- 安装包在 `clients/admin-desktop/release` 中。版本号是客户端 `package.json` 的 `version`，每次发布前改它。
- electron-builder 只为当前的系统打包，三个平台在 CI 中分别打包。
- `electron-builder.yml` 已经写好应用 ID、安全开关（`electronFuses`）、深度链接的协议、更新源（`publish`）和各平台的格式（dmg + zip、nsis、AppImage；zip 给 macOS 的自动更新用）。
- 提示 “default Electron icon is used”：`build/icon.png` 不存在。

<a id="ci"></a>
## 在 CI 中为三个平台打包

推送 `desktop-v*` 标签时，在 macOS、Windows、Linux 上分别打包，上传安装包：

```yaml
name: desktop
on:
  push:
    tags: ["desktop-v*"]
jobs:
  package:
    strategy:
      matrix:
        os: [macos-latest, windows-latest, ubuntu-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5 # go tool tenon 的版本由 go.mod 固定
        with:
          go-version-file: go.mod
      - uses: oven-sh/setup-bun@v2
      - run: go tool tenon build --client admin-desktop
        env:
          CSC_LINK: ${{ secrets.CSC_LINK }} # 签名证书
          CSC_KEY_PASSWORD: ${{ secrets.CSC_KEY_PASSWORD }}
      - uses: actions/upload-artifact@v4
        with:
          name: admin-desktop-${{ matrix.os }}
          path: |
            clients/admin-desktop/release/*.*
            !clients/admin-desktop/release/builder-*.yml
```

<a id="signing"></a>
## 签名

没有签名的安装包，macOS 拒绝打开（“已损坏，无法打开”），Windows 警告“未知发布者”。证书和密码放在 CI 的密钥中，以环境变量交给 electron-builder：

- **macOS**：Developer ID 证书，`CSC_LINK`（证书文件的 base64 或路径）、`CSC_KEY_PASSWORD`；公证用 `APPLE_ID`、`APPLE_APP_SPECIFIC_PASSWORD`、`APPLE_TEAM_ID`，并在 `mac` 中设置 `notarize: true`。
- **Windows**：代码签名证书同样用 `CSC_LINK`、`CSC_KEY_PASSWORD`；Azure Trusted Signing 写在 `win.azureSignOptions` 中。
- **Linux**：不需要。

详细配置见 electron-builder 的[代码签名](https://www.electron.build/code-signing)。

> [!IMPORTANT]
> macOS 上没有签名的应用不能自动更新：Squirrel.Mac 只安装签名相同的新版本。

<a id="auto-update"></a>
## 自动更新

`autoUpdate(host)` 用 electron-updater 在启动时和之后每 4 小时（`interval` 选项）检查一次，后台下载新版本，退出时安装，下载完成后向页面发出 `update` 事件。只有打包后的应用检查更新。

更新源是 `electron-builder.yml` 中的 `publish`，换成存放安装包的地址，例如对象存储的一个目录：

```yaml
publish:
  provider: generic
  url: https://example.com/downloads/admin-desktop
```

每次发布，把 `release` 中的安装包（`.exe`、`.dmg`、`.zip`、`.AppImage`）、`.blockmap` 和版本信息（`latest.yml`、`latest-mac.yml`、`latest-linux.yml`、`latest-linux-arm64.yml`）上传到这里。版本信息最后上传：electron-updater 先读它再下载安装包。

没有更新时，在 `main.log` 中查找 `update failed`：通常是 `publish.url` 中没有版本信息文件，或者其中的版本不比当前版本新。

<a id="forced-upgrade"></a>
## 强制升级

旧版本不能再用时，在服务端提高这个客户端的最低版本（要安装 [App 最低版本](../http/middleware.md#app-version)中间件，见[版本和强制升级](native.md#versions)）：

```yaml
http:
  app_version:
    minimum:
      admin-desktop: 1.3.0
```

低于 1.3.0 的桌面端的请求都返回 `426`。`autoUpdate` 收到后弹出对话框，用户点“立即更新”时下载、安装并重启，点“退出”时退出应用。前端不需要为此写代码。

> [!WARNING]
> 先发布新版本，再提高最低版本。顺序反了时，旧版本找不到可以更新的版本，只能提示“更新失败”，用户无法继续使用。
