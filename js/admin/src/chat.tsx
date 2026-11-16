// AI 对话的外观（Ant Design X）：消息、思考过程、工具调用、等待审批的工具调用、插话、停止和附件。
// 这是单独的入口 @go-tenon/admin/chat：只有用到 AI 对话的前端才需要安装 ai、@ai-sdk/react、
// @ant-design/x 和 @ant-design/x-markdown。逻辑在 @go-tenon/shell/chat。

import { CheckOutlined, CloseOutlined, PaperClipOutlined, RobotOutlined, ToolOutlined } from "@ant-design/icons"
import { Bubble, type BubbleItemType, FileCard, Sender, Think, ThoughtChain, Welcome } from "@ant-design/x"
import XMarkdown from "@ant-design/x-markdown"
import { describeError } from "@go-tenon/client"
import {
  type AssistantOptions,
  type AttachmentOptions,
  fileParts,
  type PendingApproval,
  useAssistantChat,
  useAssistantComposer,
  useAssistantHistory,
} from "@go-tenon/shell/chat"
import type { FileUIPart, UIMessage } from "ai"
import { Alert, Button, Card, Drawer, Flex, Space, Spin, Typography } from "antd"
import { type ReactNode, useRef, useState } from "react"

/** AssistantChat 的选项。 */
export interface AssistantChatProps extends AssistantOptions {
  /** 还没有消息时显示的欢迎语。 */
  welcome?: ReactNode
  placeholder?: string
  /** 工具的显示名称，例如 { refund_order: "退款" }；没有写的显示工具名。 */
  toolTitles?: Record<string, string>
  /** 渲染一条消息中的其他 part（例如应用自己的 data-* 片段）；返回 undefined 时不显示。 */
  renderPart?: (part: UIMessage["parts"][number], message: UIMessage) => ReactNode
  /** 对话区域的高度，默认 100%（由外层决定）。 */
  height?: number | string
  /** 附件的类型、大小和上传方式；false 时不能发送附件。默认允许 4 MiB 以内的图片和 PDF，以 data URL 发送。 */
  attachments?: AttachmentOptions | false
}

type Part = UIMessage["parts"][number]
type ToolPart = {
  type: string
  toolCallId: string
  state: string
  input?: unknown
  output?: unknown
  errorText?: string
}

const isTool = (p: Part): p is Part & ToolPart => p.type.startsWith("tool-") || p.type === "dynamic-tool"
const toolName = (p: ToolPart & { toolName?: string }) => p.toolName ?? p.type.slice("tool-".length)

const toolStatus: Record<string, "loading" | "success" | "error" | "abort" | undefined> = {
  "input-streaming": "loading",
  "input-available": "loading",
  "approval-requested": "loading",
  "approval-responded": "loading",
  "output-available": "success",
  "output-error": "error",
  "output-denied": "abort",
}

const json = (v: unknown) => (v === undefined ? "" : JSON.stringify(v, null, 2))

