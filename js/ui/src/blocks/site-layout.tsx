// 官网和内容站的布局：吸顶的页头（标志、导航、操作）、内容、页脚。窄屏时导航收进抽屉。
// 首页的内容由应用自己写；文档页用 DocsLayout。

import { Box, Burger, Container, Drawer, Group, SimpleGrid, Stack, Text } from "@mantine/core"
import { Link, Outlet } from "@tanstack/react-router"
import { type CSSProperties, type ReactNode, useState } from "react"
import { useText } from "../i18n/i18n"

// 页头的高度（像素）。
const headerHeight = 64

/** 页头或页脚中的一个链接：应用内的地址用 to，外部地址用 href。 */
export interface SiteLink {
  label: ReactNode
  to?: string
  href?: string
}

/** 页脚的一列链接。 */
export interface FooterColumn {
  title: ReactNode
  links: SiteLink[]
}

/** SiteLayout 的选项。 */
export interface SiteLayoutProps {
  /** 页头左侧的标志和名称，点击回到首页。 */
  brand: ReactNode
  /** 页头的导航。 */
  nav?: SiteLink[]
  /** 页头右侧，例如“登录”“免费试用”按钮。 */
  actions?: ReactNode
  /** 页脚的几列链接。 */
  footer?: FooterColumn[]
  /** 页脚底部的一行，例如版权和备案号。 */
  note?: ReactNode
  /** 页头和页脚的最大宽度（Mantine 的容器尺寸），默认 xl，与 DocsLayout 对齐。 */
  size?: "sm" | "md" | "lg" | "xl"
  /** 页面内容；默认是子路由。 */
  children?: ReactNode
}

function SiteAnchor({ link, onClick }: { link: SiteLink; onClick?: () => void }): ReactNode {
  return link.to ? (
    <Link to={link.to} onClick={onClick} activeOptions={{ exact: link.to === "/" }}>
      {link.label}
    </Link>
  ) : (
    <a href={link.href} onClick={onClick}>
      {link.label}
    </a>
  )
}

/** 官网的布局。 */
export function SiteLayout({
  brand,
  nav = [],
  actions,
  footer = [],
  note,
  size = "xl",
  children,
}: SiteLayoutProps): ReactNode {
  const [open, setOpen] = useState(false)
  const t = useText()
  const vars = { "--tn-header-height": `${headerHeight}px` } as CSSProperties
  return (
    <div className="tn-site" style={vars}>
      <header className="tn-site-header">
        <Container size={size} className="tn-site-header-inner">
          <Link to="/" className="tn-site-brand">
            {brand}
          </Link>
          <Group gap={2} className="tn-site-nav" visibleFrom="sm" component="nav" aria-label={t("tenon.site.nav")}>
            {nav.map((l, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: 导航是固定的列表
              <SiteAnchor key={i} link={l} />
            ))}
          </Group>
          <Box style={{ flex: 1 }} />
          <Group gap="xs" visibleFrom="sm">
            {actions}
          </Group>
          <Burger
            hiddenFrom="sm"
            size="sm"
            opened={open}
            onClick={() => setOpen((o) => !o)}
            aria-label={t("tenon.frame.open_nav")}
          />
        </Container>
      </header>
      <Drawer opened={open} onClose={() => setOpen(false)} size="xs" position="right" title={brand}>
        <Stack gap={4} className="tn-site-nav">
          {nav.map((l, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: 导航是固定的列表
            <SiteAnchor key={i} link={l} onClick={() => setOpen(false)} />
          ))}
          <Group mt="md">{actions}</Group>
        </Stack>
      </Drawer>
      <main className="tn-site-main">{children ?? <Outlet />}</main>
      {(footer.length > 0 || note) && (
        <footer className="tn-site-footer">
          <Container size={size}>
            {footer.length > 0 && (
              <SimpleGrid cols={{ base: 2, sm: Math.min(footer.length, 4) }} spacing="xl" mb={40}>
                {footer.map((col, i) => (
                  // biome-ignore lint/suspicious/noArrayIndexKey: 页脚是固定的列表
                  <Stack key={i} gap={8}>
                    <Text size="sm" fw={600} c="var(--tn-text)">
                      {col.title}
                    </Text>
                    {col.links.map((l, j) => (
                      // biome-ignore lint/suspicious/noArrayIndexKey: 页脚是固定的列表
                      <SiteAnchor key={j} link={l} />
                    ))}
                  </Stack>
                ))}
              </SimpleGrid>
            )}
            {note && <Text size="xs">{note}</Text>}
          </Container>
        </footer>
      )}
    </div>
  )
}
