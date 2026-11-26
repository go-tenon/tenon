// 页面的标题区：面包屑、标题、说明和操作按钮，页面之间保持一致的层级和间距。
// 面包屑在页面中而不在顶栏：顶栏留给标志、多标签和菜单。
// 内容多的页面可以分成几个页内标签（tabs）：标签在标题区下面，当前的标签记在地址的 ?tab= 中。

import { ActionIcon, Anchor, Badge, Breadcrumbs, Group, Stack, Tabs, Text, Title, Tooltip } from "@mantine/core"
import { IconArrowLeft, IconLayoutSidebarLeftExpand } from "@tabler/icons-react"
import { Link, useNavigate, useSearch } from "@tanstack/react-router"
import { createContext, type ReactNode, useContext, useRef } from "react"
import { useNavControl, useNavigation } from "../app/layout"
import { useText } from "../i18n/i18n"

const PageBreadcrumbsContext = createContext(true)

/** PageBreadcrumbs 的选项。 */
export interface PageBreadcrumbsProps {
  /** 其中的页面默认是否显示面包屑。 */
  show: boolean
  children?: ReactNode
}

/**
 * 布局决定其中的页面默认是否显示面包屑，例如 RailLayout 的设置中二级导航已经标出了位置，面包屑是多余的。
 * 页面的 breadcrumbs 仍可以单独打开或关掉。
 */
export function PageBreadcrumbs({ show, children }: PageBreadcrumbsProps): ReactNode {
  return <PageBreadcrumbsContext.Provider value={show}>{children}</PageBreadcrumbsContext.Provider>
}

/** 页内的一个标签。 */
export interface PageTab {
  /** 标签的标识，也是地址中 ?tab= 的值。 */
  value: string
  label: ReactNode
  /** 标签右边的数字，例如失败的条数；0、空时不显示。 */
  count?: number
  /** 数字用红色，例如有失败的任务。 */
  alert?: boolean
  content: ReactNode
}

/** Page 的选项。 */
export interface PageProps {
  title: ReactNode
  /** 标题下面的一句说明。 */
  description?: ReactNode
  /** 右侧的操作，例如“新建”按钮。 */
  actions?: ReactNode
  /** 标题上面的面包屑（来自路由的标题）。默认显示，所在的布局用 PageBreadcrumbs 关掉时不显示（例如 RailLayout 的设置中）。 */
  breadcrumbs?: boolean
  /** 标题左边的返回按钮：点击时调用它，例如内页（团队的成员页）返回列表。不设置时没有返回按钮。 */
  back?: () => void
  /** 在面包屑最后再加一级表示当前页面，例如新建页面的“新建”：路由没有自己的标题时用，前面的各级都是链接。 */
  crumb?: string
  /** 页面内容的最大宽度：narrow（720px，适合设置与短表单页）、default（占满布局允许的宽度）或具体像素值。 */
  width?: "narrow" | "default" | number
  /**
   * 页内的标签：内容多的页面分成几个标签，在标题区下面切换。当前的标签记在地址的 ?tab= 中（第一个标签不写），
   * 刷新、分享链接后还在这个标签；路由设置了 validateSearch 时要保留 tab 参数。
   * 没打开过的标签不渲染，打开过的保留状态。children 在标签的上面，例如整个页面都要看到的提示。
   */
  tabs?: PageTab[]
  children?: ReactNode
}

/** 页面：面包屑、标题区加内容；有 tabs 时内容分成几个页内标签。 */
export function Page({
  title,
  description,
  actions,
  breadcrumbs,
  back,
  crumb,
  width,
  tabs,
  children,
}: PageProps): ReactNode {
  const t = useText()
  const nav = useNavControl()
  const showNavToggle = Boolean(nav && nav.hasNav !== false && !nav.open)
  const crumbsByDefault = useContext(PageBreadcrumbsContext)
  const showCrumbs = breadcrumbs ?? crumbsByDefault
  const maw = width === "narrow" ? 720 : typeof width === "number" ? width : undefined
  return (
    <Stack gap="md" maw={maw}>
      <Stack gap={10}>
        {showCrumbs && <Crumbs extra={crumb} />}
        <div className="tn-page-header">
          <div className="tn-page-heading">
            <Group gap={6} wrap="nowrap">
              {showNavToggle && nav && (
                <Tooltip label={t("tenon.rail.expand_nav")} openDelay={350}>
                  <ActionIcon
                    variant="subtle"
                    color="gray"
                    aria-label={t("tenon.rail.expand_nav")}
                    onClick={nav.openNav}
                    ml={-6}
                  >
                    <IconLayoutSidebarLeftExpand size={20} stroke={1.6} />
                  </ActionIcon>
                </Tooltip>
              )}
              {back && (
                <ActionIcon
                  variant="subtle"
                  color="gray"
                  aria-label={t("tenon.common.back")}
                  onClick={back}
                  ml={showNavToggle ? undefined : -6}
                >
                  <IconArrowLeft size={20} stroke={1.6} />
                </ActionIcon>
              )}
              <Title order={2} className="tn-page-title">
                {title}
              </Title>
            </Group>
            {description && (
              <Text c="dimmed" size="sm" mt={4}>
                {description}
              </Text>
            )}
          </div>
          {actions && <div className="tn-page-actions">{actions}</div>}
        </div>
      </Stack>
      {children}
      {tabs && tabs.length > 0 && <PageTabs tabs={tabs} />}
    </Stack>
  )
}

/** 页内标签：当前的标签记在地址中，切换时立即展示对应面板并保留已访问面板的状态。 */
function PageTabs({ tabs }: { tabs: PageTab[] }): ReactNode {
  const search = useSearch({ strict: false }) as Record<string, unknown>
  const navigate = useNavigate()
  const first = tabs[0].value
  const current = tabs.some((t) => t.value === search.tab) ? (search.tab as string) : first
  const visited = useRef(new Set<string>())
  visited.current.add(current)
  const select = (value: string | null) => {
    if (!value || value === current) return
    void navigate({
      to: ".",
      search: ((prev: Record<string, unknown>) => ({ ...prev, tab: value === first ? undefined : value })) as never,
      replace: true,
    })
  }
  return (
    <Tabs value={current} onChange={select} className="tn-page-tabs">
      <Tabs.List>
        {tabs.map((t) => (
          <Tabs.Tab
            key={t.value}
            value={t.value}
            rightSection={
              t.count ? (
                <Badge size="sm" variant="light" color={t.alert ? "red" : "gray"} circle={t.count < 10}>
                  {t.count}
                </Badge>
              ) : undefined
            }
          >
            {t.label}
          </Tabs.Tab>
        ))}
      </Tabs.List>
      {tabs.map((t) => (
        <div
          key={t.value}
          role="tabpanel"
          hidden={t.value !== current}
          style={{ paddingTop: "var(--mantine-spacing-md)" }}
        >
          {visited.current.has(t.value) ? t.content : null}
        </div>
      ))}
    </Tabs>
  )
}

/** 面包屑：前序层级为链接，最后一级以纯文本显示当前页面。 */
function Crumbs({ extra }: { extra?: string }): ReactNode {
  const { crumbs: matched } = useNavigation()
  const crumbs = extra ? [...matched, { title: extra, path: "" }] : matched
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