/** 一条回答中的 parts：思考过程、工具调用、文字；插话拆成单独的用户消息（见 toBubbles）。 */
function AssistantParts({
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
  let tools: (Part & ToolPart)[] = []
  const flushTools = () => {
    if (tools.length === 0) return
    out.push(
      <ThoughtChain
        key={`tools-${out.length}`}
        items={tools.map((t) => ({
          key: t.toolCallId,
          icon: <ToolOutlined />,
          title: toolTitles[toolName(t)] ?? toolName(t),
          status: toolStatus[t.state],
          collapsible: true,
          content: (
            <Typography.Paragraph style={{ marginBottom: 0 }}>
              <pre style={{ margin: 0, whiteSpace: "pre-wrap" }}>
                {json(t.input)}
                {t.output !== undefined ? `\n→ ${json(t.output)}` : ""}
                {t.errorText ? `\n→ ${t.errorText}` : ""}
              </pre>
            </Typography.Paragraph>
          ),
        }))}
      />,
    )
    tools = []
  }
  // parts 只会在末尾追加，用位置作为 key 是稳定的。
  for (let i = 0; i < parts.length; i++) {
    const p = parts[i] as Part
    if (isTool(p)) {
      tools.push(p)
      continue
    }
    flushTools()
    const key = `part-${i}`
    const last = streaming && i === parts.length - 1
    if (p.type === "text") {
      out.push(<XMarkdown key={key} content={p.text} streaming={{ hasNextChunk: last }} />)
    } else if (p.type === "reasoning") {
      out.push(
        <Think key={key} title={last ? "思考中" : "思考过程"} loading={last} defaultExpanded={false}>
          {p.text}
        </Think>,
      )
    } else if (p.type !== "step-start") {
      const node = renderPart?.(p, message)
      if (node !== undefined) out.push(<div key={key}>{node}</div>)
    }
  }
  flushTools()
  return (
    <Flex vertical gap={8}>
      {out}
    </Flex>
  )
}

/** 把消息拆成气泡：回答中的插话（data-steering）显示为单独的用户消息，插话前后的回答分成两个气泡。 */
function toBubbles(messages: UIMessage[]): { key: string; role: "user" | "ai"; message: UIMessage; parts: Part[] }[] {
  const out: { key: string; role: "user" | "ai"; message: UIMessage; parts: Part[] }[] = []
  for (const m of messages) {
    if (m.role !== "assistant") {
      out.push({ key: m.id, role: "user", message: m, parts: m.parts })
      continue
    }
    let cur: Part[] = []
    let n = 0
    const flush = () => {
      if (cur.length) out.push({ key: `${m.id}:${n++}`, role: "ai", message: m, parts: cur })
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

const kindOf = (mediaType: string) => (mediaType.startsWith("image/") ? "image" : "file")

/** 用户消息中的文件：图片显示缩略图，其他文件显示文件名，点击打开或下载。 */
function SentFiles({ files }: { files: FileUIPart[] }): ReactNode {
  const open = (f: FileUIPart) => {
    const a = document.createElement("a")
    a.href = f.url
    // data URL 不能在新标签中打开，改为下载。
    if (f.url.startsWith("data:")) a.download = f.filename ?? "附件"
    else a.target = "_blank"
    a.rel = "noreferrer"
    a.click()
  }
  return (
    <FileCard.List
      items={files.map((f, i) => ({
        key: i,
        name: f.filename ?? (kindOf(f.mediaType) === "image" ? "图片" : "文件"),
        type: kindOf(f.mediaType),
        src: kindOf(f.mediaType) === "image" ? f.url : undefined,
        onClick: kindOf(f.mediaType) === "image" ? undefined : () => open(f),
      }))}
    />
  )
}

/** 等待审批的工具调用：显示参数，批准或拒绝。 */
function Approvals({
  pending,
  respond,
  toolTitles,
}: {
  pending: PendingApproval[]
  respond: (id: string, approved: boolean) => void
  toolTitles: Record<string, string>
}): ReactNode {
  if (pending.length === 0) return null
  return (
    <Flex vertical gap={8}>
      {pending.map((p) => (
        <Card
          key={p.id}
          size="small"
          title={`需要你确认：${toolTitles[p.toolName] ?? p.toolName}`}
          extra={
            <Space>
              <Button size="small" icon={<CloseOutlined />} onClick={() => respond(p.id, false)}>
                拒绝
              </Button>
              <Button size="small" type="primary" icon={<CheckOutlined />} onClick={() => respond(p.id, true)}>
                批准
              </Button>
            </Space>
          }
        >
          <pre style={{ margin: 0, whiteSpace: "pre-wrap" }}>{json(p.input)}</pre>
        </Card>
      ))}
    </Flex>
  )
}

function Conversation({
  messages,
  welcome,
  placeholder = "输入消息，Enter 发送",
  toolTitles = {},
  renderPart,
  height = "100%",
  ...options
}: AssistantChatProps & { messages: UIMessage[] }): ReactNode {
  const chat = useAssistantChat({ ...options, messages })
  const composer = useAssistantComposer(chat)
  const { attachments, canAttach } = composer
  const picker = useRef<HTMLInputElement>(null)
  const bubbles = toBubbles(chat.messages)
  const streamingKey = chat.status === "streaming" ? bubbles[bubbles.length - 1]?.key : undefined
  const items: BubbleItemType[] = bubbles.map((b) => ({
    key: b.key,
    role: b.role,
    placement: b.role === "user" ? "end" : "start",
    variant: b.role === "user" ? "filled" : "borderless",
    content:
      b.role === "user" ? (
        <Flex vertical gap={8} align="end">
          {fileParts(b.parts).length > 0 && <SentFiles files={fileParts(b.parts)} />}
          {textOf(b.parts) || null}
        </Flex>
      ) : (
        <AssistantParts
          parts={b.parts}
          message={b.message}
          streaming={b.key === streamingKey}
          toolTitles={toolTitles}
          renderPart={renderPart}
        />
      ),
  }))
  if (chat.status === "submitted")
    items.push({ key: "loading", role: "ai", placement: "start", content: "", loading: true })

  return (
    <Flex vertical gap={12} style={{ height }}>
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        {items.length === 0 ? (
          <Welcome icon={<RobotOutlined />} title={welcome ?? "有什么可以帮你？"} variant="borderless" />
        ) : (
          <Bubble.List items={items} autoScroll />
        )}
      </div>
      <Approvals pending={chat.pending} respond={chat.respond} toolTitles={toolTitles} />
      {composer.error !== undefined ? <Alert type="error" showIcon title={composer.error} /> : null}
      {canAttach && (
        <input
          ref={picker}
          type="file"
          multiple
          hidden
          accept={attachments.accept}
          aria-label="选择文件"
          onChange={(e) => {
            attachments.add(e.target.files ?? [])
            e.target.value = "" // 同一个文件移除后可以再次选择
          }}
        />
      )}
      <Sender
        value={composer.text}
        onChange={composer.setText}
        placeholder={chat.busy ? "回答进行中，输入的内容会插入这次回答" : placeholder}
        loading={chat.busy}
        // 回答进行中也可以发送：消息插入这次回答（插话）。
        onSubmit={() => void composer.submit()}
        onCancel={() => void chat.stop()}
        disabled={composer.awaiting}
        onPasteFile={canAttach ? (list) => attachments.add(list) : undefined}
        header={
          // 只在有附件时渲染：清空后立即消失，不留下收起中的面板。
          attachments.files.length > 0 && (
            <Sender.Header title="附件" open closable={false}>
              <FileCard.List
                items={attachments.items.map((a) => ({
                  key: a.id,
                  name: a.file.name,
                  byte: a.file.size,
                  type: a.preview ? "image" : "file",
                  src: a.preview,
                }))}
                removable
                onRemove={(item) => attachments.remove(String(item.key))}
              />
            </Sender.Header>
          )
        }
        prefix={
          canAttach ? (
            <Button
              type="text"
              aria-label="添加附件"
              title={chat.busy ? "回答进行中不能发送文件" : "添加图片或文件"}
              icon={<PaperClipOutlined />}
              disabled={chat.busy || composer.awaiting}
              onClick={() => picker.current?.click()}
            />
          ) : undefined
        }
        // 只有附件、没有文字时也可以发送。
        suffix={(node, { components: { SendButton } }) =>
          chat.busy ? node : <SendButton disabled={!composer.canSend} />
        }
      />
    </Flex>
  )
}

/**
 * AI 对话：先读取历史，再显示对话；进行中的回答随即接上。工具调用需要审批时显示批准和拒绝。
 *
 *	<AssistantChat api="/api/v1/assistant/chat" id={conversationId} toolTitles={{ refund_order: "退款" }} />
 */
export function AssistantChat(props: AssistantChatProps): ReactNode {
  const history = useAssistantHistory(props)
  if (history.isPending) return <Spin style={{ display: "block", padding: 48 }} />
  if (history.isError) return <Alert type="error" showIcon title={describeError(history.error).message} />
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
  width = 480,
  ...props
}: AssistantChatProps & { title?: ReactNode; width?: number }): ReactNode {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button
        type="text"
        aria-label="AI 助手"
        icon={<RobotOutlined style={{ fontSize: 16 }} />}
        onClick={() => setOpen(true)}
      />
      <Drawer title={title} open={open} onClose={() => setOpen(false)} size={width} destroyOnHidden={false}>
        {open || props.id ? <AssistantChat {...props} /> : null}
      </Drawer>
    </>
  )
}
