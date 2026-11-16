// 顶栏上的通知铃铛、后台操作的任务中心，以及页面中的操作进度。逻辑在 @go-tenon/shell，这里只负责外观。

import { BellOutlined, CloseOutlined, SyncOutlined } from "@ant-design/icons"
import {
  dismissOperation,
  type NotificationItem,
  type NotificationsSource,
  type TrackedOperation,
  useApp,
  useNotifications,
  useOperation,
  useOperations,
} from "@go-tenon/shell"
import { useRouter } from "@tanstack/react-router"
import { Badge, Button, Empty, List, Popover, Progress, Space, Spin, Typography, theme } from "antd"
import { type ReactNode, useState } from "react"

const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "short" })
const when = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : timeFormat.format(d)
}

/** NotificationBell 的选项。 */
export interface NotificationBellProps {
  /** 通知的接口，见 useNotifications。 */
  source: NotificationsSource
  /** 下拉的宽度，默认 360。 */
  width?: number
  /** 点击一条通知：默认标记已读，有 href 时打开它。 */
  onOpen?: (n: NotificationItem) => void
}

/**
 * 通知铃铛：角标是未读数，点开是通知列表。点击一条通知标记已读并打开它的页面。放在 actions 插槽：
 *
 *	<Fill name="actions"><NotificationBell source={notifications} /></Fill>
 */
export function NotificationBell({ source, width = 360, onOpen }: NotificationBellProps): ReactNode {
  const n = useNotifications(source)
  const router = useRouter()
  const [open, setOpen] = useState(false)
  const { token } = theme.useToken()
  const openItem = (item: NotificationItem) => {
    void n.markRead(item.id)
    if (onOpen) onOpen(item)
    else if (item.href) void router.navigate({ href: item.href })
    setOpen(false)
  }
  const content = (
    <div style={{ width }}>
      {n.loading ? (
        <Spin style={{ display: "block", padding: 24 }} />
      ) : n.items.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有通知" />
      ) : (
        <List
          size="small"
          style={{ maxHeight: 420, overflow: "auto" }}
          dataSource={n.items}
          renderItem={(item) => (
            <List.Item
              key={item.id}
              onClick={() => openItem(item)}
              style={{ cursor: "pointer", background: item.read ? undefined : token.colorPrimaryBg }}
            >
              <List.Item.Meta
                title={
                  <Typography.Text strong={!item.read} ellipsis>
                    {item.title}
                  </Typography.Text>
                }
                description={
                  <>
                    {item.body && <div>{item.body}</div>}
                    <Typography.Text type="secondary">{when(item.created_at)}</Typography.Text>
                  </>
                }
              />
            </List.Item>
          )}
          loadMore={
            n.hasMore ? (
              <Button type="link" block onClick={n.loadMore}>
                加载更多
              </Button>
            ) : undefined
          }
        />
      )}
    </div>
  )
  return (
    <Popover
      trigger="click"
      placement="bottomRight"
      open={open}
      onOpenChange={setOpen}
      title={
        <Space style={{ width: "100%", justifyContent: "space-between" }}>
          <span>通知</span>
          <Button type="link" size="small" disabled={n.unread === 0} onClick={() => void n.markAllRead()}>
            全部已读
          </Button>
        </Space>
      }
      content={content}
    >
      <Button type="text" aria-label={n.unread ? `通知（${n.unread} 条未读）` : "通知"}>
        <Badge count={n.unread} size="small" overflowCount={99}>
          <BellOutlined style={{ fontSize: 16 }} />
        </Badge>
      </Button>
    </Popover>
  )
}

const statusText: Record<string, string> = { pending: "等待中", running: "进行中", completed: "已完成", failed: "失败" }

/** 一个后台操作的进度条：已完成为成功，失败为异常，不知道总数时显示动画。 */
export function OperationProgressBar({ op, size }: { op: TrackedOperation; size?: "small" | "default" }): ReactNode {
  const o = op.operation
  const failed = !!op.error || o?.status === "failed"
  const status = failed ? "exception" : o?.status === "completed" ? "success" : "active"
  const percent = op.ratio === undefined ? (op.finished ? 100 : 0) : Math.round(op.ratio * 100)
  return (
    <Progress
      size={size}
      percent={percent}
      status={status}
      format={op.ratio === undefined && !op.finished ? () => <SyncOutlined spin /> : undefined}
    />
  )
}

function describe(op: TrackedOperation): string {
  const o = op.operation
  if (op.error) return "无法读取进度"
  if (!o) return "等待中"
  const parts = [statusText[o.status] ?? o.status]
  if (o.total) parts.push(`${o.processed}/${o.total}`)
  if (o.failed) parts.push(`失败 ${o.failed}`)
  if (o.error) parts.push(o.error)
  return parts.join(" · ")
}

/** 页面中的操作进度：传入 trackOperation 登记过的 ID。 */
export function OperationProgress({ id }: { id: string | undefined }): ReactNode {
  const op = useOperation(id)
  if (!op) return null
  return (
    <div>
      <Space style={{ width: "100%", justifyContent: "space-between" }}>
        <Typography.Text>{op.title}</Typography.Text>
        <Typography.Text type="secondary">{describe(op)}</Typography.Text>
      </Space>
      <OperationProgressBar op={op} />
    </div>
  )
}

/**
 * 任务中心：顶栏上的图标，角标是进行中的操作数，点开是 trackOperation 登记的操作和它们的进度。
 * 没有登记过操作时不显示。放在 actions 插槽：<Fill name="actions"><OperationCenter /></Fill>
 */
export function OperationCenter({ width = 360 }: { width?: number }): ReactNode {
  const app = useApp()
  const ops = useOperations()
  if (ops.length === 0) return null
  const running = ops.filter((o) => !o.finished).length
  return (
    <Popover
      trigger="click"
      placement="bottomRight"
      title="后台任务"
      content={
        <List
          size="small"
          style={{ width, maxHeight: 420, overflow: "auto" }}
          dataSource={[...ops]}
          renderItem={(op) => (
            <List.Item
              key={op.id}
              extra={
                op.finished ? (
                  <Button
                    type="text"
                    size="small"
                    aria-label={`移除 ${op.title}`}
                    icon={<CloseOutlined />}
                    onClick={() => dismissOperation(app, op.id)}
                  />
                ) : undefined
              }
            >
              <List.Item.Meta
                title={op.title}
                description={
                  <>
                    <OperationProgressBar op={op} size="small" />
                    <Typography.Text type="secondary">{describe(op)}</Typography.Text>
                  </>
                }
              />
            </List.Item>
          )}
        />
      }
    >
      <Button type="text" aria-label={running ? `后台任务（${running} 个进行中）` : "后台任务"}>
        <Badge count={running} size="small">
          <SyncOutlined spin={running > 0} style={{ fontSize: 16 }} />
        </Badge>
      </Button>
    </Popover>
  )
}
