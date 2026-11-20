# 前端包

改 `js` 中的前端包、playground 和脚手架模板中的界面时读这一页：各个包的职责和边界、检查的命令，以及颜色的约定。

`js` 是前端包的 Bun workspace。这些包不发布到 npm，随 Go 模块分发：应用中的 `tenon` 把框架版本中的 `js` 复制到项目的 `.tenon/js`，见[框架的前端包](../docs/installation/tooling.md#framework-packages)。

- `client` 是 `@go-tenon/client`，与界面无关的客户端运行时。它不依赖 DOM，也不依赖 Node：`tsconfig.json` 的 `lib` 只有 `es2022`，用到 `window`、`document`、`URL`、`node:*` 等时类型检查失败（测试文件同样检查）。所以网页、Electron 的主进程、React Native 都能用它，例如原生客户端登录用的 `createAuthorization`。
- `host` 是 `@go-tenon/host`，网页宿主（Electron 等）的约定在页面一侧的类型：`host()` 取得宿主放在全局的 `tenonHost`。它只有类型，不依赖任何包和 DOM，任何语言写的宿主都按它实现。
- `electron` 是 `@go-tenon/electron`，Electron 宿主：`main` 在主进程中实现宿主的约定（页面来自 `https://<id>.localhost`、转发 `/api/*`、登录、保存 token、安全默认值），`preload` 在页面中放好 `tenonHost`；桌面能力（`windowState`、`standardMenu`、`singleInstance`、`logs`、`notifications`、`badge`、`tray`、`autostart`、`deepLinks`，以及单独入口 `update` 的 `autoUpdate`）是彼此独立的函数，由应用的 `main.ts` 逐行组合。依赖 Electron 的代码只在 `main.ts`、`preload.ts` 中，其余模块用 Vitest 测试，CI 中不启动 Electron。`electron` 包在第一次运行时才下载 Electron 本体，安装依赖时不下载。
- `start` 是 `@go-tenon/start`，把 TanStack Start 接到 Go 的 `frontend` 包：Vite 插件（挂载路径、服务端渲染或页面外壳）、服务端渲染时调用 Go 接口的运行时，以及编译 SSR 可执行文件的 `tenon-start`。
- `ui` 是 `@go-tenon/ui`，基于 Mantine 的应用骨架和界面。`ui/src` 分成五层，上层只引用下层：`theme`（预设、主题、图标）→ `app`（应用骨架：装配、导航、插槽、多标签）→ `components`（页面中用的部件）→ `blocks`（布局、登录页的布局这类只描述样子的整块界面）和 `features`（框架功能的前端，逻辑和外观放在同一个目录）。`blocks` 和 `features` 互相不引用，把它们组合起来的是脚手架生成的项目。新文件按它依赖什么放进对应的层，`uilayertest` 检查引用方向。React、TanStack Router、TanStack Query 和 Mantine 是 peer 依赖。
- `playground` 是 ui 的演示，用 `bun run playground` 启动。ui 不写测试，界面的改动在 playground 中看效果。

在 `js` 目录中运行：

```bash
bun install --frozen-lockfile
bun run check       # Biome
bun run typecheck   # 每个包的 tsc --noEmit
bun run test        # Vitest：client、start 和 electron 的测试
```

- 每个有 `package.json` 的目录都会被复制到项目中安装，所以开发和测试用到的包（React、Vite、Vitest 等）都写在 `js/package.json` 的 `devDependencies` 中。测试文件（`*.test.ts`）不复制，可以用这些包。
- 在生成的项目中试用改动：用 `tenon init --tenon <框架目录>` 生成项目，`tenon dev` 每次启动时复制本地的 `js`。

## 颜色

`js` 中的包、playground、脚手架的模板和文档中的示例，颜色都从 Mantine 的主题中取。主题中的彩色是 Mantine [颜色生成器](https://mantine.dev/colors-generator)的 26 种预设（`js/ui/src/theme/theme.ts` 的 `primaryColors`），色板由 `@mantine/colors-generator` 生成，亮色和暗色模式下的取值由 Mantine 统一给出，应用换品牌色时整套界面一起变：

- 品牌色是 `brand`，组件不写 `color` 时用的就是它。它由 `createUiTheme` 的 `primary` 设置，写预设的名称（例如 `pale-indigo`）或颜色值。
- 其他彩色写预设的名称：组件的属性写 `color="deep-orange"`、`c="red"`、`bg="green.5"`，CSS 写 `var(--mantine-color-deep-orange-6)`。浅色底和上面的文字用 `-light`、`-light-color` 变体，它们在暗色模式下自动换成合适的值，不用另写暗色的规则。半透明用 `color-mix(in srgb, var(--mantine-color-violet-7) 50%, transparent)`。
- Mantine 自带的 `teal`、`orange`、`blue`、`cyan`、`grape`、`indigo`、`lime` 不在预设中，不使用；`red`、`green`、`yellow`、`pink`、`violet` 与预设同名，用的是预设的色板。彩色不写十六进制或 `rgb()` 的值，写死的值不随品牌色和暗色模式变化。
- 状态的颜色固定，同一种状态在各处用同一种颜色：成功、正常用 `green`；进行中用 `bright-blue`；暂停、等待处理用 `yellow`；需要注意用 `deep-orange`；失败、危险用 `red`；没有状态、已停用用 `gray`。
- 中性色（文字、边框、背景）不在预设中，用主题的 `gray`、`dark` 色板、`c="dimmed"` 和 `--tn-*` 变量（`--tn-text`、`--tn-muted`、`--tn-border` 等）。中性色的值只写在定义它们的地方：`theme.ts` 的色板，`styles.css` 中定义变量的规则；其他规则和组件用这些变量或 `var(--mantine-color-white)`、`var(--mantine-color-black)`，半透明用 `color-mix()`。阴影和遮罩（`mask-image`）可以直接写黑白的值。

## 服务端渲染进程的约定

Go 的 `frontend` 包托管 SSR 进程，`@go-tenon/start` 是这些约定在 Bun 一侧的实现。两边的常量各写一份（`frontend/frontend.go`、`frontend/render.go` 与 `js/start/src/headers.ts`、`js/start/src/print.tsx`、`js/start/src/vite.js`），改其中一边时同时改另一边。应用怎样使用见[托管前端与服务端渲染](../docs/frontend/hosting.md)。

| 约定 | 说明 |
| --- | --- |
| 环境变量 `TENON_FRONTEND` | 这个进程渲染的前端的名称。可执行文件中有全部服务端渲染的前端，按它加载其中一个 |
| 环境变量 `SSR_SOCKET` | 进程监听的抽象 socket，形如 `@tenon-ssr-<名称>-<随机串>`。环境变量不能含 NUL，Bun 中把开头的 `@` 换成 `\0`（`socketPath`） |
| 环境变量 `API_SOCKET` | 调用 Go 接口的抽象 socket，形如 `@tenon-api-<随机串>`；`Options.API` 为 nil 时不传 |
| 环境变量 `API_ORIGIN` | 开发时没有内部 socket，经这个地址调用 Go，由 `tenon dev` 设置 |
| 请求头 `X-CSP-Nonce` | 本次请求的 nonce，写到全部内联脚本上。Go 总是覆盖它，客户端发来的值不生效 |
| 占位 nonce `__tenon_csp_nonce__` | 构建时写在页面外壳和预渲染页面中，Go 返回页面时换成本次请求的 |
| 请求头 `X-Request-ID`、`traceparent`、`tracestate`、`Cookie`、`Accept-Language` | 调用接口时原样带上（`forwardHeaders`） |
| 响应头 `X-Tenon-Route` | 可选，匹配到的前端路由模板。Go 用它替换 chi 的路由模板来命名 span，随后从响应中删掉 |
| `POST /__tenon/print/{name}` | 可选，用打印模板渲染请求体中的 JSON，返回完整的 HTML，没有这个模板时返回 404。对外的请求访问 `/__tenon/` 下的地址时 Go 返回 404；`Render` 最多读取 64 MB |
| SIGTERM | 停止接收请求并退出。Go 发送它之前已经排空了对外的请求 |

- 页面请求的路径带着前端的挂载路径，例如 `/shop/products/1`。静态文件和预渲染的页面由 Go 返回，不转给 SSR 进程。
- 可执行文件由 `apps/dist/ssr` 流式写进 memfd 后执行，不落盘，也不在 Go 的内存中另存一份。进程设置 `Pdeathsig`，监控它的 goroutine 锁住线程，Go 被强制结束时 SSR 进程随之结束。
- memfd 和抽象 socket 都是 Linux 特有的，所以其他系统上 `Run` 返回 `ErrUnsupported`。
- 抽象 socket 没有文件权限，同一网络命名空间中的进程都能连接，所以内部入口按 `SO_PEERCRED` 只接受与 Go 进程同一用户的连接。
- 重启的退避：第一次 100 毫秒，之后翻倍，最多 5 秒；进程连续运行超过 10 秒后从头计算（`frontend/process.go` 中的包级变量，测试中改小）。
