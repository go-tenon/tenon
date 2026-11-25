# 前端观测

浏览器中的错误、请求和性能由观测平台的浏览器 SDK 收集，默认集成 [OpenObserve](https://openobserve.ai/)。`tenon init` 与 `tenon add frontend` 生成的前端已默认安装 `@openobserve/browser-rum` 与 `@openobserve/browser-logs`，并在 `src/client.tsx`、`src/observe.ts`、`src/router.tsx` 和 `src/routes/__root.tsx` 中完成接线。

运行时配置由后端配置文件的 `telemetry.browser` 段经 Go 注入页面（`<meta name="tenon-telemetry">`）：未填写时（例如本地开发）不初始化、不发任何请求；生产配置填好 `site` 和 `client_token` 后自动生效，Go 默认 CSP 也会自动放行上报域名与 `worker-src blob:`。框架同时补上 SDK 做不到的三件事：`@go-tenon/ui` 的 `createApp({ onError })` 交出路由错误组件接住的错误；请求带上 `traceparent`，后端链路接在浏览器之后；`@go-tenon/start/vite` 的 `tenon({ sourcemaps })` 生成 source map 但不放进构建结果。

<a id="installing"></a>
## 运行时配置与浏览器 SDK（OpenObserve）

在配置文件（如 `config/prod.yaml`）中填写 `telemetry.browser`，并将 `cfg.Telemetry` 交给 `frontend.Options.Telemetry`：

```yaml
telemetry:
  service: admin
  browser:
    site: https://o2.example.com      # 支持带协议 URL 或纯域名；留空时不启用
    client_token: <rum-client-token>  # 公开的 RUM 写入令牌
    organization: default             # 默认 default
    environment: production           # 默认 production
```

已有项目手动接入时，先在前端目录（`apps/<名称>`）中安装 SDK：

```bash
bun add @openobserve/browser-rum@0.4.3 @openobserve/browser-logs@0.4.3
```

在 `src/routes/__root.tsx` 的 `head()` 中写入 `telemetryMeta()`（来自 `@go-tenon/start`），并在浏览器入口 `src/client.tsx` 中通过 `browserTelemetry()` 读取配置并初始化：

```tsx title="src/client.tsx"
import { browserTelemetry } from "@go-tenon/start"
import { openobserveLogs } from "@openobserve/browser-logs"
import { openobserveRum } from "@openobserve/browser-rum"
import { StartClient } from "@tanstack/react-start/client"
import { StrictMode, startTransition } from "react"
import { hydrateRoot } from "react-dom/client"

const telemetry = browserTelemetry()
if (telemetry) {
  const common = {
    clientToken: telemetry.clientToken,
    site: telemetry.site,
    insecureHTTP: telemetry.insecureHTTP ?? false,
    organizationIdentifier: telemetry.organization,
    service: telemetry.service,
    env: telemetry.environment,
    version: import.meta.env.TENON_BUILD_VERSION ?? "dev",
  }
  openobserveRum.init({
    ...common,
    applicationId: telemetry.service,
    trackResources: true,
    trackLongTasks: true,
    trackUserInteractions: false,
    defaultPrivacyLevel: "mask-user-input",
    allowedTracingUrls: [(url: string) => url.startsWith(`${location.origin}/api/`)],
  })
  openobserveLogs.init({ ...common, forwardErrorsToLogs: true })
}

startTransition(() => {
  hydrateRoot(document, <StrictMode><StartClient /></StrictMode>)
})
```

- `client_token` 只能写入 RUM 与前端日志数据，属于公开客户端令牌，经页面 `<meta>` 下发后随配置即时生效，无需重新编译前端。
- SDK 自动捕获未捕获的异常、未处理的 Promise 拒绝、`console.error` 和失败的请求。不再自己监听 `window` 的 `error`，否则同一个错误报告两次。

<a id="route-errors"></a>
## 用 `createApp` 的 `onError` 报告页面错误

页面渲染或 loader 中抛出的错误被路由的错误组件接住，不冒泡到 `window`，SDK 看不到。把它们交给 `createApp` 的 `onError`：

```ts
import { openobserveLogs } from "@openobserve/browser-logs"

export const app = createApp({
  // ……
  onError: (error, report) => {
    // report：routeId（例如 /_app/articles/$id）、pathname、error（describeError 的结果，接口错误带 requestId）
    openobserveLogs.logger.error("route error", { route: report.routeId, path: report.pathname, request_id: report.error.requestId }, error as Error)
  },
})
```

- 默认的错误组件 `RouteError` 显示之前先报告，同一个错误只报告一次。
- 只报告 `shouldReport(error)` 为真的错误：代码中的错误、代码块加载失败和 5xx。其他接口错误、缺少权限、请求被取消不报告。
- 路由自己写了 `errorComponent` 时，在其中调用 `useReportError(error)`。路由之外的错误用 `app.reportError(error, { routeId, pathname })`，经过同样的过滤和去重。
- `requestId` 是服务端日志中的 `request_id`，见[在日志中找到一次请求](../general/logging.md#finding-a-request)。

<a id="context"></a>
## 设置用户、租户和页面名

登录、退出、切换租户时更新 SDK 中的用户；页面切换后用路由模板命名页面，`/articles/1` 和 `/articles/2` 归为同一个页面。`app` 是 `createApp` 的返回值：

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

用户只记录账号 ID，姓名、邮箱等个人信息不写进观测平台。

<a id="tracing"></a>
## 前端请求接到后端链路（`traceparent`）

`allowedTracingUrls` 中的请求带上 `traceparent` 请求头，后端的 HTTP 服务装了 `httpx.Telemetry` 时[接在浏览器的链路之后](../general/telemetry.md#http)。后端把链路推送到同一个 OpenObserve（[`telemetry.endpoint`](../general/telemetry.md#setup)），错误详情就能跳到后端的链路。前端错误跳不到后端链路时，检查这两点。

API 在其他域名时，[CORS](../http/middleware.md#cors) 要允许 `traceparent` 和 `tracestate` 请求头，否则浏览器在预检时去掉它们。

<a id="source-maps"></a>
## 上传 source map（`tenon({ sourcemaps })`）

在 `vite.config.ts` 的 `tenon()` 插件中打开：

```ts
tenon({ sourcemaps: "sourcemaps" })
```

构建时生成 source map，构建结束后从 `dist/client` 移到前端目录下的 `sourcemaps/`：source map 等于源码，不能随程序公开，`tenon build` 也不嵌入 `.map`。把 `sourcemaps/` 加入 `.gitignore`。

CI 在构建之后上传，`service`、`env`、`version` 与 SDK 中的不一致时堆栈仍是压缩后的位置：

```bash
cd apps/admin
version=$(jq -r .version dist/client/version.json)
(cd sourcemaps && zip -qr ../sourcemaps.zip .)
curl -fsS -X POST "https://o2.example.com/api/default/sourcemaps" \
  -H "Authorization: Basic $O2_SOURCEMAP_TOKEN" \
  -F "file=@sourcemaps.zip" -F "service=admin" -F "env=production" -F "version=$version"
```

版本号默认每次构建随机生成；`tenon({ version: commit, sourcemaps: "sourcemaps" })` 传入提交哈希，同一个提交的构建版本相同。

<a id="privacy"></a>
## 隐私与内容安全策略

- `defaultPrivacyLevel` 保持 `mask-user-input`，会话回放中输入框的内容被遮盖。页面上展示的业务数据同样会被录下：按需要用 `mask`，或者只对出错的会话开启回放（`sessionReplaySampleRate: 0`，出错时调用 `startSessionReplayRecording()`）。
- 加了内容安全策略（CSP）时，`connect-src` 要包含 OpenObserve 的地址，否则上报被拒绝；会话回放用 Web Worker，需要 `worker-src blob:`，否则录制静默失败。
