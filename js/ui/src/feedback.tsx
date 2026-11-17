// 确认和提示：删除、执行操作之前的确认对话框，完成或失败后的通知。

import { describeError } from "@go-tenon/client"
import { Text } from "@mantine/core"
import { modals } from "@mantine/modals"
import { notifications } from "@mantine/notifications"
import type { ReactNode } from "react"
import { errorMessage } from "./auth"

/** confirm 的选项。 */
export interface ConfirmOptions {
  title: ReactNode
  /** 对话框中的说明。 */
  message?: ReactNode
  /** 确认按钮的文字，默认“确定”。 */
  confirmLabel?: string
  /** 危险的操作：确认按钮是红色。 */
  danger?: boolean
}

/** 弹出确认对话框，用户确认时返回 true。需要 UIProvider。 */
export function confirm({ title, message, confirmLabel = "确定", danger }: ConfirmOptions): Promise<boolean> {
  return new Promise((resolve) => {
    modals.openConfirmModal({
      title,
      children: message ? (
        <Text size="sm" c="dimmed">
          {message}
        </Text>
      ) : undefined,
      labels: { confirm: confirmLabel, cancel: "取消" },
      confirmProps: { color: danger ? "red" : undefined },
      onConfirm: () => resolve(true),
      onCancel: () => resolve(false),
      onClose: () => resolve(false),
    })
  })
}

/** 操作成功的提示。 */
export function notifySuccess(message: ReactNode): void {
  notifications.show({ message, color: "green", autoClose: 2500 })
}

/** 操作失败的提示：显示服务端的文案，没有时按错误的种类给出提示。 */
export function notifyError(err: unknown, title?: ReactNode): void {
  notifications.show({ title, message: errorMessage(describeError(err)), color: "red", autoClose: 6000 })
}
