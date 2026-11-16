// AI 对话的外观：按 shadcn/ui 的 AI Elements（Chat、Message、Reasoning、Tool、Confirmation、Prompt Input）组合。
// 逻辑在 @go-tenon/shell/chat：消息流、历史、断线续传、停止、插话和审批。
// 用到时安装 AI SDK：bun add ai @ai-sdk/react

import { describeError } from "@go-tenon/client"
import {
  type AssistantOptions,
  type PendingApproval,
  useAssistantChat,
  useAssistantHistory,
} from "@go-tenon/shell/chat"
import type { UIMessage } from "ai"
import { ArrowUp, Bot, CircleCheck, CircleX, LoaderCircle, Square, Wrench } from "lucide-react"
import { type FormEvent, type KeyboardEvent, type ReactNode, useState } from "react"
import { Markdown } from "@/components/tenon/markdown"
import { Bubble, BubbleContent } from "@/components/ui/bubble"
import { Button } from "@/components/ui/button"
import { CodeBlock, CodeBlockContent } from "@/components/ui/code-block"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupTextarea } from "@/components/ui/input-group"
import { Item, ItemActions, ItemContent, ItemDescription, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Message, MessageContent } from "@/components/ui/message"
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport,
} from "@/components/ui/message-scroller"
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Trace,
  TraceCollapsible,
  TraceCollapsibleContent,
  TraceCollapsibleTrigger,
  TraceIcon,
  TraceLine,
  TraceTitle,
} from "@/components/ui/trace"
import { cn } from "@/lib/utils"

/** AssistantChat 的选项。 */
export interface AssistantChatProps extends AssistantOptions {
  /** 还没有消息时显示的标题。 */
  welcome?: ReactNode
  placeholder?: string
  /** 工具的显示名称，例如 { refund_order: "退款" }；没有写的显示工具名。 */
  toolTitles?: Record<string, string>
  /** 渲染一条消息中的其他 part（例如应用自己的 data-* 片段）；返回 undefined 时不显示。 */
  renderPart?: (part: Part, message: UIMessage) => ReactNode
  className?: string
}

type Part = UIMessage["parts"][number]
type ToolPart = {
  type: string
  toolName?: string
  toolCallId: string
  state: string
  input?: unknown
  output?: unknown
  errorText?: string
}

const isTool = (p: Part): p is Part & ToolPart => p.type.startsWith("tool-") || p.type === "dynamic-tool"
const toolName = (p: ToolPart) => p.toolName ?? p.type.slice("tool-".length)
const json = (v: unknown) => (v === undefined ? "" : JSON.stringify(v, null, 2))

/** 思考过程：生成时展开，结束后收起；用户展开或收起后以用户的选择为准。 */
function ReasoningLine({ text, running }: { text: string; running: boolean }): ReactNode {
  const [open, setOpen] = useState<boolean>()
  const line = (
    <TraceLine status={running ? "running" : undefined}>
      <TraceIcon>{running ? <LoaderCircle className="animate-spin" /> : <CircleCheck />}</TraceIcon>
      <TraceTitle>{running ? "思考中" : "思考过程"}</TraceTitle>
    </TraceLine>
  )
  if (!text) return line
  return (
    <TraceCollapsible open={open ?? running} onOpenChange={setOpen}>
      <TraceCollapsibleTrigger>{line}</TraceCollapsibleTrigger>
      <TraceCollapsibleContent className="whitespace-pre-wrap">{text}</TraceCollapsibleContent>
    </TraceCollapsible>
  )
}

/** 一次工具调用：状态、参数和结果。 */
function ToolLine({ part, title }: { part: ToolPart; title: string }): ReactNode {
  const error = part.state === "output-error"
  const denied = part.state === "output-denied"
  const done = part.state === "output-available"
  const status = error
    ? "error"
    : done || denied
      ? undefined
      : part.state === "approval-requested"
        ? "pending"
        : "running"
  const detail = [
    json(part.input),
    part.output !== undefined ? `→ ${json(part.output)}` : "",
    part.errorText ? `→ ${part.errorText}` : "",
  ]
    .filter(Boolean)
    .join("\n")
  return (
    <TraceCollapsible>
      <TraceCollapsibleTrigger>
        <TraceLine status={status}>
          <TraceIcon>
            {error || denied ? (
              <CircleX />
            ) : status === "running" ? (
              <LoaderCircle className="animate-spin" />
            ) : (
              <Wrench />
            )}
          </TraceIcon>
          <TraceTitle>
            {title}
            {denied ? "（已拒绝）" : ""}
          </TraceTitle>
        </TraceLine>
      </TraceCollapsibleTrigger>
      <TraceCollapsibleContent>
        <CodeBlock size="sm">
          <CodeBlockContent wrap className="max-h-64">
            {detail}
          </CodeBlockContent>
        </CodeBlock>
      </TraceCollapsibleContent>
    </TraceCollapsible>
  )
}

