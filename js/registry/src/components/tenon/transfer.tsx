// 导入、导出的按钮和对话框：大表格在后台导入或导出，对话框显示进度和结果，进度同时出现在任务中心。
// 逻辑在 @go-tenon/shell 的 useImport、useExport，这里只负责外观。

import { describeError } from "@go-tenon/client"
import { type ExportSource, type ImportSource, type TransferPhase, useExport, useImport } from "@go-tenon/shell"
import { CircleAlert, CircleCheck, Download, FileUp, Upload } from "lucide-react"
import { type ReactNode, useEffect, useId, useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Progress } from "@/components/ui/progress"
import { cn } from "@/lib/utils"

/** 进度条：知道总数时显示百分比，不知道时显示已处理的行数。 */
function Running({ label, total, processed }: { label: string; total: number; processed: number }): ReactNode {
  return (
    <div className="flex flex-col gap-2 text-sm">
      <p>{label}</p>
      {total > 0 ? (
        <>
          <Progress value={(processed / total) * 100} />
          <p className="text-muted-foreground">
            {processed} / {total} 行
          </p>
        </>
      ) : (
        <p className="text-muted-foreground">已处理 {processed} 行</p>
      )}
      <p className="text-xs text-muted-foreground">可以关闭对话框，进度会显示在任务中心。</p>
    </div>
  )
}

/** 失败的原因：上传、发起时的错误，或者后台操作的错误码。 */
function failure(error: unknown, code: string | undefined): string {
  if (error) return describeError(error).message ?? "请求失败，请稍后重试。"
  return code ? `处理失败（${code}）` : "处理失败，请稍后重试。"
}

function Failed({ title, message }: { title: string; message: string }): ReactNode {
  return (
    <div role="alert" className="flex gap-2 text-sm text-destructive">
      <CircleAlert className="mt-0.5 size-4 shrink-0" />
      <div>
        <p className="font-medium">{title}</p>
        <p>{message}</p>
      </div>
    </div>
  )
}

/** 结束时调用一次 callback。 */
function useOnFinished(phase: TransferPhase, callback: (() => void) | undefined): void {
  const done = useRef(false)
  useEffect(() => {
    if (phase === "completed" && !done.current) {
      done.current = true
      callback?.()
    }
    if (phase === "idle") done.current = false
  }, [phase, callback])
}

/** ImportButton 的选项。 */
export interface ImportButtonProps {
  /** 导入的接口，见 @go-tenon/shell 的 useImport。 */
  source: ImportSource
  /** 按钮和对话框的标题，默认“导入”。 */
  title?: string
  /** 可以选择的文件，默认 .xlsx 和 .csv。 */
  accept?: string
  /** 下载导入模板，例如用 saveBlob 保存 api.article.importTemplate() 的结果。 */
  template?: () => Promise<void> | void
  /** 导入完成后调用，例如让列表重新读取。 */
  onCompleted?: () => void
}

