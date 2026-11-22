// RailLayout 中铺满内容区的模块：收件箱。路由的 staticData.fullBleed 让页面占满内容区，
// <Fill name="nav"> 放模块自己的二级导航（带数量的收件箱列表），打开详情且窗口不够宽时 useCollapseNav 临时收起二级导航。

import {
  Fill,
  RailSection,
  TriggerMenu,
  textTriggerRule,
  useCollapseNav,
  useNavControl,
  useSavedView,
  useTextTrigger,
} from "@go-tenon/ui"
import {
  ActionIcon,
  Badge,
  Button,
  Group,
  NavLink,
  ScrollArea,
  SegmentedControl,
  Stack,
  Text,
  Textarea,
  Tooltip,
  UnstyledButton,
} from "@mantine/core"
import { useMediaQuery } from "@mantine/hooks"
import {
  IconAt,
  IconBolt,
  IconBraces,
  IconFilter,
  IconInbox,
  IconLayoutSidebarLeftExpand,
  IconListDetails,
  IconPlus,
  IconUserQuestion,
  IconX,
} from "@tabler/icons-react"
import { type ReactNode, useMemo, useRef, useState } from "react"

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

interface DemoView extends Box {
  priority: "all" | "high"
}

const initialViews: DemoView[] = [
  { id: "view-high", title: "高优待回复", count: 4, priority: "high" },
  { id: "view-all-open", title: "全部进行中", count: 12, priority: "all" },
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
  const [views, setViews] = useState<DemoView[]>(initialViews)
  const [priority, setPriority] = useState<"all" | "high">("all")
  const [current, setCurrent] = useState(0)
  const [detail, setDetail] = useState(false)
  const wide = useMediaQuery("(min-width: 1600px)") ?? false
  const nav = useNavControl()
  useCollapseNav(detail && !wide)
  const all = [...boxes, ...teams, ...views]
  const activeView = views.find((v) => v.id === box)
  const savedState = useSavedView(activeView ? { priority: activeView.priority } : undefined, { priority })
  const selected = all.find((b) => b.id === box) ?? boxes[0]
  const selectBox = (b: Box) => {
    setBox(b.id)
    const v = views.find((x) => x.id === b.id)
    if (v) setPriority(v.priority)
  }
  const addView = () => {
    const id = `view-${views.length + 1}`
    const next: DemoView = { id, title: `自定义视图 ${views.length + 1}`, count: 3, priority }
    setViews((cur) => [...cur, next])
    setBox(id)
  }
  const item = (b: Box, leftSection?: ReactNode) => (
    <NavLink
      key={b.id}
      label={b.title}
      leftSection={leftSection}
      active={b.id === box}
      onClick={() => selectBox(b)}
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
        <RailSection title="团队收件箱" collapsible storeKey="demo-inbox-teams">
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
        <RailSection
          title="视图"
          collapsible
          storeKey="demo-inbox-views"
          action={
            <Tooltip label="新建视图" openDelay={300}>
              <ActionIcon size="xs" variant="subtle" color="gray" aria-label="新建视图" onClick={addView}>
                <IconPlus size={14} stroke={1.8} />
              </ActionIcon>
            </Tooltip>
          }
        >
          {views.map((v) => item(v, <IconFilter size={15} stroke={1.6} />))}
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
        {activeView && (
          <Stack gap={6} px="sm" py={8} style={{ borderBottom: "1px solid var(--tn-border)" }}>
            <SegmentedControl
              size="xs"
              fullWidth
              value={priority}
              onChange={(v) => setPriority(v as "all" | "high")}
              data={[
                { value: "all", label: "全部优先级" },
                { value: "high", label: "高优先级" },
              ]}
            />
            {savedState.dirty && (
              <Group gap="xs" justify="space-between">
                <Text size="xs" c="deep-orange">
                  筛选已修改
                </Text>
                <Group gap={4}>
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    color="gray"
                    onClick={() => setPriority(activeView.priority)}
                  >
                    重置
                  </Button>
                  <Button
                    size="compact-xs"
                    onClick={() => setViews((cur) => cur.map((v) => (v.id === activeView.id ? { ...v, priority } : v)))}
                  >
                    保存
                  </Button>
                </Group>
              </Group>
            )}
          </Stack>
        )}
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
        <InboxComposer contactName={names[current % names.length]} />
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

const demoMacros = [
  { id: "m1", shortcut: "greeting", title: "欢迎语", body: "您好 {{contact.name}}，请问有什么可以帮您？" },
  { id: "m2", shortcut: "shipping", title: "查询物流", body: "正在为您核实物流进度，请稍候片刻。" },
  { id: "m3", shortcut: "refund", title: "退款说明", body: "退款已原路退回，预计 1-3 个工作日到账。" },
]

const demoMembers = [
  { id: "u1", name: "韩梅梅", role: "管理员" },
  { id: "u2", name: "李雷", role: "一线坐席" },
  { id: "u3", name: "林涛", role: "技术支持" },
]

const demoVars = [
  { key: "contact.name", label: "联系人名字" },
  { key: "contact.email", label: "联系人邮箱" },
  { key: "member.name", label: "当前坐席名字" },
]

/** InboxComposer 演示 useTextTrigger + TriggerMenu：同一输入框内同时支持 / 快捷回复、@ 提及同事和 {{ 插入变量。 */
function InboxComposer({ contactName }: { contactName: string }): ReactNode {
  const ref = useRef<HTMLTextAreaElement>(null)
  const [text, setText] = useState("")
  const rules = useMemo(
    () => [
      textTriggerRule({
        id: "macro",
        trigger: "/",
        items: (q) => {
          const s = q.toLowerCase()
          return demoMacros.filter(
            (m) => m.shortcut.includes(s) || m.title.toLowerCase().includes(s) || m.body.toLowerCase().includes(s),
          )
        },
        getKey: (m) => m.id,
        renderItem: (m) => (
          <Stack gap={2}>
            <Group gap="xs" wrap="nowrap">
              <Text size="sm" fw={500} truncate>
                {m.title}
              </Text>
              <Badge size="xs" variant="light">
                /{m.shortcut}
              </Badge>
            </Group>
            <Text size="xs" c="dimmed" truncate>
              {m.body}
            </Text>
          </Stack>
        ),
        onSelect: (m, { replace }) => replace(m.body.replace("{{contact.name}}", contactName)),
      }),
      textTriggerRule({
        id: "mention",
        trigger: "@",
        items: (q) => demoMembers.filter((m) => m.name.toLowerCase().includes(q.toLowerCase())),
        getKey: (m) => m.id,
        renderItem: (m) => (
          <Group justify="space-between" wrap="nowrap">
            <Text size="sm">{m.name}</Text>
            <Text size="xs" c="dimmed">
              {m.role}
            </Text>
          </Group>
        ),
        onSelect: (m, { replace }) => replace(`@${m.name} `),
      }),
      textTriggerRule({
        id: "var",
        trigger: "{{",
        boundary: "any",
        stop: "}}",
        items: (q) =>
          demoVars.filter((v) => v.key.includes(q.toLowerCase()) || v.label.toLowerCase().includes(q.toLowerCase())),
        getKey: (v) => v.key,
        renderItem: (v) => (
          <Group justify="space-between" wrap="nowrap">
            <Text size="sm" ff="monospace">
              {`{{${v.key}}}`}
            </Text>
            <Text size="xs" c="dimmed">
              {v.label}
            </Text>
          </Group>
        ),
        onSelect: (v, { replace }) => replace(`{{${v.key}}}`),
      }),
    ],
    [contactName],
  )
  const trigger = useTextTrigger({ ref, value: text, onChange: setText, rules })
  return (
    <div style={{ padding: 12, borderTop: "1px solid var(--tn-border)" }}>
      <div style={{ position: "relative" }}>
        <TriggerMenu trigger={trigger} position="top" width={320} empty="没有匹配项" />
        <Textarea
          ref={ref}
          value={text}
          onChange={(e) => trigger.handleChange(e.currentTarget.value, e.currentTarget.selectionStart)}
          onKeyDown={(e) => {
            if (trigger.handleKeyDown(e)) return
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault()
              setText("")
            }
          }}
          placeholder="输入 / 选快捷回复，@ 提及同事，{{ 插入变量…"
          autosize
          minRows={2}
          maxRows={5}
        />
      </div>
      <Group gap="xs" mt="xs">
        <Button size="xs" variant="subtle" leftSection={<IconBolt size={14} />} onClick={() => trigger.open("macro")}>
          快捷回复
        </Button>
        <Button size="xs" variant="subtle" leftSection={<IconBraces size={14} />} onClick={() => trigger.open("var")}>
          插入变量
        </Button>
        <Text size="xs" c="dimmed" ml="auto">
          ↑↓ 选择 · Enter 确认 · Esc 关闭
        </Text>
      </Group>
    </div>
  )
}