/** 一条回答中的 parts：思考过程和工具调用在 Trace 中，文字用 Markdown 渲染。 */
function AssistantTurn({
  parts,
  message,
  streaming,
  toolTitles,
  renderPart,
}: {
  parts: Part[]
  message: UIMessage
  streaming: boolean
  toolTitles: Record<string, string>
  renderPart?: AssistantChatProps["renderPart"]
}): ReactNode {
  const out: ReactNode[] = []
  let trace: ReactNode[] = []
  const flush = () => {
    if (trace.length) out.push(<Trace key={`trace-${out.length}`}>{trace}</Trace>)
    trace = []
  }
  // parts 只会在末尾追加，用位置作为 key 是稳定的。
  for (let i = 0; i < parts.length; i++) {
    const p = parts[i] as Part
    const key = `part-${i}`
    const last = streaming && i === parts.length - 1
    if (isTool(p)) {
      trace.push(<ToolLine key={key} part={p} title={toolTitles[toolName(p)] ?? toolName(p)} />)
      continue
    }
    if (p.type === "reasoning") {
      trace.push(<ReasoningLine key={key} text={p.text} running={last && p.state === "streaming"} />)
      continue
    }
    flush()
    if (p.type === "text") {
      out.push(
        <Markdown key={key} streaming={last}>
          {p.text}
        </Markdown>,
      )
    } else if (p.type !== "step-start") {
      const node = renderPart?.(p, message)
      if (node !== undefined) out.push(<div key={key}>{node}</div>)
    }
  }
  flush()
  return (
    <Message>
      <MessageContent className="gap-2">{out}</MessageContent>
    </Message>
  )
}

type Turn = { key: string; role: "user" | "assistant"; message: UIMessage; parts: Part[] }

/** 把消息拆成轮次：回答中的插话（data-steering）显示为单独的用户消息，插话前后的回答分开。 */
export function toTurns(messages: UIMessage[]): Turn[] {
  const out: Turn[] = []
  for (const m of messages) {
    if (m.role !== "assistant") {
      out.push({ key: m.id, role: "user", message: m, parts: m.parts })
      continue
    }
    let cur: Part[] = []
    let n = 0
    const flush = () => {
      if (cur.length) out.push({ key: `${m.id}:${n++}`, role: "assistant", message: m, parts: cur })
      cur = []
    }
    for (const p of m.parts) {
      if (p.type === "data-steering") {
        flush()
        const text = (p as { data?: { text?: string } }).data?.text ?? ""
        out.push({ key: `${m.id}:${n++}`, role: "user", message: m, parts: [{ type: "text", text }] })
      } else {
        cur.push(p)
      }
    }
    flush()
  }
  return out
}

const textOf = (parts: Part[]) =>
  parts
    .filter((p) => p.type === "text")
    .map((p) => (p as { text: string }).text)
    .join("")

/** 等待审批的工具调用：显示参数，批准或拒绝。 */
function Confirmations({
  pending,
  respond,
  toolTitles,
}: {
  pending: PendingApproval[]
  respond: (id: string, approved: boolean) => void
  toolTitles: Record<string, string>
}): ReactNode {
  return pending.map((p) => (
    <Item key={p.id} variant="outline" size="sm">
      <ItemMedia variant="icon">
        <Wrench />
      </ItemMedia>
      <ItemContent>
        <ItemTitle>需要你确认：{toolTitles[p.toolName] ?? p.toolName}</ItemTitle>
        <ItemDescription className="line-clamp-none font-mono text-xs whitespace-pre-wrap">
          {json(p.input)}
        </ItemDescription>
      </ItemContent>
      <ItemActions>
        <Button size="sm" variant="outline" onClick={() => respond(p.id, false)}>
          拒绝
        </Button>
        <Button size="sm" onClick={() => respond(p.id, true)}>
          批准
        </Button>
      </ItemActions>
    </Item>
  ))
}

