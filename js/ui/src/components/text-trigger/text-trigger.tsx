// 输入框内联触发补全：在 Textarea 或 TextInput 中监听触发前缀（如 @、/、{{），弹出候选项列表，
// 支持键盘上下选择、回车或 Tab 确认、Esc 关闭、输入法防误触，以及精准替换触发区间并恢复光标。

import { Paper, ScrollArea, Text, UnstyledButton } from "@mantine/core"
import {
  type KeyboardEvent,
  type ReactNode,
  type RefObject,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"

/** 一条触发补全规则：定义触发前缀、边界、候选项过滤、渲染方式和选中行为。 */
export interface TextTriggerRule<T = unknown> {
  /** 规则的唯一标识，供 open(ruleId) 命令式打开。 */
  id: string
  /** 触发前缀，例如 "@"、"/"、"{{"。 */
  trigger: string
  /**
   * 触发边界：
   * - "word"（默认）：仅在文本开头或空白字符之后触发，避免网址、邮箱等文本误触发；
   * - "any"：任意位置触发，适合在句子中间输入 "{{" 插入变量。
   */
  boundary?: "word" | "any"
  /** 是否允许关键词中包含空格，默认 false。 */
  allowSpace?: boolean
  /** 终止标记：触发前缀之后出现该串时不再视为当前触发，例如 "{{" 配 stop: "}}"。 */
  stop?: string
  /** 是否启用该规则，默认 true。 */
  enabled?: boolean
  /** 根据触发前缀后的输入返回候选项列表。 */
  items: (query: string) => T[]
  /** 候选项的稳定键。 */
  getKey: (item: T) => string
  /** 渲染单个候选项。 */
  renderItem: (item: T, active: boolean) => ReactNode
  /** 选中候选项时的回调；调用 replace(text) 会替换触发区间并将光标移至插入内容末尾。 */
  onSelect: (item: T, helpers: TextTriggerSelectHelpers) => void
}

/** 选中候选项时传给 onSelect 的辅助操作。 */
export interface TextTriggerSelectHelpers {
  /** 用 text 替换当前触发区间（命令式打开时在光标处插入），并恢复输入框焦点与光标位置。 */
  replace: (text: string) => void
  /** 关闭候选浮层。 */
  close: () => void
}

/**
 * textTriggerRule 构造一条类型安全的触发规则，便于把不同候选项类型的多条规则放进同一个数组，
 * 调用方无需手写类型参数即可在 items、getKey、renderItem、onSelect 中获得完整类型推导。
 */
// biome-ignore lint/suspicious/noExplicitAny: 抹除异构规则数组的单项类型参数
export function textTriggerRule<T>(rule: TextTriggerRule<T>): TextTriggerRule<any> {
  return rule
}

/** 一次光标触发匹配的结果：命中的规则标识、在原文中的起止位置和关键词。 */
export interface TextTriggerMatch {
  ruleId: string
  start: number
  end: number
  query: string
}

/** 区间替换的结果：新文本和替换后的光标位置。 */
export interface TriggerReplacement {
  value: string
  caret: number
}

/** matchTextTrigger 从 value 的光标位置 caret 往前匹配最靠近光标的触发规则；未命中时返回 null。 */
export function matchTextTrigger(
  value: string,
  caret: number,
  // biome-ignore lint/suspicious/noExplicitAny: 规则列表中各条规则的候选项类型可以不同
  rules: readonly TextTriggerRule<any>[],
): TextTriggerMatch | null {
  if (caret < 0 || caret > value.length) return null
  const before = value.slice(0, caret)
  let best: (TextTriggerMatch & { triggerLen: number }) | null = null

  for (const rule of rules) {
    if (rule.enabled === false || !rule.trigger) continue
    const idx = before.lastIndexOf(rule.trigger)
    if (idx < 0) continue

    const boundary = rule.boundary ?? "word"
    if (boundary === "word" && idx > 0 && !/\s/.test(before[idx - 1])) continue

    const query = before.slice(idx + rule.trigger.length)
    if (query.includes("\n") || query.includes("\r")) continue
    if (!rule.allowSpace && /\s/.test(query)) continue
    if (rule.stop && query.includes(rule.stop)) continue

    if (!best || idx > best.start || (idx === best.start && rule.trigger.length > best.triggerLen)) {
      best = { ruleId: rule.id, start: idx, end: caret, query, triggerLen: rule.trigger.length }
    }
  }

  return best ? { ruleId: best.ruleId, start: best.start, end: best.end, query: best.query } : null
}

/** replaceTriggerRange 把 value 中 [start, end) 区间替换为 replacement，返回新文本和新的光标位置。 */
export function replaceTriggerRange(
  value: string,
  start: number,
  end: number,
  replacement: string,
): TriggerReplacement {
  const safeStart = Math.max(0, Math.min(start, value.length))
  const safeEnd = Math.max(safeStart, Math.min(end, value.length))
  return {
    value: value.slice(0, safeStart) + replacement + value.slice(safeEnd),
    caret: safeStart + replacement.length,
  }
}

/** useTextTrigger 的选项。 */
export interface UseTextTriggerOptions {
  /** 输入框或文本域的 ref，用于读取和恢复光标位置。 */
  ref: RefObject<HTMLTextAreaElement | HTMLInputElement | null>
  /** 当前输入值。 */
  value: string
  /** 更新输入值的回调。 */
  onChange: (next: string) => void
  /** 触发规则列表；每条规则可有独立的触发前缀和候选项类型。 */
  // biome-ignore lint/suspicious/noExplicitAny: 规则列表中各条规则的候选项类型可以不同
  rules: readonly TextTriggerRule<any>[]
}

/** useTextTrigger 返回的状态与事件处理函数，同时交给输入框和 TriggerMenu。 */
export interface TextTriggerState {
  /** 浮层是否打开（有候选项，或由 open() 命令式打开）。 */
  opened: boolean
  /** 是否由 open() 命令式打开。 */
  manual: boolean
  /** 当前命中的规则。 */
  // biome-ignore lint/suspicious/noExplicitAny: 与规则列表一致
  activeRule?: TextTriggerRule<any>
  /** 当前输入的过滤关键词。 */
  query: string
  /** 当前展示的候选项列表。 */
  items: unknown[]
  /** 当前键盘高亮项的下标。 */
  activeIndex: number
  /** 设置当前高亮项下标（例如鼠标悬停时）。 */
  setActiveIndex: (index: number) => void
  /** 绑定到输入框 onChange：更新输入值并按光标位置检测触发。 */
  handleChange: (next: string, caret: number | null) => void
  /**
   * 绑定到输入框 onKeyDown：处理上下方向键、回车、Tab 和 Esc；忽略输入法组词态（isComposing）。
   * 返回 true 表示事件已被补全菜单消费，宿主应直接 return。
   */
  handleKeyDown: (e: KeyboardEvent) => boolean
  /** 选中指定下标（默认当前高亮项）的候选项。 */
  select: (index?: number) => void
  /** 命令式打开某条规则（例如点击工具栏按钮），选中后在当前光标位置插入内容。 */
  open: (ruleId: string) => void
  /** 关闭候选浮层。 */
  close: () => void
}

interface InternalMatch extends TextTriggerMatch {
  manual: boolean
}

/**
 * useTextTrigger 管理输入框的内联触发补全状态（匹配、过滤、键盘导航和精准区间替换）。
 * 搭配 TriggerMenu 使用，支持同时注册多条触发规则（如 @、/、{{）。
 */
export function useTextTrigger({ ref, value, onChange, rules }: UseTextTriggerOptions): TextTriggerState {
  const [match, setMatch] = useState<InternalMatch | null>(null)
  const [activeIndex, setActiveIndex] = useState(0)
  const valueRef = useRef(value)
  valueRef.current = value
  const rulesRef = useRef(rules)
  rulesRef.current = rules

  const activeRule = useMemo(
    () => (match ? rules.find((r) => r.id === match.ruleId && r.enabled !== false) : undefined),
    [match, rules],
  )

  const items = useMemo(() => {
    if (!match || !activeRule) return []
    return activeRule.items(match.query)
  }, [match, activeRule])

  // 规则被禁用或文本被外部重置（超出区间）时自动收起浮层。
  useEffect(() => {
    if (!match) return
    if (!activeRule || match.end > value.length) {
      setMatch(null)
    }
  }, [match, activeRule, value])

  // 候选数量缩短时修正高亮下标。
  const clampedIndex = items.length > 0 ? Math.min(activeIndex, items.length - 1) : 0

  const close = useCallback(() => {
    setMatch(null)
    setActiveIndex(0)
  }, [])

  const open = useCallback(
    (ruleId: string) => {
      const rule = rulesRef.current.find((r) => r.id === ruleId && r.enabled !== false)
      if (!rule) return
      const el = ref.current
      const caret = el?.selectionStart ?? valueRef.current.length
      setMatch({ ruleId: rule.id, start: caret, end: caret, query: "", manual: true })
      setActiveIndex(0)
      el?.focus()
    },
    [ref],
  )

  const handleChange = useCallback(
    (next: string, caret: number | null) => {
      onChange(next)
      const pos = caret ?? next.length
      const matched = matchTextTrigger(next, pos, rulesRef.current)
      setMatch(matched ? { ...matched, manual: false } : null)
      setActiveIndex(0)
    },
    [onChange],
  )

  const select = useCallback(
    (index?: number) => {
      if (!match || !activeRule) return
      const target = index ?? clampedIndex
      if (target < 0 || target >= items.length) return
      const item = items[target]
      const { start, end } = match

      const replace = (replacement: string) => {
        const res = replaceTriggerRange(valueRef.current, start, end, replacement)
        onChange(res.value)
        setMatch(null)
        setActiveIndex(0)
        requestAnimationFrame(() => {
          const el = ref.current
          if (!el) return
          el.focus()
          el.setSelectionRange(res.caret, res.caret)
        })
      }

      activeRule.onSelect(item, { replace, close })
    },
    [match, activeRule, clampedIndex, items, onChange, ref, close],
  )

  const opened = Boolean(match && activeRule && (items.length > 0 || match.manual))

  const handleKeyDown = useCallback(
    (e: KeyboardEvent): boolean => {
      if (e.nativeEvent.isComposing || !opened) return false
      if (e.key === "Escape") {
        e.preventDefault()
        close()
        return true
      }
      if (items.length === 0) return false
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault()
        setActiveIndex((prev) => {
          const cur = Math.min(prev, items.length - 1)
          return (cur + (e.key === "ArrowDown" ? 1 : items.length - 1)) % items.length
        })
        return true
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault()
        select(clampedIndex)
        return true
      }
      return false
    },
    [opened, items.length, close, select, clampedIndex],
  )

  return {
    opened,
    manual: match?.manual ?? false,
    activeRule,
    query: match?.query ?? "",
    items,
    activeIndex: clampedIndex,
    setActiveIndex,
    handleChange,
    handleKeyDown,
    select,
    open,
    close,
  }
}

