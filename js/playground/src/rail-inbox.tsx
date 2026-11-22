// RailLayout 中铺满内容区的模块：收件箱。路由的 staticData.fullBleed 让页面占满内容区，
// <Fill name="nav"> 放模块自己的二级导航（带数量的收件箱列表），打开详情且窗口不够宽时 useCollapseNav 临时收起二级导航。

import { Fill, RailSection, useCollapseNav, useNavControl } from "@go-tenon/ui"
import { ActionIcon, Button, Group, NavLink, ScrollArea, Stack, Text, UnstyledButton } from "@mantine/core"
import { useMediaQuery } from "@mantine/hooks"
import {
  IconAt,
  IconInbox,
  IconLayoutSidebarLeftExpand,
  IconListDetails,
  IconUserQuestion,
  IconX,
} from "@tabler/icons-react"
import { type ReactNode, useState } from "react"

/** 二级导航中的一项：名称和数量。 */
interface Box {
  id: string
  title: string
  count: number
}

const boxes: Box[] = [
  { id: "mine", title: "我的", count: 8 },
  { id: "mentions", title: "@我", count: 3 },
  { id: "unassigned", title: "未指派", count: 5 },
  { id: "all", title: "全部", count: 17 },
]

const teams: Box[] = [
  { id: "front", title: "一线客服", count: 6 },
  { id: "merchants", title: "商家支持", count: 2 },
]

const names = ["Rahul", "Priya", "Anil", "Sneha", "Meera", "Vikram", "Kavya", "Arjun", "Divya", "Rohan"]

const boxIcons: Record<string, ReactNode> = {
  mine: <IconInbox size={16} stroke={1.6} />,
  mentions: <IconAt size={16} stroke={1.6} />,
  unassigned: <IconUserQuestion size={16} stroke={1.6} />,
  all: <IconListDetails size={16} stroke={1.6} />,
}

/** InboxDemo 是收件箱的演示：二级导航、会话列表、会话，以及按需打开的详情，各栏自己滚动。 */
export function InboxDemo(): ReactNode {
  const [box, setBox] = useState("mine")
  const [current, setCurrent] = useState(0)
  const [detail, setDetail] = useState(false)
  const wide = useMediaQuery("(min-width: 1600px)") ?? false
  const nav = useNavControl()
  useCollapseNav(detail && !wide)
  const all = [...boxes, ...teams]
  const selected = all.find((b) => b.id === box) ?? boxes[0]
  const item = (b: Box, leftSection?: ReactNode) => (
    <NavLink
      key={b.id}
      label={b.title}
      leftSection={leftSection}
      active={b.id === box}
      onClick={() => setBox(b.id)}
      rightSection={
        <Text size="xs" c="dimmed">
          {b.count}
        </Text>
      }
    />
  )
  return (
    <div style={{ display: "flex", height: "100%" }}>
      <Fill name="nav">
        {boxes.map((b) => item(b, boxIcons[b.id]))}
        <RailSection title="团队收件箱" collapsible>
          {teams.map((b) =>
            item(
              b,
              <span
                style={{
                  display: "grid",
                  placeItems: "center",
                  width: 18,
                  height: 18,
                  borderRadius: "var(--mantine-radius-xs)",
                  background: "var(--tn-hover)",
                  color: "var(--tn-muted)",
                  fontSize: 11,
                  fontWeight: 600,
                }}
              >
                {b.title.slice(0, 1)}
              </span>,
            ),
          )}
        </RailSection>
      </Fill>
      <div
        style={{
          display: "flex",
          width: 320,
          flexShrink: 0,
          flexDirection: "column",
          borderRight: "1px solid var(--tn-border)",
        }}
      >
        <Group
          gap="xs"
          px="sm"
          wrap="nowrap"
          style={{ height: "var(--tn-rail-header)", flexShrink: 0, borderBottom: "1px solid var(--tn-border)" }}
        >
          {nav && !nav.open && (
            <ActionIcon variant="subtle" color="gray" aria-label="展开二级导航" onClick={nav.openNav}>
              <IconLayoutSidebarLeftExpand size={18} stroke={1.6} />
            </ActionIcon>
          )}
          <Stack gap={0} style={{ minWidth: 0, flex: 1 }}>
            <Text fw={600} size="sm" truncate>
              {selected.title}
            </Text>
            <Text size="xs" c="dimmed" truncate>
              待回复 {selected.count} · 最久 42 分钟
            </Text>
          </Stack>
        </Group>
        <ScrollArea style={{ flex: 1 }}>
          {Array.from({ length: 40 }, (_, i) => (
            <UnstyledButton
              // biome-ignore lint/suspicious/noArrayIndexKey: 演示数据没有 id
              key={i}
              onClick={() => setCurrent(i)}
              p="sm"
              w="100%"
              bg={i === current ? "var(--mantine-primary-color-light)" : undefined}
              style={{ display: "block", borderBottom: "1px solid var(--tn-border)" }}
            >
              <Group justify="space-between" wrap="nowrap">
                <Text size="sm" fw={500}>
                  {names[i % names.length]}
                </Text>
                <Text size="xs" c={i % 3 === 0 ? "deep-orange" : "dimmed"}>
                  {(i * 7) % 60} 分钟
                </Text>
              </Group>
              <Text size="xs" c="dimmed" truncate>
                我的订单还没有到，能帮我查一下吗？
              </Text>
            </UnstyledButton>
          ))}
        </ScrollArea>
      </div>
      <div style={{ display: "flex", flex: 1, minWidth: 0, flexDirection: "column" }}>
        <Group
          justify="space-between"
          px="sm"
          style={{ height: "var(--tn-rail-header)", flexShrink: 0, borderBottom: "1px solid var(--tn-border)" }}
        >
          <Text fw={600}>{names[current % names.length]}</Text>
          <Button size="xs" variant={detail ? "light" : "default"} onClick={() => setDetail(!detail)}>
            详情
          </Button>
        </Group>
        <ScrollArea style={{ flex: 1 }} p="md">
          {Array.from({ length: 30 }, (_, i) => (
            <Group
              // biome-ignore lint/suspicious/noArrayIndexKey: 演示数据没有 id
              key={i}
              justify={i % 2 === 0 ? "flex-start" : "flex-end"}
              mb="sm"
            >
              <Text size="sm" px="sm" py={6} bg="var(--tn-canvas)" style={{ borderRadius: 8 }}>
                {i % 2 === 0 ? "客户的消息" : "坐席的回复"} {i + 1}
              </Text>
            </Group>
          ))}
        </ScrollArea>
        <Text size="sm" c="dimmed" p="sm" style={{ borderTop: "1px solid var(--tn-border)" }}>
          回复框
        </Text>
      </div>
      {detail && (
        <div style={{ width: 300, flexShrink: 0, borderLeft: "1px solid var(--tn-border)" }}>
          <Group
            justify="space-between"
            px="sm"
            style={{ height: "var(--tn-rail-header)", borderBottom: "1px solid var(--tn-border)" }}
          >
            <Text fw={600}>详情</Text>
            <ActionIcon variant="subtle" color="gray" aria-label="关闭详情" onClick={() => setDetail(false)}>
              <IconX size={16} />
            </ActionIcon>
          </Group>
          <Text size="sm" c="dimmed" p="sm">
            窗口宽度小于 1600px 时，打开详情会临时收起二级导航，关闭后恢复。
          </Text>
        </div>
      )}
    </div>
  )
}
