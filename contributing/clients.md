# 客户端

网页以外的客户端（桌面端、App）的总览见[客户端](../docs/clients/README.md)。框架为它们提供的代码按下面的位置放，目前只有 Electron 一种；以后新增种类时按这里的位置和步骤，不另起新的位置：

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

1. 在[客户端](../docs/clients/README.md#supported-kinds)的种类表中确认它的类别（网页宿主或原生界面）、平台和库的位置；
2. 网页宿主按[网页宿主的约定](../docs/clients/host.md)实现，原生界面按[原生客户端](../docs/clients/native.md)实现；需要新语言时先加 `clientgen` 的生成器；
3. 在 `project.Kinds`、`dev/clients.go` 和 `scaffold/template/client/<种类>` 中加上它；
4. 工具链是新的：在仓库顶层加工具链的目录，复制到项目的 `.tenon/<工具链>`，CI 加一个任务；
5. 写 `docs/clients/<种类>.md`，把种类表中的状态改为“已支持”；
6. 需要服务端配合的事情先写进[客户端协议](../docs/http/protocol.md)和[原生客户端](../docs/clients/native.md)，不只写在这个种类的实现中。
