// 运行环境自检面板：doctor/doctorapi 的运维页面，按依赖分组展示各项检查结果、问题摘要与修改方法。
// 数据来自应用生成的客户端分组 api.system（operationId: system-doctor）。

import { Alert, Anchor, Badge, Button, Code, Divider, Group, Paper, SimpleGrid, Stack, Text } from "@mantine/core"
import { IconRefresh } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import type { ReactNode } from "react"
import { LoadingStage } from "../components/loading"
import { Page } from "../components/page"
import { StatCard } from "../components/stat-card"
import { type UiMessageId, useText } from "../i18n/i18n"

// ———————————————————————————— 接口 ————————————————————————————

/** 单项检查的状态：与 Go 的 doctor.Status 一致。 */
export type DoctorStatus = "passed" | "warning" | "failed" | "skipped"

/** 单项检查结果：doctor.DoctorResult。 */
export interface DoctorResultData {
  group: string
  check: string
  status: DoctorStatus
  summary?: string
  fix?: string
  doc?: string
}

/** 一次自检的报告：doctor.DoctorReport。 */
export interface DoctorReportData {
  results: DoctorResultData[]
}

/** 运行环境自检接口：tenon generate 为 doctor/doctorapi 生成的客户端分组，即 api.system。 */
export interface DoctorApi {
  doctor(): Promise<DoctorReportData>
}

/** DoctorPanel 的选项。 */
export interface DoctorPanelProps {
  /** 生成的客户端分组：api.system。 */
  api: DoctorApi
  /** 页面的标题，默认“运行环境自检”。 */
  title?: ReactNode
}

// 每种检查状态的标签文字与语义色。
const statusMeta: Record<DoctorStatus, { label: UiMessageId; color: string; rank: number }> = {
  passed: { label: "tenon.doctor.passed", color: "green", rank: 0 },
  skipped: { label: "tenon.doctor.skipped", color: "gray", rank: 1 },
  warning: { label: "tenon.doctor.warning", color: "deep-orange", rank: 2 },
  failed: { label: "tenon.doctor.failed", color: "red", rank: 3 },
}

const doctorKey = ["tenon", "system", "doctor"]

/** 按 group 分组并保留原始顺序，同时计算每组的最高严重级别。 */
function groupResults(results: DoctorResultData[]) {
  const map = new Map<string, { name: string; status: DoctorStatus; items: DoctorResultData[] }>()
  for (const r of results) {
    let g = map.get(r.group)
    if (!g) {
      g = { name: r.group, status: "passed", items: [] }
      map.set(r.group, g)
    }
    g.items.push(r)
    if (statusMeta[r.status].rank > statusMeta[g.status].rank) {
      g.status = r.status
    }
  }
  return [...map.values()]
}

/** 判断文档链接是否可安全外链打开。 */
function isSafeHref(url: string): boolean {
  return url.startsWith("https://") || url.startsWith("http://") || url.startsWith("/")
}

/**
 * 运行环境自检面板：
 *
 *	export const Route = createFileRoute("/_app/doctor")({
 *	  staticData: { title: "环境自检", icon: "stethoscope", permission: "system:read" },
 *	  component: () => <DoctorPanel api={api.system} />,
 *	})
 */
export function DoctorPanel({ api, title }: DoctorPanelProps): ReactNode {
  const t = useText()
  const query = useQuery({ queryKey: doctorKey, queryFn: () => api.doctor() })
  const loading = query.isPending || query.isFetching
  const results = query.data?.results ?? []
  const counts = {
    passed: results.filter((r) => r.status === "passed").length,
    warning: results.filter((r) => r.status === "warning").length,
    failed: results.filter((r) => r.status === "failed").length,
    skipped: results.filter((r) => r.status === "skipped").length,
  }
  const groups = groupResults(results)

  return (
    <Page
      title={title ?? t("tenon.doctor.title")}
      description={t("tenon.doctor.description")}
      actions={
        <Button
          variant="default"
          leftSection={<IconRefresh size={16} />}
          loading={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {t("tenon.doctor.rerun")}
        </Button>
      }
    >
      <LoadingStage loading={loading}>
        {query.isError ? (
          <Alert color="red" variant="light" title={t("tenon.doctor.load_failed")} role="alert">
            <Stack gap="xs">
              <Text size="sm">{query.error instanceof Error ? query.error.message : String(query.error)}</Text>
              <Group>
                <Button size="xs" variant="light" color="red" onClick={() => void query.refetch()}>
                  {t("tenon.common.retry")}
                </Button>
              </Group>
            </Stack>
          </Alert>
        ) : results.length === 0 ? (
          <Text c="dimmed" size="sm">
            {t("tenon.doctor.empty")}
          </Text>
        ) : (
          <Stack gap="md">
            {counts.failed > 0 ? (
              <Alert color="red" variant="light" role="alert">
                {t("tenon.doctor.failed_summary", { count: counts.failed })}
              </Alert>
            ) : counts.warning > 0 ? (
              <Alert color="deep-orange" variant="light" role="alert">
                {t("tenon.doctor.warning_summary", { count: counts.warning })}
              </Alert>
            ) : (
              <Alert color="green" variant="light">
                {t("tenon.doctor.all_passed")}
              </Alert>
            )}

            <SimpleGrid cols={{ base: 2, sm: 4 }}>
              <StatCard label={t("tenon.doctor.passed")} value={counts.passed} />
              <StatCard label={t("tenon.doctor.warning")} value={counts.warning} />
              <StatCard label={t("tenon.doctor.failed")} value={counts.failed} />
              <StatCard label={t("tenon.doctor.skipped")} value={counts.skipped} />
            </SimpleGrid>

            {groups.map((g) => {
              const gm = statusMeta[g.status]
              return (
                <Paper key={g.name} withBorder radius="sm" p="md">
                  <Stack gap="sm">
                    <Group justify="space-between">
                      <Text fw={600} size="sm">
                        {g.name}
                      </Text>
                      <Badge color={gm.color} variant="light">
                        {t(gm.label)}
                      </Badge>
                    </Group>
                    <Divider />
                    <Stack gap="sm">
                      {g.items.map((item) => {
                        const m = statusMeta[item.status]
                        return (
                          <Stack key={`${item.group}:${item.check}`} gap={4}>
                            <Group gap="sm" align="center" wrap="wrap">
                              <Badge color={m.color} variant="light" w={64}>
                                {t(m.label)}
                              </Badge>
                              <Code>{item.check}</Code>
                              {item.summary && (
                                <Text size="sm" c={item.status === "failed" ? "red" : undefined}>
                                  {item.summary}
                                </Text>
                              )}
                              {item.doc && isSafeHref(item.doc) && (
                                <Anchor href={item.doc} target="_blank" rel="noreferrer" size="xs" ml="auto">
                                  {t("tenon.doctor.doc")}
                                </Anchor>
                              )}
                            </Group>
                            {item.fix && (
                              <Stack gap={2} pl={{ base: 0, sm: 76 }}>
                                <Text size="xs" c="dimmed">
                                  {t("tenon.doctor.fix")}
                                </Text>
                                <Code block>{item.fix}</Code>
                              </Stack>
                            )}
                          </Stack>
                        )
                      })}
                    </Stack>
                  </Stack>
                </Paper>
              )
            })}
          </Stack>
        )}
      </LoadingStage>
    </Page>
  )
}
