// 文档页的布局：左侧是目录，中间是正文，右侧是本页目录（按正文中的 h2、h3 生成）。放在 SiteLayout 之中。

import { Container, Group, Text, Typography, UnstyledButton } from "@mantine/core"
import { IconChevronLeft, IconChevronRight } from "@tabler/icons-react"
import { Link, useRouterState } from "@tanstack/react-router"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { NavLinkLink } from "../components/links"
import { useText } from "../i18n/i18n"

/** 文档目录中的一组。 */
export interface DocsSection {
  title: ReactNode
  items: { title: string; to: string }[]
}

/** DocsLayout 的选项。 */
export interface DocsLayoutProps {
  /** 左侧目录。 */
  nav: DocsSection[]
  /** 正文。 */
  children: ReactNode
  /** 不显示右侧的本页目录。 */
  hideToc?: boolean
}

/** 本页目录中的一项。 */
export interface TocItem {
  id: string
  title: string
  depth: 2 | 3
}

/** 文档页。正文中没有 id 的标题会按文字生成 id，本页目录据此跳转。 */
export function DocsLayout({ nav, children, hideToc }: DocsLayoutProps): ReactNode {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const t = useText()
  const article = useRef<HTMLElement>(null)
  const toc = useToc(article, pathname)
  const flat = nav.flatMap((s) => s.items)
  const index = flat.findIndex((i) => i.to === pathname)
  const prev = index > 0 ? flat[index - 1] : undefined
  const next = index >= 0 && index < flat.length - 1 ? flat[index + 1] : undefined
  return (
    <Container size="xl" className="tn-docs">
      <aside className="tn-docs-sidebar" aria-label={t("tenon.docs.sidebar")}>
        {nav.map((section, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: 目录是固定的列表
          <div key={i}>
            <div className="tn-docs-section">{section.title}</div>
            {section.items.map((item) => (
              <NavLinkLink
                key={item.to}
                className="tn-nav-item"
                to={item.to}
                label={item.title}
                active={item.to === pathname}
              />
            ))}
          </div>
        ))}
      </aside>
      <article className="tn-docs-article" ref={article}>
        <Typography className="tn-prose">{children}</Typography>
        {(prev || next) && (
          <Group justify="space-between" mt={56} pt="lg" style={{ borderTop: "1px solid var(--tn-border)" }}>
            {prev ? <PageLink item={prev} dir="prev" /> : <span />}
            {next ? <PageLink item={next} dir="next" /> : <span />}
          </Group>
        )}
      </article>
      {!hideToc && (
        <nav className="tn-docs-toc" aria-label={t("tenon.docs.toc")}>
          {toc.items.length > 0 && (
            <>
              <Text size="xs" fw={600} mb={8}>
                {t("tenon.docs.on_this_page")}
              </Text>
              {toc.items.map((t) => (
                <a key={t.id} href={`#${t.id}`} data-depth={t.depth} data-active={t.id === toc.active || undefined}>
                  {t.title}
                </a>
              ))}
            </>
          )}
        </nav>
      )}
    </Container>
  )
}

function PageLink({ item, dir }: { item: { title: string; to: string }; dir: "prev" | "next" }): ReactNode {
  const t = useText()
  return (
    <UnstyledButton component={Link} to={item.to} style={{ textAlign: dir === "next" ? "right" : "left" }}>
      <Text size="xs" c="dimmed">
        {dir === "prev" ? t("tenon.docs.prev") : t("tenon.docs.next")}
      </Text>
      <Group gap={4} wrap="nowrap" c="brand" justify={dir === "next" ? "flex-end" : "flex-start"}>
        {dir === "prev" && <IconChevronLeft size={16} />}
        <Text fw={500} size="sm">
          {item.title}
        </Text>
        {dir === "next" && <IconChevronRight size={16} />}
      </Group>
    </UnstyledButton>
  )
}

/** 由标题文字生成 id：保留文字（包括中文），空白换成 -。 */
export function headingId(text: string): string {
  return text
    .trim()
    .toLowerCase()
    .replace(/[\s]+/g, "-")
    .replace(/[^\p{L}\p{N}_-]/gu, "")
}

/** 读取正文中的 h2、h3：生成本页目录，并跟踪滚动到的位置。 */
export function useToc(ref: { current: HTMLElement | null }, key: unknown): { items: TocItem[]; active?: string } {
  const [items, setItems] = useState<TocItem[]>([])
  const [active, setActive] = useState<string>()
  // biome-ignore lint/correctness/useExhaustiveDependencies: key 变化（换了页面）时重新读取
  useEffect(() => {
    const root = ref.current
    if (!root) return
    const scan = () => {
      const used = new Set<string>()
      const found: TocItem[] = []
      for (const h of root.querySelectorAll<HTMLHeadingElement>("h2, h3")) {
        let id = h.id || headingId(h.textContent ?? "")
        for (let n = 2; used.has(id); n++) id = `${headingId(h.textContent ?? "")}-${n}`
        used.add(id)
        if (!h.id) h.id = id
        found.push({ id, title: h.textContent ?? "", depth: h.tagName === "H3" ? 3 : 2 })
      }
      setItems((prev) => (JSON.stringify(prev) === JSON.stringify(found) ? prev : found))
    }
    scan()
    const mo = new MutationObserver(scan)
    mo.observe(root, { childList: true, subtree: true })
    let io: IntersectionObserver | undefined
    if (typeof IntersectionObserver !== "undefined") {
      io = new IntersectionObserver(
        (entries) => {
          const visible = entries
            .filter((e) => e.isIntersecting)
            .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
          if (visible[0]) setActive(visible[0].target.id)
        },
        { rootMargin: "0px 0px -70% 0px" },
      )
      for (const h of root.querySelectorAll("h2, h3")) io.observe(h)
    }
    return () => {
      mo.disconnect()
      io?.disconnect()
    }
  }, [ref, key])
  return { items, active }
}
