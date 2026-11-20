// 通知菜单：通知（model.ts 的 useNotifications）的外观，顶栏的铃铛。作为布局的 actions 放在顶栏中。

import {
  ActionIcon,
  Box,
  Button,
  Group,
  Indicator,
  Popover,
  ScrollArea,
  Stack,
  Text,
  UnstyledButton,
} from "@mantine/core"
import { IconBell } from "@tabler/icons-react"
import { useNavigate } from "@tanstack/react-router"
import { type ReactNode, useState } from "react"
import { useText } from "../../i18n/i18n"
import { useFormatDateTime } from "../../theme/format"
import { type NotificationsSource, useNotifications } from "./model"

/** 通知：铃铛显示未读数，弹出最近的通知，点击打开并标记已读。 */
export function NotificationsMenu({ source }: { source: NotificationsSource }): ReactNode {
  const n = useNotifications(source)
  const navigate = useNavigate()
  const [opened, setOpened] = useState(false)
  const formatDateTime = useFormatDateTime()
  const t = useText()
  return (
    <Popover width={360} position="bottom-end" opened={opened} onChange={setOpened}>
      <Popover.Target>
        <Indicator disabled={n.unread === 0} label={n.unread > 99 ? "99+" : n.unread} size={16} offset={6} color="red">
          <ActionIcon
            size={32}
            aria-label={
              n.unread > 0 ? t("tenon.notifications.unread", { count: n.unread }) : t("tenon.notifications.title")
            }
            onClick={() => setOpened((o) => !o)}
          >
            <IconBell size={16} stroke={1.5} />
          </ActionIcon>
        </Indicator>
      </Popover.Target>
      <Popover.Dropdown p={0}>
        <Group justify="space-between" px="md" py="sm" style={{ borderBottom: "1px solid var(--tn-border)" }}>
          <Text fw={600} size="sm">
            {t("tenon.notifications.title")}
          </Text>
          {n.unread > 0 && (
            <Button variant="subtle" size="compact-xs" onClick={() => void n.markAllRead()}>
              {t("tenon.notifications.read_all")}
            </Button>
          )}
        </Group>
        <ScrollArea.Autosize mah={420}>
          {n.items.length === 0 ? (
            <Text c="dimmed" size="sm" ta="center" py="xl">
              {n.loading ? t("tenon.common.loading") : t("tenon.notifications.empty")}
            </Text>
          ) : (
            <Stack gap={0}>
              {n.items.map((item) => (
                <UnstyledButton
                  key={item.id}
                  px="md"
                  py="sm"
                  style={{ borderBottom: "1px solid var(--tn-border)" }}
                  onClick={() => {
                    if (!item.read) void n.markRead(item.id)
                    if (item.href) {
                      setOpened(false)
                      void navigate({ to: item.href })
                    }
                  }}
                >
                  <Group gap="sm" align="flex-start" wrap="nowrap">
                    <Box
                      mt={7}
                      w={6}
                      h={6}
                      style={{ borderRadius: "50%", flex: "none" }}
                      bg={item.read ? "transparent" : "brand.6"}
                    />
                    <div style={{ minWidth: 0 }}>
                      <Text size="sm" fw={item.read ? 400 : 500} lineClamp={2}>
                        {item.title}
                      </Text>
                      {item.body && (
                        <Text size="xs" c="dimmed" lineClamp={2}>
                          {item.body}
                        </Text>
                      )}
                      <Text size="xs" c="dimmed" mt={2}>
                        {formatDateTime(item.created_at)}
                      </Text>
                    </div>
                  </Group>
                </UnstyledButton>
              ))}
              {n.hasMore && (
                <Button variant="subtle" color="gray" radius={0} onClick={n.loadMore}>
                  {t("tenon.common.load_more")}
                </Button>
              )}
            </Stack>
          )}
        </ScrollArea.Autosize>
      </Popover.Dropdown>
    </Popover>
  )
}