function Conversation({
  messages,
  welcome = "有什么可以帮你？",
  placeholder = "输入消息，Enter 发送",
  toolTitles = {},
  renderPart,
  className,
  ...options
}: AssistantChatProps & { messages: UIMessage[] }): ReactNode {
  const chat = useAssistantChat({ ...options, messages })
  const [text, setText] = useState("")
  const [error, setError] = useState<unknown>()
  const turns = toTurns(chat.messages)
  const streamingKey = chat.status === "streaming" ? turns[turns.length - 1]?.key : undefined
  const awaiting = chat.pending.length > 0

  const submit = async (e?: FormEvent) => {
    e?.preventDefault()
    const value = text
    if (!value.trim() || awaiting) return
    setText("")
    setError(undefined)
    try {
      await chat.send(value)
    } catch (err) {
      setError(err)
      setText(value)
    }
  }
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      void submit()
    }
  }
  const failure = error ?? chat.error
  return (
    <div className={cn("flex h-full min-h-0 flex-col gap-3", className)}>
      {turns.length === 0 ? (
        <Empty className="flex-1">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Bot />
            </EmptyMedia>
            <EmptyTitle>{welcome}</EmptyTitle>
            <EmptyDescription>回答进行中也可以继续输入，消息会插入这次回答。</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <MessageScrollerProvider autoScroll>
          <MessageScroller className="min-h-0 flex-1">
            <MessageScrollerViewport>
              <MessageScrollerContent className="gap-4">
                {turns.map((t) => (
                  <MessageScrollerItem key={t.key} messageId={t.key} scrollAnchor={t.role === "user"}>
                    {t.role === "user" ? (
                      <Message align="end">
                        <MessageContent>
                          <Bubble align="end" variant="muted">
                            <BubbleContent className="whitespace-pre-wrap">{textOf(t.parts)}</BubbleContent>
                          </Bubble>
                        </MessageContent>
                      </Message>
                    ) : (
                      <AssistantTurn
                        parts={t.parts}
                        message={t.message}
                        streaming={t.key === streamingKey}
                        toolTitles={toolTitles}
                        renderPart={renderPart}
                      />
                    )}
                  </MessageScrollerItem>
                ))}
                {chat.status === "submitted" && (
                  <Message>
                    <MessageContent>
                      <Skeleton className="h-4 w-24" />
                    </MessageContent>
                  </Message>
                )}
              </MessageScrollerContent>
            </MessageScrollerViewport>
            <MessageScrollerButton />
          </MessageScroller>
        </MessageScrollerProvider>
      )}
      <Confirmations pending={chat.pending} respond={chat.respond} toolTitles={toolTitles} />
      {failure ? (
        <p role="alert" className="text-sm text-destructive">
          {describeError(failure).message}
        </p>
      ) : null}
      <form onSubmit={(e) => void submit(e)}>
        <InputGroup>
          <InputGroupTextarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder={chat.busy ? "回答进行中，输入的内容会插入这次回答" : placeholder}
            disabled={awaiting}
            aria-label="消息"
          />
          <InputGroupAddon align="block-end" className="justify-end gap-1">
            {chat.busy && (
              <InputGroupButton
                type="button"
                size="icon-sm"
                variant="outline"
                aria-label="停止"
                onClick={() => void chat.stop()}
              >
                <Square />
              </InputGroupButton>
            )}
            <InputGroupButton
              type="submit"
              size="icon-sm"
              variant="default"
              aria-label="发送"
              disabled={!text.trim() || awaiting}
            >
              <ArrowUp />
            </InputGroupButton>
          </InputGroupAddon>
        </InputGroup>
      </form>
    </div>
  )
}

/**
 * AI 对话：先读取历史，再显示对话；进行中的回答随即接上。工具调用需要审批时显示批准和拒绝。
 *
 *	<AssistantChat api="/api/v1/assistant/chat" id={conversationId} toolTitles={{ refund_order: "退款" }} />
 */
export function AssistantChat(props: AssistantChatProps): ReactNode {
  const history = useAssistantHistory(props)
  if (history.isPending) {
    return (
      <div className="flex flex-col gap-3 p-4">
        <Skeleton className="ml-auto h-8 w-40" />
        <Skeleton className="h-16 w-3/4" />
      </div>
    )
  }
  if (history.isError) {
    return (
      <p role="alert" className="p-4 text-sm text-destructive">
        {describeError(history.error).message}
      </p>
    )
  }
  // key：换对话时重新创建，useChat 用新的历史。
  return <Conversation key={`${props.api}:${props.id}`} {...props} messages={history.data} />
}

/**
 * 顶栏上的 AI 助手按钮，点开是侧边的对话。放在 actions 插槽：
 *
 *	<Fill name="actions"><AssistantButton api="/api/v1/assistant/chat" id={conversationId} /></Fill>
 */
export function AssistantButton({
  title = "AI 助手",
  ...props
}: AssistantChatProps & { title?: ReactNode }): ReactNode {
  const [open, setOpen] = useState(false)
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button variant="ghost" size="icon" aria-label="AI 助手">
          <Bot />
        </Button>
      </SheetTrigger>
      <SheetContent className="w-full gap-0 sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>{title}</SheetTitle>
        </SheetHeader>
        <div className="min-h-0 flex-1 px-4 pb-4">{open && <AssistantChat {...props} />}</div>
      </SheetContent>
    </Sheet>
  )
}
