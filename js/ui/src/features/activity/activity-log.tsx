// 操作日志表格：一个对象的历史（例如文章的修改记录）、一个人的操作记录，或整个租户的操作日志。
// 逻辑来自 model.ts 中的 useActivity，接口由应用定义，见文档“操作日志”。
//
//   <ActivityLog source={{ key: ["article", id], list: (p) => api.article.history(id, p) }} />

import { Alert, Anchor, Button, Group, Stack, Text } from "@mantine/core"
import { useRouter } from "@tanstack/react-router"
import type { ReactNode } from "react"
import { type Column, DataTable } from "../../components/data-table/data-table"
import { useText } from "../../i18n/i18n"
import { useFormatDateTime } from "../../theme/format"
import { type ActivityItem, type ActivitySource, useActivity } from "./model"

/** ActivityLog 的选项。 */
export interface ActivityLogProps {
  source: ActivitySource
  /** 点击一条记录；默认打开它的 href（应用内的地址），没有 href 的记录不能点击。 */
  onOpen?: (item: ActivityItem) => void
  /** 表格上方左侧的内容，例如筛选条件。 */
  toolbar?: ReactNode
  /** 每页条数的选项，默认 10、20、50、100。 */
  perPageOptions?: number[]
  /** 没有记录时显示的文字，默认“还没有操作记录”（当前语言的）。 */
  empty?: ReactNode
}

/** 按时间从新到旧以固定分页表格显示操作记录。 */
export function ActivityLog({ source, onOpen, toolbar, perPageOptions, empty }: ActivityLogProps): ReactNode {
  const formatDateTime = useFormatDateTime()
  const t = useText()
  const a = useActivity(source)
  const router = useRouter()

  if (a.error) {
    return (
      <Stack gap="sm">
        {toolbar}
        <Alert color="red" variant="light" title={t("tenon.activity.load_failed")}>
          <Button size="xs" variant="default" mt="xs" onClick={() => void a.refresh()}>
            {t("tenon.common.retry")}
          </Button>
        </Alert>
      </Stack>
    )
  }

  const open = (item: ActivityItem) => {
    if (onOpen) onOpen(item)
    else if (item.href) void router.navigate({ href: item.href })
  }

  const columns: Column<ActivityItem>[] = [
    {
      accessor: "created_at",
      title: t("tenon.activity.time"),
      noWrap: true,
      width: 148,
      render: (item) => (
        <Text span size="sm" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
          {formatDateTime(item.created_at)}
        </Text>
      ),
    },
    {
      accessor: "actor",
      title: t("tenon.activity.actor"),
      noWrap: true,
      render: (item) => (
        <Group gap={4} wrap="nowrap">
          <Text span size="sm" c={item.actor ? undefined : "dimmed"}>
            {item.actor ?? t("tenon.activity.system")}
          </Text>
          {item.impersonator && (
            <Text span size="xs" c="dimmed">
              {t("tenon.activity.on_behalf", { name: item.impersonator })}
            </Text>
          )}
        </Group>
      ),
    },
    {
      accessor: "text",
      title: t("tenon.activity.action"),
      render: (item) =>
        item.href || onOpen ? (
          <Anchor component="button" type="button" size="sm" ta="left" onClick={() => open(item)}>
            {item.text}
          </Anchor>
        ) : (
          <Text span size="sm">
            {item.text}
          </Text>
        ),
    },
  ]

  return (
    <DataTable
      records={a.items}
      total={a.total}
      loading={a.loading}
      list={{
        page: a.page,
        perPage: a.perPage,
        setPage: a.setPage,
        setPerPage: a.setPerPage,
        sort: undefined,
        setSort: () => {},
      }}
      perPageOptions={perPageOptions}
      columns={columns}
      toolbar={toolbar}
      empty={empty ?? t("tenon.activity.empty")}
    />
  )
}
