// 表格单元格中的长文本：只显示一行，被截断时桌面悬停或触屏点击弹出全文。

import { Box, Popover, Text } from "@mantine/core"
import { useElementSize } from "@mantine/hooks"
import { type ReactNode, useEffect, useLayoutEffect, useRef, useState } from "react"

/** cardPadding 是弹出卡片左右的内边距，px。 */
const cardPadding = 12

/** TruncatedText 的属性。 */
export interface TruncatedTextProps {
  /** 要显示的文字。 */
  value: string
  /** 文字撑开列宽的上限，px，默认 320。表格把多出的宽度分给这一列时，文字铺满实际的列宽。 */
  maxWidth?: number
  /** 列变窄时的下限，px，默认 120；文字比它短时按文字本身的宽度。 */
  minWidth?: number
}

/**
 * TruncatedText 是表格单元格中的长文本：只显示一行，铺满这一列实际的宽度，超出的部分用省略号代替。
 * 文字被截断时，桌面端鼠标悬停或移动端点击在上方弹出全文（上方空间不够时在下方），卡片中文字的宽度和左右位置与单元格一致；
 * 弹出后可选中文字复制，在卡片中点击不会触发表格行的点击。卡片的颜色与主题中的 Tooltip 相同。
 *
 * ResourcePage 中文字类型（kind 为 text）的列自动使用它；自己写的列中直接使用：
 *
 *	{ accessor: "description", title: "说明", render: (row) => <TruncatedText value={row.description} /> }
 *
 * 列宽由两份看不见的文字决定，短的文字按本身的宽度，所以短文字不会把列撑宽：
 *   - 第一份最宽 maxWidth，可以在任意字符处换行、高度固定为一行，它给出列合适的宽度，并允许列变窄
 *     （窗口变窄、拖动表头的边缘）；
 *   - 第二份不换行、最宽 minWidth、高度为 0，它给出列变窄时的下限。
 * 看得见的文字绝对定位、铺满单元格，因此省略号出现在列的右边缘；列宽变化时重新判断是否截断。
 */
export function TruncatedText({ value, maxWidth = 320, minWidth = 120 }: TruncatedTextProps): ReactNode {
  const { ref, width } = useElementSize()
  const text = useRef<HTMLParagraphElement>(null)
  const [truncated, setTruncated] = useState(false)
  const [opened, setOpened] = useState(false)
  const timer = useRef<number | undefined>(undefined)

  const clearTimer = () => {
    if (timer.current !== undefined) {
      window.clearTimeout(timer.current)
      timer.current = undefined
    }
  }
  useEffect(
    () => () => {
      if (timer.current !== undefined) window.clearTimeout(timer.current)
    },
    [],
  )

  // biome-ignore lint/correctness/useExhaustiveDependencies: 列宽或文字变化时重新判断是否截断
  useLayoutEffect(() => {
    const el = text.current
    if (el) setTruncated(el.scrollWidth > el.clientWidth)
  }, [width, value])

  const openLater = () => {
    if (!truncated) return
    clearTimer()
    timer.current = window.setTimeout(() => setOpened(true), 300)
  }
  const closeLater = () => {
    clearTimer()
    timer.current = window.setTimeout(() => setOpened(false), 200)
  }

  return (
    <Popover
      opened={truncated && opened}
      onChange={setOpened}
      disabled={!truncated}
      width={Math.max(width + 2 * cardPadding, 180)}
      offset={{ mainAxis: 8, crossAxis: -cardPadding }}
      position="top-start"
      shadow="md"
    >
      <Popover.Target>
        <Box
          ref={ref}
          pos="relative"
          style={truncated ? { cursor: "pointer" } : undefined}
          onMouseEnter={openLater}
          onMouseLeave={closeLater}
          onClick={(e) => {
            if (!truncated) return
            e.stopPropagation()
            clearTimer()
            setOpened((o) => !o)
          }}
        >
          <Text
            span
            size="sm"
            display="block"
            maw={maxWidth}
            aria-hidden
            style={{ visibility: "hidden", overflowWrap: "anywhere", height: "1lh", overflow: "hidden" }}
          >
            {value}
          </Text>
          <Text
            span
            size="sm"
            display="block"
            maw={minWidth}
            h={0}
            aria-hidden
            style={{ visibility: "hidden", whiteSpace: "nowrap", overflow: "hidden" }}
          >
            {value}
          </Text>
          <Text ref={text} span size="sm" pos="absolute" inset={0} truncate="end">
            {value}
          </Text>
        </Box>
      </Popover.Target>
      {/* 卡片渲染在 portal 中，点击事件仍会经过 React 树冒泡到表格的行；在这里拦住，选中文字时不触发行的点击。 */}
      <Popover.Dropdown
        bg="var(--mantine-color-dark-filled)"
        bd="none"
        px={cardPadding}
        maw="calc(100vw - 24px)"
        onMouseEnter={clearTimer}
        onMouseLeave={closeLater}
        onClick={(e) => e.stopPropagation()}
      >
        <Text size="sm" c="white" style={{ whiteSpace: "pre-wrap", wordBreak: "break-word", userSelect: "text" }}>
          {value}
        </Text>
      </Popover.Dropdown>
    </Popover>
  )
}
