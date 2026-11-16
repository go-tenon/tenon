// 导入、导出的按钮和对话框：大表格在后台导入或导出，对话框显示进度和结果，进度同时出现在任务中心。
// 逻辑在 @go-tenon/shell 的 useImport、useExport，这里只负责外观。

import { DownloadOutlined, ExportOutlined, ImportOutlined, InboxOutlined } from "@ant-design/icons"
import { describeError } from "@go-tenon/client"
import { type ExportSource, type ImportSource, type TransferPhase, useExport, useImport } from "@go-tenon/shell"
import { Alert, Button, Modal, Progress, Result, Space, Typography, Upload } from "antd"
import { type ReactNode, useEffect, useRef, useState } from "react"

/** 进度条：知道总数时显示百分比，不知道时显示已处理的行数。 */
function Running({ label, total, processed }: { label: string; total: number; processed: number }): ReactNode {
  return (
    <Space direction="vertical" style={{ width: "100%" }}>
      <Typography.Text>{label}</Typography.Text>
      {total > 0 ? (
        <Progress percent={Math.floor((processed / total) * 100)} />
      ) : (
        <Typography.Text type="secondary">已处理 {processed} 行</Typography.Text>
      )}
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        可以关闭对话框，进度会显示在任务中心。
      </Typography.Text>
    </Space>
  )
}

/** 失败的原因：上传、发起时的错误，或者后台操作的错误码。 */
function failure(error: unknown, code: string | undefined): string {
  if (error) return describeError(error).message ?? "请求失败，请稍后重试。"
  return code ? `处理失败（${code}）` : "处理失败，请稍后重试。"
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
        <Space direction="vertical" style={{ width: "100%" }}>
          <Upload.Dragger
            accept={accept}
            showUploadList={false}
            beforeUpload={(file) => {
              void imp.start(file)
              return false
            }}
          >
            <p className="ant-upload-drag-icon">
              <InboxOutlined />
            </p>
            <p className="ant-upload-text">点击或拖动表格到这里</p>
            <p className="ant-upload-hint">支持 {accept.replaceAll(",", "、")}</p>
          </Upload.Dragger>
          {template && (
            <Button type="link" icon={<DownloadOutlined />} onClick={() => void template()} style={{ padding: 0 }}>
              下载导入模板
            </Button>
          )}
        </Space>
      )
      break
    case "uploading":
      body = (
        <Space direction="vertical" style={{ width: "100%" }}>
          <Typography.Text>正在上传…</Typography.Text>
          <Progress percent={Math.floor((imp.uploaded ?? 0) * 100)} />
        </Space>
      )
      break
    case "starting":
    case "running":
      body = <Running label="正在导入…" total={op?.total ?? 0} processed={op?.processed ?? 0} />
      break
    case "completed": {
      const failed = op?.failed ?? 0
      body = (
        <Result
          status={failed > 0 ? "warning" : "success"}
          title="导入完成"
          subTitle={`成功 ${(op?.processed ?? 0) - failed} 行${failed > 0 ? `，${failed} 行没有导入` : ""}`}
          extra={
            imp.downloadFailures && (
              <Button icon={<DownloadOutlined />} onClick={() => void imp.downloadFailures?.()}>
                下载没有导入的行
              </Button>
            )
          }
        />
      )
      break
    }
    case "failed":
      body = (
        <Space direction="vertical" style={{ width: "100%" }}>
          <Alert type="error" showIcon message="导入失败" description={failure(imp.error, op?.error)} />
          <Button onClick={imp.reset}>重新选择文件</Button>
        </Space>
      )
      break
  }
  return (
    <>
      <Button icon={<ImportOutlined />} onClick={() => setOpen(true)}>
        {title}
      </Button>
      <Modal title={title} open={open} onCancel={close} footer={<Button onClick={close}>关闭</Button>} destroyOnHidden>
        {body}
      </Modal>
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
 *	<ExportButton source={{ start: () => api.article.exportStart({ format: "xlsx" }), progress }} />
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
  const close = () => {
    setOpen(false)
    if (finished) exp.reset()
  }
  let body: ReactNode
  if (exp.phase === "completed") {
    body = (
      <Result
        status="success"
        title="导出完成"
        subTitle={`共 ${op?.processed ?? 0} 行`}
        extra={
          exp.file && (
            <Button type="primary" icon={<DownloadOutlined />} href={exp.file.url} download={exp.file.filename ?? true}>
              下载
            </Button>
          )
        }
      />
    )
  } else if (exp.phase === "failed") {
    body = <Alert type="error" showIcon message="导出失败" description={failure(exp.error, op?.error)} />
  } else {
    body = <Running label="正在导出…" total={op?.total ?? 0} processed={op?.processed ?? 0} />
  }
  return (
    <>
      <Button icon={<ExportOutlined />} onClick={begin}>
        {title}
      </Button>
      <Modal title={title} open={open} onCancel={close} footer={<Button onClick={close}>关闭</Button>} destroyOnHidden>
        {body}
      </Modal>
    </>
  )
}
