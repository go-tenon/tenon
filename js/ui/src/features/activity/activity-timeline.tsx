// 操作记录的时间线：一个对象的历史（例如文章的修改记录），或者一个人的操作记录。
// 逻辑来自 model.ts 中的 useActivity，接口由应用定义，见文档“操作日志”。
//
//   <ActivityTimeline source={{ key: ["article", id], list: (before) => api.article.history(id, { before }).then((r) => r.items) }} />

import { Alert, Anchor, Button, Center, Loader, Stack, Text, Timeline } from "@mantine/core"
import { useRouter } from "@tanstack/react-router"
import type { ReactNode } from "react"
import { useText } from "../../i18n/i18n"
import { useFormatDateTime } from "../../theme/format"
import { type ActivityItem, type ActivitySource, useActivity } from "./model"

/** ActivityTimeline 的选项。 */
export interface ActivityTimelineProps {
  source: ActivitySource
  /** 点击一条记录；默认打开它的 href（应用内的地址），没有 href 的记录不能点击。 */
  onOpen?: (item: ActivityItem) => void
  /** 没有记录时显示的文字，默认“还没有操作记录”（当前语言的）。 */
  empty?: ReactNode
}

/** 按时间从新到旧显示操作记录，到底时“加载更多”。 */
export function ActivityTimeline({ source, onOpen, empty }: ActivityTimelineProps): ReactNode {
  const formatDateTime = useFormatDateTime()
  const t = useText()
  const a = useActivity(source)
  const router = useRouter()
  if (a.loading) {
    return (
      <Center p="lg">
        <Loader size="sm" />
      </Center>
    )
  }
  if (a.error && a.items.length === 0) {
    return (
      <Alert color="red" variant="light" title={t("tenon.activity.load_failed")}>
        <Button size="xs" variant="default" mt="xs" onClick={() => void a.refresh()}>
          {t("tenon.common.retry")}
        </Button>
      </Alert>
    )
  }
  if (a.items.length === 0) {
    return (
      <Text c="dimmed" size="sm" ta="center" p="lg">
        {empty ?? t("tenon.activity.empty")}
      </Text>
    )
  }
  const open = (item: ActivityItem) => {
    if (onOpen) onOpen(item)
    else if (item.href) void router.navigate({ href: item.href })
  }
  return (
    <Stack gap="md">
      <Timeline bulletSize={10} lineWidth={1}>
        {a.items.map((item) => (
          <Timeline.Item key={item.id}>
            {item.href || onOpen ? (
              <Anchor component="button" type="button" size="sm" onClick={() => open(item)}>
                {item.text}
              </Anchor>
            ) : (
              <Text size="sm">{item.text}</Text>
            )}
            <Text size="xs" c="dimmed" mt={2}>
              {[
                item.actor ?? t("tenon.activity.system"),
                item.impersonator && t("tenon.activity.on_behalf", { name: item.impersonator }),
                formatDateTime(item.created_at),
              ]
                .filter(Boolean)
                .join(" · ")}
            </Text>
          </Timeline.Item>
        ))}
      </Timeline>
      {a.hasMore && (
        <Button variant="default" size="xs" onClick={a.loadMore}>
          {t("tenon.common.load_more")}
        </Button>
      )}
    </Stack>
  )
}
