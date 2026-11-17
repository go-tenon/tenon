// 顶栏的菜单：用户菜单、租户切换、通知、后台任务。放进 AppLayout 的插槽：
//
//   <Fill name="user"><UserMenu name={me.name} logout={api.auth.logout} /></Fill>

import {
  dismissOperation,
  type NotificationsSource,
  type TrackedOperation,
  useApp,
  useNotifications,
  useOperations,
  useTenant,
} from "@go-tenon/shell"
import {
  ActionIcon,
  Avatar,
  Badge,
  Box,
  Button,
  Group,
  Indicator,
  Loader,
  Menu,
  Popover,
  Progress,
  ScrollArea,
  Stack,
  Text,
  Tooltip,
  UnstyledButton,
} from "@mantine/core"
import { IconBell, IconCheck, IconListCheck, IconLogout, IconSelector, IconX } from "@tabler/icons-react"
import { useNavigate } from "@tanstack/react-router"
import { type ReactNode, useState } from "react"

/** 用户菜单中的一项。 */
export interface UserMenuItem {
  label: ReactNode
  icon?: ReactNode
  /** 应用内的地址。 */
  to?: string
  onClick?: () => void
}

/** UserMenu 的选项。 */
export interface UserMenuProps {
  /** 显示的名称；读取中时为空。 */
  name?: string
  /** 名称下面的一行，例如邮箱。 */
  email?: string
  /** 头像的地址；没有时显示名称的首字。 */
  avatar?: string
  /** 调用应用的退出接口：撤销登录、清除会话 cookie。之后清除会话中的身份。 */
  logout?: () => Promise<unknown>
  /** 退出之前的菜单项，例如“账号设置”。 */
  items?: UserMenuItem[]
}

/** 用户菜单：头像和名称，菜单中有应用的菜单项和退出登录。 */
export function UserMenu({ name, email, avatar, logout, items = [] }: UserMenuProps): ReactNode {
  const app = useApp()
  const navigate = useNavigate()
  const signOut = async () => {
    try {
      await logout?.()
    } finally {
      app.session.signOut()
    }
  }
  return (
    <Menu width={220} position="bottom-end">
      <Menu.Target>
        <UnstyledButton aria-label="用户菜单" style={{ borderRadius: 999, padding: 2 }}>
          <Group gap={8} wrap="nowrap">
            <Avatar src={avatar} size={28} radius="xl" color="brand" variant="light">
              {name?.slice(0, 1)}
            </Avatar>
          </Group>
        </UnstyledButton>
      </Menu.Target>
      <Menu.Dropdown>
        {(name || email) && (
          <Box px="sm" py={8}>
            <Text size="sm" fw={500} truncate>
              {name}
            </Text>
            {email && (
              <Text size="xs" c="dimmed" truncate>
                {email}
              </Text>
            )}
          </Box>
        )}
        {(name || email) && <Menu.Divider />}
        {items.map((item, i) => (
          <Menu.Item
            // biome-ignore lint/suspicious/noArrayIndexKey: 菜单项是固定的列表
            key={i}
            leftSection={item.icon}
            onClick={() => (item.to ? navigate({ to: item.to }) : item.onClick?.())}
          >
            {item.label}
          </Menu.Item>
        ))}
        {items.length > 0 && <Menu.Divider />}
        <Menu.Item leftSection={<IconLogout size={16} stroke={1.6} />} onClick={() => void signOut()}>
          退出登录
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
  )
}

/** 租户切换：当前租户的名称，菜单中列出用户所属的全部租户。只有一个租户或不是多租户应用时只显示名称或不显示。 */
export function TenantSwitcher(): ReactNode {
  const { tenant, tenants, switchTo, current } = useTenant()
  if (tenants.length === 0) return null
  if (tenants.length === 1)
    return (
      <Text size="sm" fw={500} px="xs">
        {tenant?.name ?? tenants[0].name}
      </Text>
    )
  return (
    <Menu width={240} position="bottom-end">
      <Menu.Target>
        <Button
          variant="subtle"
          color="gray"
          rightSection={<IconSelector size={14} stroke={1.6} />}
          aria-label="切换租户"
          fw={500}
        >
          {tenant?.name ?? "选择租户"}
        </Button>
      </Menu.Target>
      <Menu.Dropdown>
        <Menu.Label>切换到</Menu.Label>
        {tenants.map((t) => (
          <Menu.Item
            key={t.id}
            onClick={() => void switchTo(t.id)}
            rightSection={t.id === current ? <IconCheck size={14} stroke={2} /> : undefined}
          >
            {t.name}
          </Menu.Item>
        ))}
      </Menu.Dropdown>
    </Menu>
  )
}

