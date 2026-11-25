import { browserTelemetry } from "@go-tenon/start"
import { openobserveLogs } from "@openobserve/browser-logs"
import { openobserveRum } from "@openobserve/browser-rum"
import { StartClient } from "@tanstack/react-start/client"
import { StrictMode, startTransition } from "react"
import { hydrateRoot } from "react-dom/client"
import { enableObserve } from "./observe"

// 读取 Go 从配置文件下发的公开观测配置；未配置（例如本地开发）时不初始化，不发送任何数据。
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
  enableObserve({
    reportError: (error, report) => {
      openobserveLogs.logger.error(
        "route error",
        { route: report.routeId, path: report.pathname, request_id: report.error.requestId },
        error instanceof Error ? error : undefined,
      )
    },
    setViewName: (name) => openobserveRum.setViewName(name),
    setUser: (id, tenant) => {
      openobserveRum.setUser({ id })
      openobserveRum.setGlobalContextProperty("tenant", tenant)
    },
    clearUser: () => openobserveRum.clearUser(),
  })
}

startTransition(() => {
  hydrateRoot(
    document,
    <StrictMode>
      <StartClient />
    </StrictMode>,
  )
})
