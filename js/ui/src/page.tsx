// 页面的标题区：标题、说明和操作按钮，页面之间保持一致的层级和间距。

import { Stack, Text, Title } from "@mantine/core"
import type { ReactNode } from "react"

/** Page 的选项。 */
export interface PageProps {
  title: ReactNode
  /** 标题下面的一句说明。 */
  description?: ReactNode
  /** 右侧的操作，例如“新建”按钮。 */
  actions?: ReactNode
  children?: ReactNode
}

/** 页面：标题区加内容。 */
export function Page({ title, description, actions, children }: PageProps): ReactNode {
  return (
    <Stack gap="lg">
      <div className="tn-page-header">
        <div>
          <Title order={2} className="tn-page-title">
            {title}
          </Title>
          {description && (
            <Text c="dimmed" size="sm" mt={4}>
              {description}
            </Text>
          )}
        </div>
        {actions && <div>{actions}</div>}
      </div>
      {children}
    </Stack>
  )
}
