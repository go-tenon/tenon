// 导入与导出：shell/ 中的 useImport、useExport 的外观。导入在对话框中选择表格、上传、显示进度和结果；
// 导出显示进度，完成后下载。进度同时出现在顶栏的后台任务中。

import { describeError, type OperationLike } from "@go-tenon/client"
import { Alert, Anchor, Button, Group, Modal, Progress, Stack, Text, ThemeIcon, UnstyledButton } from "@mantine/core"
import {
  IconAlertTriangle,
  IconCheck,
  IconDownload,
  IconFileSpreadsheet,
  IconFileUpload,
  IconUpload,
} from "@tabler/icons-react"
import { type DragEvent, type ReactNode, useEffect, useRef, useState } from "react"
import { errorMessage } from "./auth"
import { type ExportSource, type ImportSource, type TransferPhase, useExport, useImport } from "./shell"

/** 结束时调用一次 onCompleted，例如刷新列表。 */
function useOnFinished(phase: TransferPhase, onCompleted?: () => void) {
  const prev = useRef(phase)
  useEffect(() => {
    if (phase === "completed" && prev.current !== "completed") onCompleted?.()
    prev.current = phase
  }, [phase, onCompleted])
}

function failure(error: unknown, opError?: string): string {
  if (opError) return opError
  if (error) return errorMessage(describeError(error))
  return "处理时出错"
}

function Running({ label, op }: { label: string; op?: OperationLike }): ReactNode {
  const total = op?.total ?? 0
  const processed = op?.processed ?? 0
  return (
    <Stack gap={8}>
      <Group justify="space-between">
        <Text size="sm">{label}</Text>
        <Text size="sm" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
          {total > 0
            ? `${processed} / ${total} · ${Math.floor((processed / total) * 100)}%`
            : processed > 0
              ? `已处理 ${processed} 行`
              : ""}
        </Text>
      </Group>
      <Progress value={total > 0 ? (processed / total) * 100 : 0} animated={total === 0} size="md" />
    </Stack>
  )
}

/** ImportButton 的选项。 */
export interface ImportButtonProps {
  /** 导入的接口，见 shell/ 中的 useImport。 */
  source: ImportSource
  /** 按钮和对话框的标题，默认“导入”。 */
  title?: string
  /** 可以选择的文件类型，默认 .xlsx,.csv。 */
  accept?: string
  /** 下载导入模板；没有时不显示。 */
  template?: () => Promise<void> | void
  /** 导入完成后调用，例如刷新列表。 */
  onCompleted?: () => void
}

/**
 * 导入按钮：选择或拖入表格后上传并在后台导入，对话框显示进度和结果；有没有导入的行时可以下载它们，改正后重新导入。
 *
 *	<ImportButton source={articleImport} template={downloadTemplate} onCompleted={() => list.refetch()} />
 */
export function ImportButton({
  source,
  title = "导入",
  accept = ".xlsx,.csv",
  template,
  onCompleted,
}: ImportButtonProps): ReactNode {
  const imp = useImport({ title, ...source })
  const [open, setOpen] = useState(false)
  useOnFinished(imp.phase, onCompleted)
  const op = imp.operation?.operation
  const finished = imp.phase === "completed" || imp.phase === "failed"
  const close = () => {
    setOpen(false)
    if (finished) imp.reset()
  }
  let body: ReactNode
  switch (imp.phase) {
    case "idle":
      body = (
        <Stack gap="sm">
          <DropZone accept={accept} onFile={(f) => void imp.start(f)} />
          {template && (
            <Anchor component="button" type="button" size="sm" onClick={() => void template()}>
              <Group gap={4}>
                <IconDownload size={14} />
                下载导入模板
              </Group>
            </Anchor>
          )}
        </Stack>
      )
      break
    case "uploading":
      body = (
        <Stack gap={8}>
          <Text size="sm">正在上传…</Text>
          <Progress value={(imp.uploaded ?? 0) * 100} size="md" />
        </Stack>
      )
      break
    case "starting":
    case "running":
      body = <Running label="正在导入…" op={op} />
      break
    case "completed": {
      const failed = op?.failed ?? 0
      body = (
        <Stack align="center" gap="xs" py="md" ta="center">
          <ThemeIcon size={44} radius="xl" variant="light" color={failed > 0 ? "deep-orange" : "green"}>
            {failed > 0 ? <IconAlertTriangle size={22} /> : <IconCheck size={22} />}
          </ThemeIcon>
          <Text fw={600}>导入完成</Text>
          <Text size="sm" c="dimmed">
            成功 {(op?.processed ?? 0) - failed} 行{failed > 0 ? `，${failed} 行没有导入` : ""}
          </Text>
          {imp.downloadFailures && (
            <Button
              variant="default"
              leftSection={<IconDownload size={16} />}
              mt="xs"
              onClick={() => void imp.downloadFailures?.()}
            >
              下载没有导入的行
            </Button>
          )}
        </Stack>
      )
      break
    }
    case "failed":
      body = (
        <Stack gap="sm">
          <Alert color="red" variant="light" title="导入失败" role="alert">
            {failure(imp.error, op?.error)}
          </Alert>
          <Group>
            <Button variant="default" onClick={imp.reset}>
              重新选择文件
            </Button>
          </Group>
        </Stack>
      )
      break
  }
  return (
    <>
      <Button variant="default" leftSection={<IconFileUpload size={16} />} onClick={() => setOpen(true)}>
        {title}
      </Button>
      <Modal title={title} opened={open} onClose={close} size="md">
        {body}
        <Group justify="flex-end" mt="lg">
          <Button variant="default" onClick={close}>
            关闭
          </Button>
        </Group>
      </Modal>
    </>
  )
}

