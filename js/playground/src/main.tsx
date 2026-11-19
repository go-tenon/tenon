import "@fontsource-variable/inter/index.css"
import "@fontsource/noto-sans-sc/400.css"
import "@fontsource/noto-sans-sc/500.css"
import "@fontsource/noto-sans-sc/700.css"
import "@go-tenon/ui/styles.css"
import { createSession, type OperationLike } from "@go-tenon/client"
import {
  AnchorLink,
  type App,
  AppRoot,
  BrowserSignIn,
  CenteredAuthLayout,
  createApp,
  createUiTheme,
  DocsLayout,
  ExportButton,
  FrameLayout,
  ImportButton,
  LanguageMenu,
  LoginForm,
  NotFoundPage,
  NotificationsMenu,
  type NotificationsSource,
  OperationsMenu,
  Page,
  QueuePanel,
  RailLayout,
  ResourceFormPage,
  ResourcePage,
  RouteError,
  type RouterContext,
  requireAuth,
  SchedulePanel,
  SettingsForm,
  SiteLayout,
  SplitAuthLayout,
  trackOperation,
  UIProvider,
  UserMenu,
  useApp,
  useFormatDateTime,
} from "@go-tenon/ui"
import {
  Badge,
  Button,
  Card,
  Container,
  Group,
  Paper,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core"
import { IconCircleCheck, IconDownload, IconPlus, IconSearch, IconSettings } from "@tabler/icons-react"
import { type AnyRoute, createRootRouteWithContext, createRoute, Link, Outlet, useParams } from "@tanstack/react-router"
import { type ReactNode, useEffect, useMemo, useState } from "react"
import { createRoot } from "react-dom/client"
import { CustomerForm } from "./customer-form"
import { PrimaryPicker, savedPrimary } from "./primary-picker"
import {
  customerExport,
  customerImport,
  customersModel,
  queryClient,
  queueApi,
  scheduleApi,
  shopSettings,
} from "./resources"

// ———————————————————————————— 共用的假数据 ————————————————————————————

const session = createSession({ load: async () => ({ account: "u1", tenant: "acme" }) })
const permissions = async () => ["orders:read", "users:manage"]

const notifications: NotificationsSource = {
  list: async () => [
    {
      id: "n1",
      title: "订单 #10482 已付款",
      body: "客户 华东分公司 支付了 ¥12800.00",
      read: false,
      created_at: "2026-11-17T09:12:00Z",
    },
    { id: "n2", title: "导出完成：11 月订单", read: false, created_at: "2026-11-17T08:40:00Z" },
    { id: "n3", title: "新成员加入：李雷", read: true, created_at: "2026-11-16T15:02:00Z" },
  ],
  markRead: async () => {},
  markAllRead: async () => {},
  unread: async () => 2,
}

const statuses: Record<string, string> = {
  待付款: "yellow",
  待发货: "bright-blue",
  已发货: "light-blue",
  已完成: "green",
  已取消: "gray",
}
const orders = Array.from({ length: 14 }, (_, i) => ({
  id: 10482 - i,
  customer: ["华东分公司", "北辰贸易", "青木设计", "远山物流", "星河零售", "海岸咨询"][i % 6],
  status: Object.keys(statuses)[i % 5],
  amount: 1280 + ((i * 3779) % 24000),
  // 和后端一样是带时区的 ISO 时间，显示时按主题的格式。
  created: `2026-11-${String(17 - (i % 9)).padStart(2, "0")}T${String(1 + (i % 8)).padStart(2, "0")}:${String((i * 7) % 60).padStart(2, "0")}:00Z`,
}))
const money = (n: number) => `¥${n.toFixed(2)}`

function Root(): ReactNode {
  return <Outlet />
}

// ———————————————————————————— FrameLayout 和 compact 预设 ————————————————————————————

const languages = [
  { value: "zh-CN", label: "简体中文" },
  { value: "zh-TW", label: "繁體中文" },
  { value: "en", label: "English" },
]

function FrameShell(): ReactNode {
  // 演示：只记下选择，不换文案。
  const [language, setLanguage] = useState("zh-CN")
  return (
    <FrameLayout
      title="Acme"
      tabs
      actions={
        <>
          <OperationsMenu />
          <NotificationsMenu source={notifications} />
          <LanguageMenu languages={languages} value={language} onChange={setLanguage} />
        </>
      }
      user={
        <UserMenu
          name="韩梅梅"
          email="hanmeimei@acme.cn"
          logout={async () => {}}
          items={[{ label: "账号设置", to: "/settings" }]}
        />
      }
    />
  )
}

function Stat({ label, value, delta }: { label: string; value: string; delta: string }): ReactNode {
  const up = !delta.startsWith("-")
  return (
    <Card>
      <Text size="xs" c="dimmed" fw={500}>
        {label}
      </Text>
      <Group align="baseline" gap={8} mt={6}>
        <Text fz={22} fw={600} style={{ letterSpacing: "-0.02em" }}>
          {value}
        </Text>
        <Text size="xs" c={up ? "green" : "red"} fw={500}>
          {delta}
        </Text>
      </Group>
    </Card>
  )
}

function OrdersTable({ rows }: { rows: typeof orders }): ReactNode {
  const formatDateTime = useFormatDateTime()
  return (
    <Paper style={{ overflow: "hidden" }}>
      <Table highlightOnHover verticalSpacing={8} horizontalSpacing="md" striped={false}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>订单号</Table.Th>
            <Table.Th>客户</Table.Th>
            <Table.Th>状态</Table.Th>
            <Table.Th>金额</Table.Th>
            <Table.Th>下单时间</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((o) => (
            <Table.Tr key={o.id}>
              <Table.Td ff="monospace">#{o.id}</Table.Td>
              <Table.Td>{o.customer}</Table.Td>
              <Table.Td>
                <Badge color={statuses[o.status]}>{o.status}</Badge>
              </Table.Td>
              <Table.Td style={{ fontVariantNumeric: "tabular-nums" }}>{money(o.amount)}</Table.Td>
              <Table.Td c="dimmed">{formatDateTime(o.created)}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Paper>
  )
}

function FrameHome(): ReactNode {
  const app = useApp()
  useEffect(() => trackOperation(app, { id: "op1", title: "导出 11 月订单", open: progress }), [app])
  return (
    <Page title="概览" description="今天的经营数据，每 5 分钟更新一次。">
      <SimpleGrid cols={{ base: 2, md: 4 }} spacing="md">
        <Stat label="今日成交额" value="¥128460" delta="+12.4%" />
        <Stat label="订单数" value="1284" delta="+3.1%" />
        <Stat label="待发货" value="86" delta="-8.0%" />
        <Stat label="退款率" value="1.2%" delta="-0.3%" />
      </SimpleGrid>
      <Title order={4}>最近的订单</Title>
      <OrdersTable rows={orders.slice(0, 6)} />
    </Page>
  )
}

function FrameOrders(): ReactNode {
  return (
    <Page
      title="订单"
      description="全部渠道的订单，按下单时间倒序。"
      actions={
        <Group gap="xs">
          <Button variant="default" leftSection={<IconDownload size={14} />}>
            导出
          </Button>
          <Button leftSection={<IconPlus size={14} />}>新建订单</Button>
        </Group>
      }
    >
      <Group gap="xs">
        <TextInput placeholder="搜索订单号或客户" leftSection={<IconSearch size={14} />} w={240} />
        <Select placeholder="状态" data={Object.keys(statuses)} w={120} clearable />
        <Select placeholder="渠道" data={["官网", "小程序", "门店"]} w={120} clearable />
      </Group>
      <OrdersTable rows={orders} />
    </Page>
  )
}

function FramePlaceholder({ title }: { title: string }): ReactNode {
  return (
    <Page title={title} description="这个页面还在建设中。">
      <Paper p="xl">
        <Text c="dimmed">内容区</Text>
      </Paper>
    </Page>
  )
}

function frameApp() {
  const root = createRootRouteWithContext<RouterContext>()({ component: Root })
  const layout = createRoute({ getParentRoute: () => root, id: "_app", beforeLoad: requireAuth, component: FrameShell })
  let order = 0
  const page = (
    parent: AnyRoute,
    path: string,
    title: string,
    icon: string | undefined,
    component: () => ReactNode,
    permission?: string,
  ) =>
    createRoute({
      getParentRoute: () => parent,
      path,
      staticData: { title, icon, permission, order: order++ },
      component,
    })
  const trade = createRoute({
    getParentRoute: () => layout,
    path: "/trade",
    staticData: { title: "交易", icon: "cart", order: 0.5 },
  })
  const customers = createRoute({
    getParentRoute: () => layout,
    path: "/customers",
    staticData: { title: "客户", icon: "team", order: 0.7 },
    validateSearch: customersModel.listSearch,
    component: () => (
      <ResourcePage
        model={customersModel}
        route={customers as never}
        description="全部渠道的客户。"
        toolbar={
          <>
            <ImportButton source={customerImport} title="导入" />
            <ExportButton source={customerExport} title="导出" />
          </>
        }
      />
    ),
  })
  const routeTree = root.addChildren([
    layout.addChildren([
      page(layout, "/", "概览", "dashboard", FrameHome),
      trade.addChildren([
        page(trade, "/orders", "订单", "order", FrameOrders),
        page(trade, "/refunds", "退款", "receipt", () => <FramePlaceholder title="退款" />),
        page(trade, "/invoices", "发票", "file-text", () => <FramePlaceholder title="发票" />),
      ]),
      page(layout, "/products", "商品", "product", () => <FramePlaceholder title="商品" />),
      // 新建和修改客户是单独的页面：客户列表路由下的 new 和 $id/edit。
      customers.addChildren([
        // 新建用自己写的表单（选了等级就预填信用额度），修改按声明渲染。
        createRoute({
          getParentRoute: () => customers,
          path: "/new",
          component: () => (
            <ResourceFormPage model={customersModel}>{(ctx) => <CustomerForm ctx={ctx} />}</ResourceFormPage>
          ),
        }),
        createRoute({
          getParentRoute: () => customers,
          path: "/$id/edit",
          component: () => <ResourceFormPage model={customersModel} />,
        }),
      ]),
      page(layout, "/users", "用户", "users", () => <FramePlaceholder title="用户" />, "users:manage"),
      page(layout, "/reports", "报表", "report", () => <FramePlaceholder title="报表" />),
      page(layout, "/queue", "队列", "list", () => <QueuePanel api={queueApi} />),
      page(layout, "/schedules", "定时任务", "clock", () => <SchedulePanel api={scheduleApi} />),
      page(layout, "/billing", "计费", "wallet", () => <FramePlaceholder title="计费" />, "billing:manage"),
      page(layout, "/settings", "设置", "settings", () => (
        <Page title="设置" description="店铺的基本信息，对所有渠道生效。">
          <SettingsForm model={shopSettings} title={null} />
        </Page>
      )),
    ]),
  ])
  const app = createApp({
    id: "playground-frame",
    routeTree,
    session,
    queryClient,
    permissions,
    router: { basepath: "/frame", defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFoundPage },
  })
  return app
}

// 演示的后台任务：大约 12 秒跑完，期间顶栏的“后台任务”按钮转圈，结束后显示完成。
async function* progress(): AsyncIterable<{ event: string; data: OperationLike }> {
  for (let i = 0; i <= 10; i++) {
    const status = i === 10 ? "completed" : "running"
    yield { event: "progress", data: { id: "op1", status, total: 10, processed: i, failed: 0 } }
    await new Promise((r) => setTimeout(r, 1_200))
  }
}

// ———————————————————————————— RailLayout 和 comfortable 预设 ————————————————————————————

function RailShell(): ReactNode {
  return (
    <RailLayout
      title="Acme"
      user={
        <UserMenu
          name="韩梅梅"
          email="hanmeimei@acme.cn"
          position="right-end"
          items={[{ label: "设置", icon: <IconSettings size={16} />, to: "/settings/profile" }]}
          logout={async () => {}}
        />
      }
    />
  )
}

/** RailLayout 中页面的占位。 */
function RailPlaceholder({ title }: { title: string }): ReactNode {
  return (
    <Page title={title}>
      <Text c="dimmed">页面的占位。</Text>
    </Page>
  )
}

function railApp() {
  const root = createRootRouteWithContext<RouterContext>()({ component: Root })
  const layout = createRoute({
    getParentRoute: () => root,
    id: "_app",
    beforeLoad: requireAuth,
    component: RailShell,
  })
  // 导航来自路由：第一级是图标栏中的模块，模块下的页面在二级导航中；/settings 下的页面从用户菜单进入，
  // 其中没有路径的分组（个人）是二级导航中的一节。
  const page = (parent: AnyRoute, path: string, title: string, icon: string, order = 0) =>
    createRoute({
      getParentRoute: () => parent,
      path,
      staticData: { title, icon, order },
      component: () => <RailPlaceholder title={title} />,
    })
  const content = createRoute({
    getParentRoute: () => layout,
    id: "content",
    staticData: { title: "内容", icon: "file", order: 1 },
  })
  const settings = createRoute({
    getParentRoute: () => layout,
    path: "/settings",
    staticData: { title: "设置", icon: "settings", order: 9 },
  })
  const personal = createRoute({ getParentRoute: () => settings, id: "personal", staticData: { title: "个人" } })
  // 租户在路径中：/t/$tenant 是没有标题的布局路由，其下页面的链接填上当前地址中的租户，例如 /t/acme/inbox；
  // 详情页 /t/$tenant/tickets/$id 自己引入了 id，不出现在菜单中。打开 /rail/t/acme/inbox 查看。
  const tenant = createRoute({ getParentRoute: () => layout, path: "/t/$tenant" })
  const ticket = createRoute({
    getParentRoute: () => tenant,
    path: "/tickets/$id",
    staticData: { title: "工单详情" },
    component: () => <RailPlaceholder title="工单详情" />,
  })
  const routeTree = root.addChildren([
    layout.addChildren([
      page(layout, "/", "首页", "home"),
      tenant.addChildren([page(tenant, "/inbox", "收件箱", "message", 2), ticket]),
      content.addChildren([page(content, "/items", "全部", "list"), page(content, "/archive", "归档", "file", 1)]),
      settings.addChildren([personal.addChildren([page(personal, "/profile", "个人资料", "user")])]),
    ]),
  ])
  return createApp({
    id: "playground-rail",
    routeTree,
    session,
    permissions,
    router: { basepath: "/rail", defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFoundPage },
  })
}

// ———————————————————————————— 登录页 ————————————————————————————

// 登录页的样式与布局无关，任何布局都可以搭配任何一种登录页。/login/ 列出全部样式，每种样式一个地址，
// 例如 /login/centered、/login/split；新增一种样式时在 loginStyles 中加一项。
/** CenteredAuthLayout：居中卡片。 */
function CenteredLogin(): ReactNode {
  return (
    <CenteredAuthLayout
      brand={
        <Group gap={10}>
          <span className="tn-brand-mark">A</span>
          <Text fw={600}>Acme</Text>
        </Group>
      }
      title="登录"
      description="欢迎回来，登录 Acme。"
      footer={
        <>
          还没有账号？<AnchorLink to="/centered">免费注册</AnchorLink>
        </>
      }
    >
      <LoginForm
        login={async () =>
          Promise.reject({ status: 401, code: "auth.invalid_credentials", detail: "邮箱或密码不对", fields: [] })
        }
        forgot={<AnchorLink to="/centered">忘记密码？</AnchorLink>}
        initialValues={{ email: "demo@example.com", password: "demo-password" }}
      />
    </CenteredAuthLayout>
  )
}

/** SplitAuthLayout：左右分栏。 */
function SplitLogin(): ReactNode {
  return (
    <SplitAuthLayout
      brand={
        <Group gap={10}>
          <span className="tn-brand-mark">A</span>
          <Text fw={600}>Acme</Text>
        </Group>
      }
      aside={
        <Stack gap="xl">
          <Stack gap="sm">
            <Title order={1} fz={36} lh={1.2}>
              把团队的工作放在一处
            </Title>
            <Text size="lg" opacity={0.8}>
              任务、文档和讨论在同一个地方，随时知道每件事进行到哪一步。
            </Text>
          </Stack>
          <Stack gap="sm">
            {["几分钟建好第一个项目", "按角色分配权限", "手机和电脑上都能用"].map((item) => (
              <Group key={item} gap="sm" wrap="nowrap">
                <IconCircleCheck size={20} stroke={1.75} />
                <Text>{item}</Text>
              </Group>
            ))}
          </Stack>
        </Stack>
      }
      asideFooter="© 2026 Acme"
      title="登录"
      description="欢迎回来，登录 Acme。"
      footer={
        <>
          还没有账号？<AnchorLink to="/split">免费注册</AnchorLink>
        </>
      }
    >
      <LoginForm
        login={async () =>
          Promise.reject({ status: 401, code: "auth.invalid_credentials", detail: "邮箱或密码不对", fields: [] })
        }
        forgot={<AnchorLink to="/split">忘记密码？</AnchorLink>}
      />
    </SplitAuthLayout>
  )
}

/** BrowserSignIn：桌面端、App 的登录方式，在系统浏览器中登录。这里点击后等两秒，然后提示登录没有完成。 */
function BrowserLogin(): ReactNode {
  return (
    <CenteredAuthLayout
      brand={
        <Group gap={10}>
          <span className="tn-brand-mark">A</span>
          <Text fw={600}>Acme</Text>
        </Group>
      }
      title="登录"
      description="欢迎回来，登录 Acme。"
    >
      <BrowserSignIn
        signIn={() => new Promise((_, reject) => setTimeout(() => reject(new Error("cancelled")), 2000))}
      />
    </CenteredAuthLayout>
  )
}

const loginStyles = [
  { path: "/centered", name: "CenteredAuthLayout", description: "居中卡片", component: CenteredLogin },
  { path: "/split", name: "SplitAuthLayout", description: "左右分栏", component: SplitLogin },
  {
    path: "/browser",
    name: "BrowserSignIn",
    description: "桌面端、App 的登录方式：在系统浏览器中登录，可以放在任何一种登录页中",
    component: BrowserLogin,
  },
]

/** /login/ 列出全部登录页样式。 */
function LoginStyles(): ReactNode {
  return (
    <Container size="sm" py={64}>
      <Stack gap="md">
        <Title order={2}>登录页</Title>
        {loginStyles.map((style) => (
          <Card key={style.path} component={Link} to={style.path}>
            <Text fw={600}>{style.name}</Text>
            <Text size="sm" c="dimmed">
              {style.description}
            </Text>
          </Card>
        ))}
      </Stack>
    </Container>
  )
}

function loginApp() {
  const root = createRootRouteWithContext<RouterContext>()({ component: Root })
  const routeTree = root.addChildren([
    createRoute({ getParentRoute: () => root, path: "/", component: LoginStyles }),
    ...loginStyles.map(({ path, component }) => createRoute({ getParentRoute: () => root, path, component })),
  ])
  return createApp({
    id: "playground-login",
    routeTree,
    router: { basepath: "/login", defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFoundPage },
  })
}

// ———————————————————————————— 官网和文档 ————————————————————————————

const brand = (
  <Group gap={10}>
    <span className="tn-brand-mark">T</span>
    <span>Tenon</span>
  </Group>
)

function Site(): ReactNode {
  return (
    <SiteLayout
      brand={brand}
      nav={[
        { label: "首页", to: "/" },
        { label: "文档", to: "/docs/getting-started" },
      ]}
      actions={
        <>
          <Button variant="subtle" color="gray" size="sm" component="a" href="/login/">
            登录页
          </Button>
          <Button variant="subtle" color="gray" size="sm" component="a" href="/frame/">
            FrameLayout
          </Button>
          <Button size="sm" component="a" href="/rail/">
            RailLayout
          </Button>
        </>
      }
    />
  )
}

/** 首页的占位：做实际产品时换成自己的落地页。 */
function Home(): ReactNode {
  return (
    <Container size="sm" py={96}>
      <Stack gap="md" align="flex-start">
        <Title order={1}>Tenon</Title>
        <Text c="dimmed">这里是首页的占位，做实际产品时换成自己的落地页。页头和文档页是现成的。</Text>
        <Button component={Link} to="/docs/getting-started">
          阅读文档
        </Button>
      </Stack>
    </Container>
  )
}

const docsNav = [
  {
    title: "开始",
    items: [
      { title: "快速开始", to: "/docs/getting-started" },
      { title: "项目结构", to: "/docs/structure" },
    ],
  },
  {
    title: "指南",
    items: [
      { title: "配置", to: "/docs/configuration" },
      { title: "部署", to: "/docs/deployment" },
    ],
  },
]

function Doc(): ReactNode {
  const { slug } = useParams({ strict: false }) as { slug: string }
  const title = docsNav.flatMap((s) => s.items).find((i) => i.to.endsWith(slug))?.title ?? slug
  return (
    <DocsLayout nav={docsNav}>
      <h1>{title}</h1>
      <p>
        这一页介绍怎样用 <code>tenon init</code> 创建项目、启动开发服务器，并在浏览器中看到第一个页面。读完大约需要 5
        分钟。
      </p>
      <h2>安装</h2>
      <p>需要 Go 1.27 和 Bun 1.4。安装好以后，在终端中运行：</p>
      <pre>
        <code>{"go install github.com/go-tenon/tenon/cmd/tenon@latest\ntenon init blog"}</code>
      </pre>
      <h3>检查环境</h3>
      <p>
        <code>tenon doctor</code> 会检查数据库、NATS 和 Bun 是否可用，并说明怎样修改。
      </p>
      <h2>启动</h2>
      <p>进入项目目录，运行开发服务器。修改 Go 代码或页面后，浏览器会自动刷新。</p>
      <blockquote>开发服务器监听 8080 端口。端口被占用时，用 --addr 换一个。</blockquote>
      <h2>下一步</h2>
      <ul>
        <li>写第一个接口</li>
        <li>加上登录</li>
        <li>部署到服务器</li>
      </ul>
    </DocsLayout>
  )
}

function siteApp() {
  const root = createRootRouteWithContext<RouterContext>()({ component: Site })
  const routeTree = root.addChildren([
    createRoute({ getParentRoute: () => root, path: "/", component: Home }),
    createRoute({ getParentRoute: () => root, path: "/docs/$slug", component: Doc }),
  ])
  return createApp({ id: "playground-site", routeTree, router: { defaultNotFoundComponent: NotFoundPage } })
}

// ———————————————————————————— 入口 ————————————————————————————

// 布局的演示各用一套预设：/frame/ 是 FrameLayout 和 compact，/rail/ 是 RailLayout 和 comfortable，
// 其余是 SiteLayout、DocsLayout 和 spacious；登录页（/login/）用默认的 comfortable。
const path = location.pathname
const demos = { frame: frameApp, rail: railApp, login: loginApp }
const demo =
  (Object.keys(demos) as (keyof typeof demos)[]).find((d) => path === `/${d}` || path.startsWith(`/${d}/`)) ?? "site"
const app: App = demo === "site" ? siteApp() : demos[demo]()
const preset = demo === "frame" ? "compact" : demo === "site" ? "spacious" : "comfortable"
// 日期时间的格式是应用的设置：表格多的演示用短格式。
const datetimeFormat = demo === "frame" ? "MM/DD HH:mm" : undefined
const scheme = new URLSearchParams(location.search).get("scheme")

/** 按右下角选择的主题色生成主题，换颜色时不重新加载页面。 */
function Playground(): ReactNode {
  const [primary, setPrimary] = useState(savedPrimary)
  const theme = useMemo(() => createUiTheme({ preset, primary, datetimeFormat }), [primary])
  return (
    <UIProvider theme={theme} defaultColorScheme={scheme === "dark" ? "dark" : "light"}>
      <AppRoot app={app} />
      <PrimaryPicker value={primary} onChange={setPrimary} />
    </UIProvider>
  )
}

createRoot(document.getElementById("root") as HTMLElement).render(<Playground />)
