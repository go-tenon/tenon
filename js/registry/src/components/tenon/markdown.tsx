// 消息的 Markdown 渲染：Streamdown 处理流式输出（未闭合的语法先补全再渲染），代码块交给 shadcn 的 CodeBlock，
// 带语法高亮和复制按钮。行内代码保持行内。

import { type ComponentProps, type ReactNode, useMemo } from "react"
import { type ExtraProps, Streamdown, useIsCodeFenceIncomplete } from "streamdown"
import {
  CodeBlock,
  CodeBlockActions,
  CodeBlockContent,
  CodeBlockCopyButton,
  CodeBlockHeader,
  CodeBlockLine,
  CodeBlockTitle,
} from "@/components/ui/code-block"
import { useHighlightedCode } from "@/hooks/use-highlighted-code"
import { cn } from "@/lib/utils"

function MarkdownCodeBlock({ code, language }: { code: string; language: string }): ReactNode {
  // 代码块的结束标记还没有到达时，复制按钮不可用。
  const incomplete = useIsCodeFenceIncomplete()
  const lines = useHighlightedCode(code, language)
  // 行没有其他标识，按位置排列：内容变化时同一位置仍是同一行。
  const rows: ReactNode[] = []
  for (let i = 0; i < lines.length; i++) rows.push(<CodeBlockLine key={`line-${i}`}>{lines[i]}</CodeBlockLine>)
  return (
    <CodeBlock className="my-2">
      <CodeBlockHeader>
        <CodeBlockTitle>{language || "text"}</CodeBlockTitle>
        <CodeBlockActions>
          <CodeBlockCopyButton value={code} disabled={incomplete} />
        </CodeBlockActions>
      </CodeBlockHeader>
      <CodeBlockContent>{rows}</CodeBlockContent>
    </CodeBlock>
  )
}

function MarkdownCode({ node: _node, className, children, ...props }: ComponentProps<"code"> & ExtraProps): ReactNode {
  // Streamdown 给代码块（而不是行内代码）加上 data-block。
  if (!("data-block" in props)) {
    return (
      <code className={cn("rounded-sm bg-muted px-1 py-0.5 font-mono text-[0.9em]", className)} {...props}>
        {children}
      </code>
    )
  }
  const language = /language-([\w-]+)/.exec(className ?? "")?.[1] ?? ""
  return <MarkdownCodeBlock code={String(children ?? "")} language={language} />
}

/** Markdown：streaming 为 true 时按流式输出渲染（回答还在生成）。 */
export function Markdown({
  children,
  streaming,
  className,
}: {
  children: string
  streaming?: boolean
  className?: string
}): ReactNode {
  const components = useMemo(() => ({ code: MarkdownCode }), [])
  return (
    <Streamdown
      className={cn(
        "flex flex-col gap-2 text-sm leading-relaxed [&_ol]:list-decimal [&_ol]:pl-5 [&_ul]:list-disc [&_ul]:pl-5",
        className,
      )}
      mode={streaming ? "streaming" : "static"}
      isAnimating={streaming}
      components={components}
    >
      {children}
    </Streamdown>
  )
}
