# 客户端

除了网页，你的应用还可以有桌面端和 App。这一组介绍网页以外的客户端：有哪几类、各放在项目的哪里、怎样生成和运行，以及它们共同遵守的约定。它们调用的都是同一组 [HTTP 接口](../http/README.md)，服务端不需要为每种客户端另写一套。

阅读前需要了解：[前端](../frontend/README.md)；[客户端协议](../http/protocol.md)。

- [三类客户端](#kinds)
- [支持的种类](#supported-kinds)
- [项目中的目录](#layout)
- [命令](#commands)
- [怎样选择](#choosing)
- [这一组的页面](#pages)

<a id="kinds"></a>
## 三类客户端

| 类别 | 界面 | 例子 |
| --- | --- | --- |
| 网页 | `apps` 中的前端，Go 托管 | 官网、后台、控制台 |
| 网页宿主 | 同一份 `apps` 中的前端，运行在原生窗口里 | Electron、Tauri、Wails |
| 原生界面 | 界面用平台的技术重写 | SwiftUI、WinUI 3、Jetpack Compose、React Native、Flutter、mygo |

**网页宿主**是一个装着网页的原生程序：窗口里运行的就是你在 `apps` 中写的前端，宿主负责网页做不到的事，例如系统通知、托盘、开机自启。同一份前端代码在浏览器和宿主中都能运行，在宿主中多出来的能力通过 [`tenonHost`](host.md) 调用。

**原生界面**的客户端不复用前端代码，界面要重写一遍，换来的是平台原生的外观和性能。它们和网页宿主一样调用你的 HTTP 接口，按[原生客户端](native.md)一页登录、上报版本。

三类客户端共用的部分分成几层，越往下越通用：

1. **客户端协议**：所有客户端都遵守，见[客户端协议](../http/protocol.md)。需要服务端配合的事情（登录方式、版本上报、实时频道的认证）都写在这里，任何语言照着就能实现。
2. **各语言的客户端库**：由接口生成的客户端，加上与界面无关的运行时，例如 TypeScript 的 `@go-tenon/client`。
3. **宿主约定**：只对网页宿主，见[网页宿主的约定](host.md)。
4. **各宿主的实现**：例如 Electron 的 `@go-tenon/electron`。

<a id="supported-kinds"></a>
## 支持的种类

| `--kind` | 类别 | 平台 | 状态 |
| --- | --- | --- | --- |
| `electron` | 网页宿主 | Windows、macOS、Linux | 已支持，见 [Electron 桌面端](electron.md) |
| `tauri` | 网页宿主 | 桌面三平台、iOS、Android | 规划中 |
| `wails` | 网页宿主 | 桌面三平台 | 规划中 |
| `mygo` | 原生界面（Go） | 桌面三平台 | 规划中 |
| `winui` | 原生界面（C#） | Windows | 规划中 |
| `swiftui` | 原生界面（Swift） | iOS、macOS | 规划中 |
| `compose` | 原生界面（Kotlin） | Android | 规划中 |
| `react-native` | 原生界面（TypeScript） | iOS、Android | 规划中 |
| `flutter` | 原生界面（Dart） | iOS、Android、桌面 | 规划中 |

规划中的种类目前不能用 `tenon add client` 生成。你可以先自己按[原生客户端](native.md)写：接口用 [OpenAPI](../http/openapi.md) 生成对应语言的客户端，登录和版本按那一页实现。

<a id="layout"></a>
## 项目中的目录

每个客户端是 `clients` 中的一个目录，目录名就是客户端的名字。下面是所有种类都用上时的样子：

```
myapp/
├── apps/                    # 网页前端，Go 托管
│   └── admin/
├── packages/
│   └── client/              # 由接口生成的 TypeScript 客户端：apps、网页宿主、React Native 共用
├── clients/                 # 网页以外的客户端
│   ├── admin-desktop/       # --kind electron --frontend admin
│   ├── admin-tauri/         # --kind tauri --frontend admin
│   ├── apple/               # --kind swiftui
│   ├── android/             # --kind compose
│   └── mobile/              # --kind react-native
└── .tenon/                  # 框架随版本分发的库，由 tenon 复制，不提交
    └── js/                  # client、host、electron 等前端包
```

每个客户端目录中都有一个 `tenon.json`，写明种类，网页宿主还写明显示哪个前端：

```json
{
  "kind": "electron",
  "frontend": "admin"
}
```

`tenon` 的命令按它识别客户端，不管客户端用什么工具链。

客户端的名字有三个用途，所以要选一个稳定的名字：

- 授权服务器中登记的 client ID，见[原生客户端](native.md#registering)；
- 请求头 `X-App-Version` 中的平台名，例如 `admin-desktop/1.2.0`，服务端按它为每个客户端设置最低版本；
- 网页宿主中页面的地址 `https://<名字>.localhost`，以及深度链接的协议 `<名字>://`。

<a id="commands"></a>
## 命令

| 命令 | 作用 |
| --- | --- |
| `go tool tenon add client <名字> --kind <种类> --frontend <前端>` | 生成 `clients/<名字>`，加入 Bun workspace；网页宿主在前端中加上授权页 |
| `go tool tenon dev --client <名字>` | 照常运行应用和前端的开发服务器，同时运行这个客户端，可以写多个 `--client` |
| `go tool tenon build --client <名字>` | 打包这个客户端：网页宿主先按 SPA 模式构建前端，再生成安装包 |

`tenon build` 不带 `--client` 时只构建服务端的程序，客户端单独打包和发布。

<a id="choosing"></a>
## 怎样选择

- 已经有网页后台，想要一个能放在任务栏里、能弹系统通知的桌面版：用网页宿主，界面一行不改，见 [Electron 桌面端](electron.md)。
- 想要原生的外观、更小的安装包，或者要上架应用商店：用原生界面，见[原生客户端](native.md)。
- 同一个应用可以同时有好几个客户端，例如网页后台、Electron 桌面端和一个 iOS App，它们登录同一个账号，调用同一组接口。

<a id="pages"></a>
## 这一组的页面

按阅读顺序：

- [原生客户端](native.md)：所有安装在设备上的客户端共用的部分：登记、在系统浏览器中登录、保存 token、版本、通知和深度链接
- [Electron 桌面端](electron.md)：生成、登录、托盘和通知等桌面能力、自己加原生方法
- [打包、签名与更新](electron-release.md)：生成安装包、在 CI 中为三个平台打包、签名、自动更新和强制升级
- [网页宿主的约定](host.md)：宿主要做到的事和 `tenonHost` 接口，写新的宿主或排查问题时阅读