/** 选择或拖入一个文件。 */
function DropZone({ accept, onFile }: { accept: string; onFile: (file: File) => void }): ReactNode {
  const input = useRef<HTMLInputElement>(null)
  const [over, setOver] = useState(false)
  const drop = (e: DragEvent) => {
    e.preventDefault()
    setOver(false)
    const file = e.dataTransfer.files[0]
    if (file) onFile(file)
  }
  return (
    <UnstyledButton
      onClick={() => input.current?.click()}
      onDragOver={(e) => {
        e.preventDefault()
        setOver(true)
      }}
      onDragLeave={() => setOver(false)}
      onDrop={drop}
      style={{
        display: "block",
        width: "100%",
        padding: "28px 16px",
        textAlign: "center",
        border: `1px dashed ${over ? "var(--mantine-color-brand-5)" : "var(--tn-border-strong)"}`,
        borderRadius: "var(--mantine-radius-md)",
        background: over ? "var(--tn-active-bg)" : "var(--tn-canvas)",
        transition: "background-color 120ms ease, border-color 120ms ease",
      }}
    >
      <Stack align="center" gap={6}>
        <IconFileSpreadsheet size={28} stroke={1.4} color="var(--tn-muted)" />
        <Text size="sm" fw={500}>
          点击选择或拖入表格
        </Text>
        <Text size="xs" c="dimmed">
          支持 {accept.replaceAll(",", "、")}
        </Text>
      </Stack>
      <input
        ref={input}
        type="file"
        accept={accept}
        hidden
        aria-label="选择表格"
        onChange={(e) => {
          const file = e.currentTarget.files?.[0]
          e.currentTarget.value = ""
          if (file) onFile(file)
        }}
      />
    </UnstyledButton>
  )
}

/** ExportButton 的选项。 */
export interface ExportButtonProps {
  /** 导出的接口，见 shell/ 中的 useExport。 */
  source: ExportSource
  /** 按钮和对话框的标题，默认“导出”。 */
  title?: string
}

/**
 * 导出按钮：点击后在后台导出，对话框显示进度，完成后下载。
 *
 *	<ExportButton source={{ start: () => api.article.exportStart({ format: "xlsx" }), progress }} />
 */
export function ExportButton({ source, title = "导出" }: ExportButtonProps): ReactNode {
  const exp = useExport({ title, ...source })
  const [open, setOpen] = useState(false)
  const op = exp.operation?.operation
  const close = () => {
    setOpen(false)
    if (exp.phase === "completed" || exp.phase === "failed") exp.reset()
  }
  let body: ReactNode = null
  switch (exp.phase) {
    case "starting":
    case "running":
      body = <Running label="正在导出…" op={op} />
      break
    case "completed":
      body = (
        <Stack align="center" gap="xs" py="md" ta="center">
          <ThemeIcon size={44} radius="xl" variant="light" color="green">
            <IconCheck size={22} />
          </ThemeIcon>
          <Text fw={600}>导出完成</Text>
          {exp.file && (
            <Button
              component="a"
              href={exp.file.url}
              download={exp.file.filename}
              leftSection={<IconDownload size={16} />}
              mt="xs"
            >
              下载{exp.file.filename ? ` ${exp.file.filename}` : ""}
            </Button>
          )}
        </Stack>
      )
      break
    case "failed":
      body = (
        <Alert color="red" variant="light" title="导出失败" role="alert">
          {failure(exp.error, op?.error)}
        </Alert>
      )
      break
  }
  return (
    <>
      <Button
        variant="default"
        leftSection={<IconUpload size={16} />}
        onClick={() => {
          setOpen(true)
          if (exp.phase === "idle") void exp.start()
        }}
      >
        {title}
      </Button>
      <Modal title={title} opened={open} onClose={close} size="sm">
        {body}
        <Group justify="flex-end" mt="lg">
          <Button variant="default" onClick={close}>
            关闭
          </Button>
        </Group>
      </Modal>
    </>
  )
}
