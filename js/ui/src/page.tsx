// 页面的标题区：面包屑、标题、说明和操作按钮，页面之间保持一致的层级和间距。
// 面包屑在页面中而不在顶栏：顶栏留给标志、多标签和菜单。

import { Anchor, Breadcrumbs, Stack, Text, Title } from "@mantine/core"
import { Link } from "@tanstack/react-router"
import type { ReactNode } from "react"
import { useNavigation } from "./shell"

/** Page 的选项。 */
export interface PageProps {
  title: ReactNode
  /** 标题下面的一句说明。 */
  description?: ReactNode
  /** 右侧的操作，例如“新建”按钮。 */
  actions?: ReactNode
  /** 标题上面的面包屑（来自路由的标题），默认显示；false 时不显示。 */
  breadcrumbs?: boolean
  children?: ReactNode
}

/** 页面：面包屑、标题区加内容。 */
export function Page({ title, description, actions, breadcrumbs = true, children }: PageProps): ReactNode {
  return (
    <Stack gap="md">
      <Stack gap={10}>
        {breadcrumbs && <Crumbs />}
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
      </Stack>
      {children}
    </Stack>
  )
}

/** 面包屑：最后一级是当前页面，不是链接。 */
function Crumbs(): ReactNode {
  const { crumbs } = useNavigation()
  if (crumbs.length === 0) return null
  return (
    <Breadcrumbs className="tn-crumbs" separatorMargin={6}>
      {crumbs.map((c, i) =>
        i === crumbs.length - 1 ? (
          <Text key={c.path} size="xs" aria-current="page">
            {c.title}
          </Text>
        ) : (
          <Anchor key={c.path} component={Link} to={c.path} size="xs" c="dimmed">
            {c.title}
          </Anchor>
        ),
      )}
    </Breadcrumbs>
  )
}
