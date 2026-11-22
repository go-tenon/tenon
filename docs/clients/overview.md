# 客户端

网页以外的客户端（桌面端、App）的种类、目录和命令。所有客户端调用同一组 HTTP 接口，遵守[客户端协议](../http/protocol.md)。

包：`@go-tenon/host`、`@go-tenon/electron`。

<a id="kinds"></a>
## 三类客户端

| 类别 | 界面 | 例子 |
| --- | --- | --- |
| 网页 | `apps` 中的前端，Go 托管 | 官网、后台 |
| 网页宿主 | 同一份 `apps` 中的前端，运行在原生窗口中；原生能力经 [`tenonHost`](host.md) 调用 | Electron、Tauri、Wails |
| 原生界面 | 用平台的技术重写，按[原生客户端](native.md)登录、上报版本 | SwiftUI、Jetpack Compose、React Native、Flutter |

共用的部分：[客户端协议](../http/protocol.md)（所有客户端）→ 各语言的客户端库（例如 `@go-tenon/client`）→ [网页宿主的约定](host.md)（只对网页宿主）→ 各宿主的实现（例如 `@go-tenon/electron`）。

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

规划中的种类不能用 `tenon add client` 生成。需要时用 [OpenAPI](../http/openapi.md) 生成对应语言的客户端，登录和版本按[原生客户端](native.md)实现。

<a id="layout"></a>
## 项目中的目录

每个客户端是 `clients` 中的一个目录，目录名是客户端的名字，其中的 `tenon.json` 写明种类和显示的前端：

```
myapp/
├── apps/admin/              # 网页前端
├── packages/client/         # 由接口生成的 TypeScript 客户端，apps 和网页宿主共用
├── clients/
│   └── admin-desktop/
│       └── tenon.json       # { "kind": "electron", "frontend": "admin" }
└── tenon/js/                # 框架的前端包，由 tenon 同步，随仓库提交
```

客户端的名字以小写字母开头，只含小写字母、数字和 `-`。它同时是授权服务器中的 client ID（见[登记客户端](native.md#registering)）、`X-App-Version` 中的平台名、网页宿主的页面地址 `https://<名字>.localhost` 和深度链接的协议 `<名字>://`，发布之后不要再改。

<a id="commands"></a>
## 命令

| 命令 | 作用 |
| --- | --- |
| `go tool tenon add client <名字> --kind <种类> --frontend <前端>` | 生成 `clients/<名字>`，加入 Bun workspace；网页宿主在前端中加上授权页 |
| `go tool tenon dev --client <名字>` | 运行应用和前端的开发服务器，同时运行这个客户端；可以写多个 `--client` |
| `go tool tenon build --client <名字>` | 打包这个客户端；不带 `--client` 时只构建服务端 |

<a id="choosing"></a>
## 怎样选择

- 已有网页后台，要一个能弹系统通知、常驻托盘的桌面版：用网页宿主，界面不改，见 [Electron 桌面端](electron.md)。
- 要原生的外观、更小的安装包或上架应用商店：用原生界面，见[原生客户端](native.md)。
- 一个应用可以同时有多个客户端，它们登录同一个账号，调用同一组接口。
