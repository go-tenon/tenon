// 错误页：403、404、500、网络错误。RouteError 是路由的 defaultErrorComponent，按错误的种类选择页面。

import { describeError } from "@go-tenon/client"
import { Button, Group, Stack, Text, Title } from "@mantine/core"
import { type ErrorComponentProps, Link, useRouter } from "@tanstack/react-router"
import type { ReactNode } from "react"
import { isForbidden, useReportError } from "../app/app"
import { type UiMessageId, useText } from "../i18n/i18n"

/** 错误页的种类。 */
export type ErrorPageKind = "forbidden" | "not_found" | "server" | "network"

// 每种错误页的状态码、标题和说明的消息 ID。
const copy: Record<ErrorPageKind, { code: string; title: UiMessageId; description: UiMessageId }> = {
  forbidden: {
    code: "403",
    title: "tenon.error_page.forbidden_title",
    description: "tenon.error_page.forbidden_description",
  },
  not_found: {
    code: "404",
    title: "tenon.error_page.not_found_title",
    description: "tenon.error_page.not_found_description",
  },
  server: { code: "500", title: "tenon.error_page.server_title", description: "tenon.error_page.server_description" },
  network: { code: "", title: "tenon.error_page.network_title", description: "tenon.error_page.network_description" },
}

/** ErrorPage 的选项。 */
export interface ErrorPageProps {
  kind: ErrorPageKind
  /** 替换默认的标题。 */
  title?: ReactNode
  /** 替换默认的说明。 */
  description?: ReactNode
  /** 操作按钮；默认是“回到首页”。 */
  actions?: ReactNode
  /** 请求 ID，便于反馈问题时定位。 */
  requestId?: string
}

/** 错误页。 */
export function ErrorPage({ kind, title, description, actions, requestId }: ErrorPageProps): ReactNode {
  const t = useText()
  const c = copy[kind]
  return (
    <div className="tn-error" role={kind === "server" || kind === "network" ? "alert" : undefined}>
      <Stack align="center" gap="sm" maw={420}>
        {c.code && <div className="tn-error-code">{c.code}</div>}
        <Title order={3}>{title ?? t(c.title)}</Title>
        <Text c="dimmed" size="sm">
          {description ?? t(c.description)}
        </Text>
        <Group mt="md" gap="sm">
          {actions ?? (
            <Button component={Link} to="/" variant="default">
              {t("tenon.error_page.home")}
            </Button>
          )}
        </Group>
        {requestId && (
          <Text size="xs" c="dimmed" mt="md" ff="monospace">
            {t("tenon.error_page.request_id", { id: requestId })}
          </Text>
        )}
      </Stack>
    </div>
  )
}

/** 404 页面，用作路由的 defaultNotFoundComponent。 */
export function NotFoundPage(): ReactNode {
  return <ErrorPage kind="not_found" />
}

/** 路由的错误组件，用作 defaultErrorComponent：没有权限显示 403，不存在显示 404，其他显示可以重试的错误页。 */
export function RouteError({ error, reset }: ErrorComponentProps): ReactNode {
  useReportError(error)
  const t = useText()
  const router = useRouter()
  if (isForbidden(error)) return <ErrorPage kind="forbidden" />
  const info = describeError(error)
  if (info.kind === "forbidden" || info.status === 403) return <ErrorPage kind="forbidden" description={info.message} />
  if (info.kind === "not_found" || info.status === 404) return <ErrorPage kind="not_found" description={info.message} />
  const retry = (
    <Button
      onClick={() => {
        reset()
        void router.invalidate()
      }}
    >
      {t("tenon.common.retry")}
    </Button>
  )
  return <ErrorPage kind={info.kind === "network" ? "network" : "server"} actions={retry} requestId={info.requestId} />
}
