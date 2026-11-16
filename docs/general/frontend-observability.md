# 前端观测

[可观测性](telemetry.md)一章讲的是服务端：链路、指标和日志用 OTLP 推送到 OpenObserve 这样的接收端。用户浏览器中发生的事同样需要知道：页面报错、接口失败、哪个页面慢。浏览器端用观测平台自己的 SDK 收集，框架负责三件 SDK 自己做不到的事：

- 页面出错时，路由的错误组件接住了错误，它不会冒泡到 `window`，SDK 捕获不到。`createApp` 的 `onError` 把这些错误交给你；
- 浏览器的请求带上 `traceparent`，服务端的链路接在浏览器之后，从一个前端错误可以跳到它引起的后端链路；
- 构建时生成 source map，但不放进 `dist`：它们等于源码，不能随应用一起公开，由 CI 上传给观测平台，用来还原压缩后的堆栈。

两个术语：

- **`traceparent`**：一个请求头，写着这个请求属于哪条链路。服务端看到它，就把自己的链路接在后面。
- **source map**：前端代码在构建时被压缩，报错的堆栈中只有 `index-3f2a.js:1:20345` 这样的位置。source map 记录压缩后的位置对应源码的哪一行，观测平台用它还原出可读的堆栈。

下面以 [OpenObserve](https://openobserve.ai/) 的浏览器 SDK 为例。其他平台（例如 Sentry）的做法相同，换成它的 SDK 即可。

阅读前需要了解：[可观测性](telemetry.md)；[应用骨架](../frontend/app-shell.md)中的 `createApp`。

- [安装 SDK](#installing)
- [页面中的错误](#route-errors)
- [用户、租户和页面](#context)
- [前后端的链路](#tracing)
- [Source map](#source-maps)
- [隐私与内容安全策略](#privacy)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="installing"></a>
## 安装 SDK

在前端目录中安装 OpenObserve 的浏览器 SDK：`browser-rum` 收集页面、请求、资源加载、性能和会话回放，`browser-logs` 收集日志和错误。

```bash
bun add @openobserve/browser-rum@0.4.3 @openobserve/browser-logs@0.4.3
```

新建 `src/observe.ts`，在 `main.tsx` 中最先导入它：

```ts
import { openobserveLogs } from "@openobserve/browser-logs"
import { openobserveRum } from "@openobserve/browser-rum"

const clientToken = import.meta.env.VITE_O2_CLIENT_TOKEN
const site = import.meta.env.VITE_O2_SITE // 例如 o2.example.com，不带协议

// 没有设置时（本地开发）不启动 SDK。
export const observing = Boolean(clientToken && site)

if (clientToken && site) {
  const common = {
    clientToken,
    site,
    organizationIdentifier: "default",
    service: "admin", // 前端的名称，与上传 source map 时相同
    env: import.meta.env.MODE, // production、staging……与上传 source map 时相同
    // tenonVersion 注入的构建版本；开发模式下没有
    version: import.meta.env.TENON_BUILD_VERSION ?? "dev",
  }
  openobserveRum.init({
    ...common,
    applicationId: "admin",
    trackResources: true,
    trackLongTasks: true,
    // 后台系统的页面中有业务数据：点击记录元素的文字，默认不开
    trackUserInteractions: false,
    defaultPrivacyLevel: "mask-user-input",
    allowedTracingUrls: [(url: string) => url.startsWith(`${location.origin}/api/`)],
  })
  openobserveLogs.init({ ...common, forwardErrorsToLogs: true })
}
```

环境变量写在前端目录的 `.env.production` 中（`VITE_` 开头的变量在构建时写进页面）。它们和 `MODE` 的类型在 `src/env.d.ts` 中声明：

```ts
/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_O2_CLIENT_TOKEN?: string
  readonly VITE_O2_SITE?: string
}
```

`clientToken` 是 OpenObserve 中为浏览器创建的令牌，只能写入 RUM 数据，放在页面中是预期的。OpenObserve 用 http 访问时（例如内网测试环境），`common` 中加上 `insecureHTTP: true`。

SDK 自动捕获未捕获的异常、未处理的 Promise 拒绝、`console.error`、失败的请求（包括 4xx 和 5xx）和资源加载失败，不需要自己监听 `window` 的 `error`，否则同一个错误会报告两次。

<a id="route-errors"></a>
## 页面中的错误

页面渲染或 loader 中抛出的错误被路由的错误组件接住，显示错误页，SDK 看不到它们。把它们交给 `createApp` 的 `onError`：

```ts
import { openobserveLogs } from "@openobserve/browser-logs"

export const app = createApp({
  // ……
  onError: (error, report) => {
    // report：routeId（例如 /_app/articles/$id）、pathname、error（分类，接口错误带 requestId）
    openobserveLogs.logger.error("route error", { route: report.routeId, path: report.pathname, request_id: report.error.requestId }, error as Error)
  },
})
```

- 默认的错误组件（外观层的 `RouteError`）显示之前先报告。同一个错误只报告一次，`onError` 自己出错不影响错误页。
- 只报告需要处理的错误（`shouldReport`）：代码中的错误、代码块加载失败和 5xx。缺少权限、请求被取消和其他接口错误（未登录、校验失败、不存在等）是预期的，不报告。
- 路由自己写了 `errorComponent` 时，在其中调用 `useReportError(error)`。
- `requestId` 是服务端为这个请求生成的 ID，在服务端的日志和链路中可以按它查到这个请求，见[日志](logging.md)。

<a id="context"></a>
## 用户、租户和页面

登录、退出、切换租户时更新 SDK 中的用户；页面切换后用路由模板命名页面，`/articles/1` 和 `/articles/2` 归为同一个页面 `/_app/articles/$id`：

```ts
import { openobserveRum } from "@openobserve/browser-rum"
import { app } from "./app"
import { observing } from "./observe"

if (observing) {
  const syncUser = () => {
    const id = app.session.identity()
    if (id) {
      openobserveRum.setUser({ id: id.account })
      openobserveRum.setGlobalContextProperty("tenant", id.tenant ?? "")
    } else {
      openobserveRum.clearUser()
    }
  }
  app.session.subscribe(syncUser)
  syncUser()
  app.router.subscribe("onResolved", () => {
    const match = app.router.state.matches.at(-1)
    if (match) openobserveRum.setViewName(match.routeId)
  })
}
```

用户只记录账号 ID。姓名、邮箱等个人信息不写进观测平台，需要时按 ID 到应用中查。

<a id="tracing"></a>
## 前后端的链路

`allowedTracingUrls` 中的请求带上 W3C 的 `traceparent` 请求头，框架的 HTTP 服务[接在调用方的链路之后](telemetry.md#http)。后端把链路推送到同一个 OpenObserve（[配置](telemetry.md#setup)中的 `telemetry.endpoint`），错误详情中的 Trace Correlation 就能跳到后端的链路：慢在哪个查询、哪个下游服务出了错。

前端和 API 同源（`/api`）时不涉及跨域。API 在其他域名时，[CORS](../http/optional-middleware.md#cors) 要允许 `traceparent` 和 `tracestate` 请求头，否则浏览器在预检时去掉它们。

<a id="source-maps"></a>
## Source map

生产构建的脚本是压缩过的，错误的堆栈指向 `index-BjXc12aB.js:1:23456`，没法看。上传 source map 后，OpenObserve 把堆栈还原为源码中的位置（企业版的功能）。

在 `vite.config.ts` 的 `tenonVersion` 中打开：

```ts
tenonVersion({ sourcemaps: "sourcemaps" })
```

构建时生成 source map，但脚本中不写 `sourceMappingURL`，构建结束后把 `.map` 从 `dist` 移到前端目录下的 `sourcemaps/`：它们不嵌入二进制，不会被公开。即使 `.map` 留在了 `dist` 中，`httpx.SPA` 也不提供它们。把 `sourcemaps/` 加入 `.gitignore`。

版本号是 `tenonVersion` 的构建版本，页面中是 `import.meta.env.TENON_BUILD_VERSION`（交给 SDK 的 `version`），构建结束后也写在 `dist/version.json` 中。CI 在构建之后上传，`service`、`env`、`version` 要与 SDK 中的完全一致：

```bash
cd apps/admin
version=$(jq -r .version dist/version.json)
(cd sourcemaps && zip -qr ../sourcemaps.zip .)
curl -fsS -X POST "https://o2.example.com/api/default/sourcemaps" \
  -H "Authorization: Basic $O2_SOURCEMAP_TOKEN" \
  -F "file=@sourcemaps.zip" -F "service=admin" -F "env=production" -F "version=$version"
```

上传用的令牌要有 OpenObserve 的 `sourcemaps` 权限，只在 CI 中使用。默认的版本号每次构建随机生成；用 `tenonVersion({ version: commit, sourcemaps: "sourcemaps" })` 传入提交哈希，同一个提交的多次构建版本相同。

<a id="privacy"></a>
## 隐私与内容安全策略

- `defaultPrivacyLevel` 保持 `mask-user-input`：会话回放中输入框的内容被遮盖。后台页面中展示的业务数据同样会被录下，按需要用 `mask`，或者只对出错的会话开启回放（`sessionReplaySampleRate: 0`，出错时调用 `startSessionReplayRecording()`）。
- `trackUserInteractions` 记录被点击的元素的文字，可能包含业务数据，确有需要时再开。
- 给前端加了内容安全策略（CSP）时：`connect-src` 要包含 OpenObserve 的地址；会话回放用 Web Worker，需要 `worker-src blob:`，否则录制静默失败。

<a id="troubleshooting"></a>
## 常见问题

**观测平台中的堆栈仍然是压缩后的位置**

source map 没有上传，或者上传时的版本号与页面上报的版本不一致。检查 CI 中上传 source map 的步骤，见 [Source map](#source-maps)。

**前端错误跳不到后端的链路**

接口请求没有带上 `traceparent`，或者服务端没有安装 `httpx.Telemetry`，见[前后端的链路](#tracing)。

**浏览器控制台报错上报请求被内容安全策略拒绝**

观测平台的地址没有加入 `connect-src`，见[隐私与内容安全策略](#privacy)。

<a id="api-reference"></a>
## API 参考

| 名称 | 说明 |
| --- | --- |
| `createApp({ onError })` | 页面出错时调用：`(error, report)`，`report` 有 `routeId`、`pathname`、`error`（`describeError` 的结果） |
| `useReportError(error)` | 路由自己的错误组件中报告错误 |
| `shouldReport(error)` | 错误是否需要报告 |
| `app.reportError(error, where)` | 直接报告一个错误，经过同样的过滤和去重 |
| `tenonVersion({ sourcemaps })` | `@go-tenon/shell/vite`：生成 source map 并移出 `dist` |
| `httpx.SPA` | 不提供 `.map` 文件 |