/**
 * 导入按钮：选择表格后上传并在后台导入，对话框显示进度和结果，有没有导入的行时可以下载它们，改正后重新导入。
 *
 *   <ImportButton source={articleImport} template={downloadTemplate} onCompleted={() => list.refetch()} />
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
  const [over, setOver] = useState(false)
  const inputId = useId()
  useOnFinished(imp.phase, onCompleted)
  const op = imp.operation?.operation
  const finished = imp.phase === "completed" || imp.phase === "failed"
  const change = (next: boolean) => {
    setOpen(next)
    if (!next && finished) imp.reset()
  }
  const choose = (files: FileList | null) => {
    const file = files?.[0]
    if (file) void imp.start(file)
  }
  let body: ReactNode
  switch (imp.phase) {
    case "idle":
      body = (
        <div className="flex flex-col gap-3">
          <label
            htmlFor={inputId}
            onDragOver={(e) => {
              e.preventDefault()
              setOver(true)
            }}
            onDragLeave={() => setOver(false)}
            onDrop={(e) => {
              e.preventDefault()
              setOver(false)
              choose(e.dataTransfer.files)
            }}
            className={cn(
              "flex cursor-pointer flex-col items-center gap-2 rounded-lg border border-dashed p-8 text-center text-sm hover:bg-muted/50",
              over && "border-primary bg-muted/50",
            )}
          >
            <FileUp className="size-8 text-muted-foreground" />
            <span>点击或拖动表格到这里</span>
            <span className="text-xs text-muted-foreground">支持 {accept.replaceAll(",", "、")}</span>
            <input
              id={inputId}
              type="file"
              accept={accept}
              className="sr-only"
              onChange={(e) => choose(e.target.files)}
            />
          </label>
          {template && (
            <Button variant="link" className="h-auto self-start p-0" onClick={() => void template()}>
              <Download />
              下载导入模板
            </Button>
          )}
        </div>
      )
      break
    case "uploading":
      body = (
        <div className="flex flex-col gap-2 text-sm">
          <p>正在上传…</p>
          <Progress value={(imp.uploaded ?? 0) * 100} />
        </div>
      )
      break
    case "starting":
    case "running":
      body = <Running label="正在导入…" total={op?.total ?? 0} processed={op?.processed ?? 0} />
      break
    case "completed": {
      const failed = op?.failed ?? 0
      body = (
        <div className="flex flex-col items-center gap-3 py-4 text-center text-sm">
          <CircleCheck className={cn("size-10", failed > 0 ? "text-amber-500" : "text-emerald-600")} />
          <p className="font-medium">导入完成</p>
          <p className="text-muted-foreground">
            成功 {(op?.processed ?? 0) - failed} 行{failed > 0 && `，${failed} 行没有导入`}
          </p>
          {imp.downloadFailures && (
            <Button variant="outline" onClick={() => void imp.downloadFailures?.()}>
              <Download />
              下载没有导入的行
            </Button>
          )}
        </div>
      )
      break
    }
    case "failed":
      body = (
        <div className="flex flex-col gap-3">
          <Failed title="导入失败" message={failure(imp.error, op?.error)} />
          <Button variant="outline" className="self-start" onClick={imp.reset}>
            重新选择文件
          </Button>
        </div>
      )
      break
  }
  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)}>
        <Upload />
        {title}
      </Button>
      <Dialog open={open} onOpenChange={change}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
          </DialogHeader>
          {body}
          <DialogFooter>
            <Button variant="outline" onClick={() => change(false)}>
              关闭
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

/** ExportButton 的选项。 */
export interface ExportButtonProps {
  /** 导出的接口，见 @go-tenon/shell 的 useExport。 */
  source: ExportSource
  /** 按钮和对话框的标题，默认“导出”。 */
  title?: string
}

/**
 * 导出按钮：点击后在后台导出，对话框显示进度，完成后下载。
 *
 *   <ExportButton source={{ start: () => api.article.exportStart({ format: "xlsx" }), progress }} />
 */
export function ExportButton({ source, title = "导出" }: ExportButtonProps): ReactNode {
  const exp = useExport({ title, ...source })
  const [open, setOpen] = useState(false)
  const op = exp.operation?.operation
  const finished = exp.phase === "completed" || exp.phase === "failed"
  const begin = () => {
    setOpen(true)
    if (exp.phase === "idle") void exp.start()
  }
  const change = (next: boolean) => {
    setOpen(next)
    if (!next && finished) exp.reset()
  }
  let body: ReactNode
  if (exp.phase === "completed") {
    body = (
      <div className="flex flex-col items-center gap-3 py-4 text-center text-sm">
        <CircleCheck className="size-10 text-emerald-600" />
        <p className="font-medium">导出完成</p>
        <p className="text-muted-foreground">共 {op?.processed ?? 0} 行</p>
        {exp.file && (
          <Button asChild>
            <a href={exp.file.url} download={exp.file.filename ?? true}>
              <Download />
              下载
            </a>
          </Button>
        )}
      </div>
    )
  } else if (exp.phase === "failed") {
    body = <Failed title="导出失败" message={failure(exp.error, op?.error)} />
  } else {
    body = <Running label="正在导出…" total={op?.total ?? 0} processed={op?.processed ?? 0} />
  }
  return (
    <>
      <Button variant="outline" onClick={begin}>
        <Download />
        {title}
      </Button>
      <Dialog open={open} onOpenChange={change}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
          </DialogHeader>
          {body}
          <DialogFooter>
            <Button variant="outline" onClick={() => change(false)}>
              关闭
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
