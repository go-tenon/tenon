# 打包、签名与更新

桌面端写好以后，要变成用户能安装的安装包：Windows 的安装程序、macOS 的 dmg、Linux 的 AppImage。这一页介绍怎样打包、在 CI 中为三个平台打包、为安装包签名，以及发布新版本后怎样让用户自动更新，必要时要求旧版本先升级。

阅读前需要了解：[Electron 桌面端](electron.md)。

- [打包](#packaging)
- [在 CI 中为三个平台打包](#ci)
- [签名](#signing)
- [自动更新](#auto-update)
- [强制升级](#forced-upgrade)
- [常见问题](#troubleshooting)

<a id="packaging"></a>
## 打包

在项目根目录运行：

```bash
go tool tenon build --client admin-desktop
```

它依次做三件事：

1. 安装依赖（`bun install --frozen-lockfile`）。
2. 构建前端：设置环境变量 `TENON_RENDER=csr` 运行前端的 `bun run build`，不论它在网页上是否在服务端渲染，都按 SPA 模式构建，再复制到 `clients/admin-desktop/dist/renderer`。
3. 在客户端目录中运行 `bun run package`：把主进程和 preload 打包到 `dist`，再由 [electron-builder](https://www.electron.build) 生成安装包。

安装包在 `clients/admin-desktop/release` 中，例如 Linux 上的 `运营后台-0.1.0-arm64.AppImage`。版本号来自客户端 `package.json` 的 `version`，每次发布前改它。

electron-builder 只能为当前的系统打包：macOS 的安装包只能在 macOS 上生成和签名，Windows 的最好在 Windows 上生成。所以通常在 CI 中用三台机器分别打包。

打包的配置在 `electron-builder.yml` 中，生成时已经写好了：

- `appId`、`productName`、`executableName`：应用的 ID、显示的名称和可执行文件的名称；
- `electronFuses`：Electron 的安全开关，例如不能把程序当作 Node 运行、只从安装包加载代码并校验它的完整性；
- `protocols`：深度链接的协议；
- `publish`：自动更新的来源，见[自动更新](#auto-update)；
- `mac`、`win`、`linux`：各平台的安装包格式，macOS 另外打一个 zip，自动更新要用它。

<a id="ci"></a>
## 在 CI 中为三个平台打包

下面的 GitHub Actions 工作流在推送 `desktop-v*` 标签时，分别在 macOS、Windows、Linux 上打包，把安装包上传为构建产物：

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
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - uses: oven-sh/setup-bun@v2
      - run: go tool tenon build --client admin-desktop
        env:
          CSC_LINK: ${{ secrets.CSC_LINK }} # 签名证书，见下一节
          CSC_KEY_PASSWORD: ${{ secrets.CSC_KEY_PASSWORD }}
      - uses: actions/upload-artifact@v4
        with:
          name: admin-desktop-${{ matrix.os }}
          path: |
            clients/admin-desktop/release/*.*
            !clients/admin-desktop/release/builder-*.yml
```

`go tool tenon` 由项目的 `go.mod` 固定版本，所以 CI 中先安装 Go，再安装 Bun。三个任务互不依赖，一个平台失败不影响另外两个。

<a id="signing"></a>
## 签名

没有签名的安装包，macOS 会拒绝打开，Windows 会弹出“未知发布者”的警告。正式发布前为它们签名：

| 平台 | 需要什么 | 怎样交给 electron-builder |
| --- | --- | --- |
| macOS | Apple Developer 账号的 Developer ID 证书，并经过 Apple 公证 | 环境变量 `CSC_LINK`（证书文件的 base64 或路径）、`CSC_KEY_PASSWORD`；公证用 `APPLE_ID`、`APPLE_APP_SPECIFIC_PASSWORD`、`APPLE_TEAM_ID`，并在 `mac` 中设置 `notarize: true` |
| Windows | 代码签名证书，或者 Azure Trusted Signing | 证书文件同样用 `CSC_LINK`、`CSC_KEY_PASSWORD`；Azure Trusted Signing 写在 `win.azureSignOptions` 中 |
| Linux | 不需要 | — |

证书和密码放在 CI 的密钥中，不要提交到仓库。具体的配置见 electron-builder 的 [代码签名](https://www.electron.build/code-signing) 一页。

> [!IMPORTANT]
> macOS 上没有签名的应用不能自动更新：Squirrel.Mac 只安装签名相同的新版本。

<a id="auto-update"></a>
## 自动更新

`main.ts` 中的 `autoUpdate(host)` 用 [electron-updater](https://www.electron.build/auto-update) 自动更新：启动时和之后每 4 小时检查一次，有新版本时在后台下载，下次退出时安装。只有打包后的应用会检查，开发时什么也不做。

更新的来源是 `electron-builder.yml` 中的 `publish`：

```yaml
publish:
  provider: generic
  url: https://example.com/downloads/admin-desktop
```

把这个地址换成你放安装包的地方，例如对象存储的一个目录。每次发布，把 `release` 中的这些文件上传到这里：

- 安装包：`.exe`、`.dmg`、`.zip`、`.AppImage`；
- 增量更新用的 `.blockmap`；
- 版本信息：`latest.yml`（Windows）、`latest-mac.yml`、`latest-linux.yml`（arm64 是 `latest-linux-arm64.yml`）。

electron-updater 先读版本信息，发现版本比自己新时下载对应的安装包。所以先上传安装包，最后再上传版本信息，用户不会读到指向还没上传完的文件的版本信息。

下载完成后，桌面端发出 `update` 事件，页面可以提示用户“重启以完成更新”，点击后调用 `host()?.update?.install()`，见[在页面中调用桌面能力](electron.md#from-the-page)。

<a id="forced-upgrade"></a>
## 强制升级

改了接口、旧版本不能再用时，在服务端提高这个客户端的最低版本：

```yaml
http:
  app_version:
    minimum:
      admin-desktop: 1.3.0
```

平台名就是客户端的名字，桌面端在每个请求的 `X-App-Version` 中带上它和自己的版本，见[原生客户端：版本和强制升级](native.md#versions)。服务端要安装 [App 最低版本](../http/middleware.md#app-version)中间件。

低于 1.3.0 的桌面端发出的请求都返回 `426`。桌面端收到后弹出对话框：“当前版本过低，更新到新版本后才能继续使用”。用户点“立即更新”后，下载新版本、安装并重启；点“退出”则退出应用。前端不需要为这件事写任何代码。

> [!WARNING]
> 先发布新版本，再提高最低版本。顺序反了的话，旧版本收到 `426` 后找不到可以更新的版本，只能提示“更新失败”，用户无法继续使用。

<a id="troubleshooting"></a>
## 常见问题

**打包时提示 “default Electron icon is used”**

`build/icon.png` 不存在。把你的应用图标（至少 512×512 的 PNG）放在这里，electron-builder 会生成各平台需要的格式。

**macOS 上打开安装好的应用，提示“已损坏，无法打开”**

安装包没有签名，或者没有经过公证。按[签名](#signing)配置证书和公证。

**自动更新一直没有动静**

在日志 `main.log` 中查找 `update failed`：常见的原因是 `publish.url` 中没有版本信息文件，或者版本信息中的版本不比当前版本新。开发时（没有打包）不会检查更新。
