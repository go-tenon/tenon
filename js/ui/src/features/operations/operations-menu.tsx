// 后台任务菜单：操作中心（model.ts）的外观，顶栏的图标按钮，列出导入、导出等耗时操作的进度。作为布局的 actions 放在顶栏中。

import { ActionIcon, Badge, Box, Group, Loader, Popover, Progress, Stack, Text } from "@mantine/core"
import { IconListCheck, IconX } from "@tabler/icons-react"
import type { ReactNode } from "react"
import { useApp } from "../../app/app"
import { useText } from "../../i18n/i18n"
import { dismissOperation, type TrackedOperation, useOperations } from "./model"

/** 后台任务：导入、导出等耗时操作的进度。有进行中的任务时图标转动。 */
export function OperationsMenu(): ReactNode {
  const ops = useOperations()
  const app = useApp()
  const t = useText()
  if (ops.length === 0) return null
  const running = ops.filter((o) => !o.finished).length
  return (
    <Popover width={340} position="bottom-end">
      <Popover.Target>
        <ActionIcon
          size={32}
          aria-label={running > 0 ? t("tenon.operations.running", { count: running }) : t("tenon.operations.title")}
        >
          {running > 0 ? <Loader size={16} /> : <IconListCheck size={16} stroke={1.5} />}
        </ActionIcon>
      </Popover.Target>
      <Popover.Dropdown p={0}>
        <Text fw={600} size="sm" px="md" py="sm" style={{ borderBottom: "1px solid var(--tn-border)" }}>
          {t("tenon.operations.title")}
        </Text>
        <Stack gap={0}>
          {ops.map((op) => (
            <OperationRow key={op.id} op={op} onDismiss={() => dismissOperation(app, op.id)} />
          ))}
        </Stack>
      </Popover.Dropdown>
    </Popover>
  )
}

function OperationRow({ op, onDismiss }: { op: TrackedOperation; onDismiss: () => void }): ReactNode {
  const t = useText()
  const status = op.error ? "failed" : op.operation?.status
  const failed = status === "failed"
  const done = op.finished && !failed
  return (
    <Box px="md" py="sm" style={{ borderBottom: "1px solid var(--tn-border)" }}>
      <Group justify="space-between" wrap="nowrap" gap="xs">
        <Text size="sm" truncate>
          {op.title}
        </Text>
        {op.finished || op.error ? (
          <Group gap={4} wrap="nowrap">
            <Badge color={failed ? "red" : "green"} size="sm">
              {failed ? t("tenon.operations.failed") : t("tenon.operations.done")}
            </Badge>
            <ActionIcon size="sm" aria-label={t("tenon.operations.remove")} onClick={onDismiss}>
              <IconX size={14} />
            </ActionIcon>
          </Group>
        ) : (
          <Text size="xs" c="dimmed">
            {op.ratio !== undefined ? `${Math.round(op.ratio * 100)}%` : t("tenon.operations.processing")}
          </Text>
        )}
      </Group>
      {!done && !failed && (
        <Progress value={(op.ratio ?? 0) * 100} size="sm" mt={8} animated={op.ratio === undefined} />
      )}
      {op.operation && op.operation.failed > 0 && (
        <Text size="xs" c="red" mt={4}>
          {t("tenon.operations.failed_items", { count: op.operation.failed })}
        </Text>
      )}
    </Box>
  )
}