/** TriggerMenu 的选项。 */
export interface TriggerMenuProps {
  /** useTextTrigger 返回的状态对象。 */
  trigger: TextTriggerState
  /** 浮层相对输入框容器的位置：top 向上弹出（默认，适合底部回复框），bottom 向下弹出（适合普通表单）。 */
  position?: "top" | "bottom"
  /** 浮层宽度，默认 280。 */
  width?: number | string
  /** 列表区最大高度（像素），默认 240。 */
  maxHeight?: number
  /** 顶部标题或说明，可选。 */
  header?: ReactNode
  /** 无匹配项时的空状态内容（仅在命令式打开或明确传入时展示）。 */
  empty?: ReactNode
}

/**
 * TriggerMenu 是配合 useTextTrigger 使用的候选浮层，需放在 position: relative 的输入框容器内。
 * 点击候选项时阻止输入框失焦，键盘切换时自动将高亮项滚入可视区。
 */
export function TriggerMenu({
  trigger,
  position = "top",
  width = 280,
  maxHeight = 240,
  header,
  empty,
}: TriggerMenuProps): ReactNode {
  const menuRef = useRef<HTMLDivElement>(null)
  const itemRefs = useRef<(HTMLButtonElement | null)[]>([])

  // 键盘移动高亮项时确保其位于滚动可视范围内。
  useEffect(() => {
    if (!trigger.opened) return
    itemRefs.current[trigger.activeIndex]?.scrollIntoView({ block: "nearest" })
  }, [trigger.opened, trigger.activeIndex])

  // 点击浮层外部时关闭（不阻止默认事件，保证目标元素正常获得焦点）。
  useEffect(() => {
    if (!trigger.opened) return
    const onPointerDown = (e: PointerEvent) => {
      const target = e.target as Node | null
      if (target && menuRef.current?.contains(target)) return
      trigger.close()
    }
    document.addEventListener("pointerdown", onPointerDown)
    return () => document.removeEventListener("pointerdown", onPointerDown)
  }, [trigger.opened, trigger.close])

  if (!trigger.opened || !trigger.activeRule) return null
  if (trigger.items.length === 0 && !empty) return null

  const rule = trigger.activeRule
  return (
    <Paper
      ref={menuRef}
      withBorder
      shadow="md"
      radius="md"
      p={4}
      w={width}
      className="tn-trigger-menu"
      data-position={position}
      role="listbox"
    >
      {header && <div className="tn-trigger-header">{header}</div>}
      {trigger.items.length === 0 ? (
        <Text size="xs" c="dimmed" px="xs" py="sm" ta="center">
          {empty}
        </Text>
      ) : (
        <ScrollArea.Autosize mah={maxHeight} type="auto">
          {trigger.items.map((item, i) => {
            const active = i === trigger.activeIndex
            return (
              <UnstyledButton
                key={rule.getKey(item)}
                ref={(el) => {
                  itemRefs.current[i] = el
                }}
                w="100%"
                className="tn-trigger-item"
                data-active={active || undefined}
                role="option"
                aria-selected={active}
                onMouseEnter={() => trigger.setActiveIndex(i)}
                onMouseDown={(e) => {
                  // 阻止输入框失焦，保证 replace 时焦点与选区不中断。
                  e.preventDefault()
                  trigger.select(i)
                }}
              >
                {rule.renderItem(item, active)}
              </UnstyledButton>
            )
          })}
        </ScrollArea.Autosize>
      )}
    </Paper>
  )
}