/** 通知：铃铛显示未读数，弹出最近的通知，点击打开并标记已读。 */
export function NotificationsMenu({ source }: { source: NotificationsSource }): ReactNode {
  const n = useNotifications(source)
  const navigate = useNavigate()
  const [opened, setOpened] = useState(false)
  const time = new Intl.DateTimeFormat("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  })
  return (
    <Popover width={360} position="bottom-end" opened={opened} onChange={setOpened}>
      <Popover.Target>
        <Indicator disabled={n.unread === 0} label={n.unread > 99 ? "99+" : n.unread} size={16} offset={6} color="red">
          <ActionIcon
            size="lg"
            aria-label={n.unread > 0 ? `通知，${n.unread} 条未读` : "通知"}
            onClick={() => setOpened((o) => !o)}
          >
            <IconBell size={18} stroke={1.6} />
          </ActionIcon>
        </Indicator>
      </Popover.Target>
      <Popover.Dropdown p={0}>
        <Group justify="space-between" px="md" py="sm" style={{ borderBottom: "1px solid var(--tn-border)" }}>
          <Text fw={600} size="sm">
            通知
          </Text>
          {n.unread > 0 && (
            <Button variant="subtle" size="compact-xs" onClick={() => void n.markAllRead()}>
              全部已读
            </Button>
          )}
        </Group>
        <ScrollArea.Autosize mah={420}>
          {n.items.length === 0 ? (
            <Text c="dimmed" size="sm" ta="center" py="xl">
              {n.loading ? "加载中…" : "没有通知"}
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
                        {time.format(new Date(item.created_at))}
                      </Text>
                    </div>
                  </Group>
                </UnstyledButton>
              ))}
              {n.hasMore && (
                <Button variant="subtle" color="gray" radius={0} onClick={n.loadMore}>
                  加载更多
                </Button>
              )}
            </Stack>
          )}
        </ScrollArea.Autosize>
      </Popover.Dropdown>
    </Popover>
  )
}

/** 后台任务：导入、导出等耗时操作的进度。有进行中的任务时图标转动。 */
export function OperationsMenu(): ReactNode {
  const ops = useOperations()
  const app = useApp()
  if (ops.length === 0) return null
  const running = ops.filter((o) => !o.finished).length
  return (
    <Popover width={340} position="bottom-end">
      <Popover.Target>
        <Tooltip label={running > 0 ? `${running} 个任务进行中` : "后台任务"}>
          <ActionIcon size="lg" aria-label="后台任务">
            {running > 0 ? <Loader size={16} /> : <IconListCheck size={18} stroke={1.6} />}
          </ActionIcon>
        </Tooltip>
      </Popover.Target>
      <Popover.Dropdown p={0}>
        <Text fw={600} size="sm" px="md" py="sm" style={{ borderBottom: "1px solid var(--tn-border)" }}>
          后台任务
        </Text>
        <Stack gap={0}>
          {ops.map((op) => (
            <OperationRow key={op.id} op={op} onDismiss={() => dismissOperation(app, op.id)} />
          ))}
        </Stack>
      </Popover.Dropdown>
    </Popover>
  )
}

function OperationRow({ op, onDismiss }: { op: TrackedOperation; onDismiss: () => void }): ReactNode {
  const status = op.error ? "failed" : op.operation?.status
  const failed = status === "failed"
  const done = op.finished && !failed
  return (
    <Box px="md" py="sm" style={{ borderBottom: "1px solid var(--tn-border)" }}>
      <Group justify="space-between" wrap="nowrap" gap="xs">
        <Text size="sm" truncate>
          {op.title}
        </Text>
        {op.finished || op.error ? (
          <Group gap={4} wrap="nowrap">
            <Badge color={failed ? "red" : "green"} size="sm">
              {failed ? "失败" : "完成"}
            </Badge>
            <ActionIcon size="sm" aria-label="移除" onClick={onDismiss}>
              <IconX size={14} />
            </ActionIcon>
          </Group>
        ) : (
          <Text size="xs" c="dimmed">
            {op.ratio !== undefined ? `${Math.round(op.ratio * 100)}%` : "处理中"}
          </Text>
        )}
      </Group>
      {!done && !failed && (
        <Progress value={(op.ratio ?? 0) * 100} size="sm" mt={8} animated={op.ratio === undefined} />
      )}
      {op.operation && op.operation.failed > 0 && (
        <Text size="xs" c="red" mt={4}>
          {op.operation.failed} 项失败
        </Text>
      )}
    </Box>
  )
}
