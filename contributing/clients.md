# 客户端

网页以外的客户端（桌面端、App）的总览见[客户端](../docs/clients/overview.md)。框架为它们提供的代码按下面的位置放，目前只有 Electron 一种；以后新增种类时按这里的位置和步骤，不另起新的位置：

```
tenon/
├── go.mod                                  # 主模块：服务端各包和 cmd/tenon，不引入任何客户端的依赖
├── clientgen/                              # 生成器：公共部分 + 各语言（ts、go，以后 swift、kotlin、csharp、dart）
├── cmd/tenon/internal/
│   ├── project/                            # 发现 clients/<名>/tenon.json，Kinds 是支持的种类
│   ├── dev/clients.go                      # 每个种类：需要的框架包、开发时怎样运行、怎样打包
│   └── scaffold/template/client/<种类>/     # 每个种类的模板
├── js/                                     # Bun workspace
│   ├── client  host  electron              # 客户端运行时、宿主约定、Electron 宿主
│   └── tauri  wails  react-native          # 以后：宿主页面一侧的 JS、React Native
├── go/                                     # 以后：独立的 Go module（wails、mygo），不进主 go.mod
├── rust/  swift/  kotlin/  dotnet/  dart/  # 以后：按工具链分的库
```

新增一种客户端时：

1. 在[客户端](../docs/clients/overview.md#supported-kinds)的种类表中确认它的类别（网页宿主或原生界面）、平台和库的位置；
2. 网页宿主按[实现一个新的宿主](#new-host)实现，原生界面按[原生客户端](../docs/clients/native.md)实现；需要新语言时先加 `clientgen` 的生成器；
3. 在 `project.Kinds`、`dev/clients.go` 和 `scaffold/template/client/<种类>` 中加上它；
4. 工具链是新的：在仓库顶层加工具链的目录，复制到项目的 `.tenon/<工具链>`，CI 加一个任务；
5. 写 `docs/clients/<种类>.md`，把种类表中的状态改为“已支持”；
6. 需要服务端配合的事情先写进[客户端协议](../docs/http/protocol.md)和[原生客户端](../docs/clients/native.md)，不只写在这个种类的实现中。

<a id="host-design"></a>
## 网页宿主的约定为什么这样定

[网页宿主的约定](../docs/clients/host.md)中的规则出于下面的考虑，新的宿主照样遵守：

- **页面从本地加载**：打开服务器上的网页时，桌面端只是套着窗口的浏览器，离线打不开，远程内容运行在有原生能力的窗口中，用的是浏览器的 cookie 会话。从本地加载时，界面的版本和安装包一致，原生能力只给自己的页面。
- **接口由宿主转发**：页面直接跨域请求服务器时，要开放跨域，token 也要交给页面。由宿主转发时，接口对页面是同源的，前端代码与网页上相同；token 只在宿主中，页面中被注入的脚本也拿不到。
- **页面来自 `https://<名字>.localhost`**：前端路由只把 http(s) 的同源地址当作站内跳转（例如没有登录时跳到登录页），自定义协议（`app://`）上的这类跳转会被当作不安全的地址拒绝。`.localhost` 是保留的域名，不会解析到外部，宿主拦截它的请求，不访问网络。
- **方法都是异步的，参数是 JSON**：宿主可以用任何语言实现（Electron 用 TypeScript，Tauri 用 Rust，Wails 用 Go），页面和宿主之间只能传可以序列化的数据。

<a id="new-host"></a>
## 实现一个新的宿主

1. 按[宿主要做到的事](../docs/clients/host.md#host-duties)实现本地加载、转发、登录、保存 token 和实时频道的认证。登录中与界面无关的部分（PKCE、检查回调、换取 token）用 `@go-tenon/client` 的 `createAuthorization`、`readCallback`、`exchangeCode`，或者用宿主的语言按[客户端协议](../docs/http/protocol.md#native-clients)实现。
2. 在页面的全局放一个 `tenonHost`，形状与 `@go-tenon/host` 的 `TenonHost` 类型相同，`kind` 是新宿主的种类。
3. 对照 Electron 的实现检查：`js/electron/src` 中的 `routes.ts`、`forward.ts`、`csp.ts` 是与 Electron 无关的规则，可以照着移植，它们的测试写明了每条规则的边界。
