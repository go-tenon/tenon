import "@fontsource-variable/inter/index.css"
import "@fontsource/noto-sans-sc/400.css"
import "@fontsource/noto-sans-sc/500.css"
import "@fontsource/noto-sans-sc/700.css"
import "@go-tenon/ui/styles.css"
import { createSession, type OperationLike } from "@go-tenon/client"
import {
  AnchorLink,
  type App,
  AppLayout,
  AppRoot,
  AuthLayout,
  createApp,
  createUiTheme,
  DocsLayout,
  ExportButton,
  ImportButton,
  LanguageMenu,
  LoginForm,
  NotFoundPage,
  NotificationsMenu,
  type NotificationsSource,
  OperationsMenu,
  Page,
  QueuePanel,
  ResourceFormPage,
  ResourcePage,
  RouteError,
  type RouterContext,
  requireAuth,
  SchedulePanel,
  SettingsForm,
  SiteLayout,
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
  Progress,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  Textarea,
  TextInput,
  ThemeIcon,
  Title,
} from "@mantine/core"
import { IconDownload, IconPlus, IconSearch, IconStack2 } from "@tabler/icons-react"
import { type AnyRoute, createRootRouteWithContext, createRoute, Link, Outlet, useParams } from "@tanstack/react-router"
import { type ReactNode, useEffect, useMemo, useState } from "react"
import { createRoot } from "react-dom/client"
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
const tenancy = {
  tenants: async () => [
    { id: "acme", name: "Acme 科技" },
    { id: "globex", name: "Globex 零售" },
  ],
}

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

// ———————————————————————————— 运营后台 ————————————————————————————

const languages = [
  { value: "zh-CN", label: "简体中文" },
  { value: "zh-TW", label: "繁體中文" },
  { value: "en", label: "English" },
]

function AdminShell(): ReactNode {
  // 演示：只记下选择，不换文案。
  const [language, setLanguage] = useState("zh-CN")
  return (
    <AppLayout
      title="Acme 运营后台"
      tabs
      actions={
        <>
          <OperationsMenu />
          <NotificationsMenu source={notifications} />
          <LanguageMenu languages={languages} value={language} onChange={setLanguage} />
        </>
      }
      user={<UserMenu name="韩梅梅" logout={async () => {}} items={[{ label: "账号设置", to: "/settings" }]} />}
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

function AdminHome(): ReactNode {
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

function AdminOrders(): ReactNode {
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

function AdminPlaceholder({ title }: { title: string }): ReactNode {
  return (
    <Page title={title} description="这个页面还在建设中。">
      <Paper p="xl">
        <Text c="dimmed">内容区</Text>
      </Paper>
    </Page>
  )
}

function adminApp() {
  const root = createRootRouteWithContext<RouterContext>()({ component: Root })
  const layout = createRoute({ getParentRoute: () => root, id: "_app", beforeLoad: requireAuth, component: AdminShell })
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
      page(layout, "/", "概览", "dashboard", AdminHome),
      trade.addChildren([
        page(trade, "/orders", "订单", "order", AdminOrders),
        page(trade, "/refunds", "退款", "receipt", () => <AdminPlaceholder title="退款" />),
        page(trade, "/invoices", "发票", "file-text", () => <AdminPlaceholder title="发票" />),
      ]),
      page(layout, "/products", "商品", "product", () => <AdminPlaceholder title="商品" />),
      // 新建和修改客户是单独的页面：客户列表路由下的 new 和 $id/edit。
      customers.addChildren([
        createRoute({
          getParentRoute: () => customers,
          path: "/new",
          component: () => <ResourceFormPage model={customersModel} />,
        }),
        createRoute({
          getParentRoute: () => customers,
          path: "/$id/edit",
          component: () => <ResourceFormPage model={customersModel} />,
        }),
      ]),
      page(layout, "/users", "用户", "users", () => <AdminPlaceholder title="用户" />, "users:manage"),
      page(layout, "/reports", "报表", "report", () => <AdminPlaceholder title="报表" />),
      page(layout, "/queue", "队列", "list", () => <QueuePanel api={queueApi} />),
      page(layout, "/schedules", "定时任务", "clock", () => <SchedulePanel api={scheduleApi} />),
      page(layout, "/billing", "计费", "wallet", () => <AdminPlaceholder title="计费" />, "billing:manage"),
      page(layout, "/settings", "设置", "settings", () => (
        <Page title="设置" description="店铺的基本信息，对所有渠道生效。">
          <SettingsForm model={shopSettings} title={null} />
        </Page>
      )),
    ]),
    createRoute({ getParentRoute: () => root, path: "/login", component: LoginPage }),
  ])
  const app = createApp({
    id: "playground-admin",
    routeTree,
    session,
    queryClient,
    permissions,
    router: { basepath: "/admin", defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFoundPage },
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

function LoginPage(): ReactNode {
  return (
    <AuthLayout
      brand={
        <Group gap={10}>
          <span className="tn-brand-mark">A</span>
          <Text fw={600}>Acme</Text>
        </Group>
      }
      title="登录"
      description="使用工作邮箱登录 Acme 运营后台。"
      footer={
        <>
          还没有账号？<AnchorLink to="/">联系管理员</AnchorLink>
        </>
      }
    >
      <LoginForm
        login={async () =>
          Promise.reject({ status: 401, code: "auth.invalid_credentials", detail: "邮箱或密码不对", fields: [] })
        }
        forgot={<AnchorLink to="/">忘记密码？</AnchorLink>}
      />
    </AuthLayout>
  )
}

// ———————————————————————————— SaaS 控制台 ————————————————————————————

function ConsoleShell(): ReactNode {
  return (
    <AppLayout
      title="Acme Cloud"
      maxWidth={1200}
      actions={<NotificationsMenu source={notifications} />}
      user={<UserMenu name="韩梅梅" logout={async () => {}} />}
    />
  )
}

function ConsoleHome(): ReactNode {
  const projects = [
    { name: "官网", env: "生产", usage: 64, status: "运行中" },
    { name: "订单服务", env: "生产", usage: 82, status: "运行中" },
    { name: "数据分析", env: "预发布", usage: 23, status: "已暂停" },
  ]
  return (
    <Page
      title="概览"
      description="Acme 科技 · 专业版"
      actions={<Button leftSection={<IconPlus size={16} />}>新建项目</Button>}
    >
      <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="lg">
        {projects.map((p) => (
          <Card key={p.name}>
            <Group justify="space-between">
              <Group gap="sm">
                <ThemeIcon variant="light" radius="md" size={36}>
                  <IconStack2 size={18} />
                </ThemeIcon>
                <div>
                  <Text fw={600}>{p.name}</Text>
                  <Text size="xs" c="dimmed">
                    {p.env}环境
                  </Text>
                </div>
              </Group>
              <Badge color={p.status === "运行中" ? "green" : "gray"} variant="dot">
                {p.status}
              </Badge>
            </Group>
            <Text size="xs" c="dimmed" mt="lg" mb={6}>
              本月用量 {p.usage}%
            </Text>
            <Progress value={p.usage} size="sm" />
          </Card>
        ))}
      </SimpleGrid>
      <Card>
        <Title order={4} mb={4}>
          团队设置
        </Title>
        <Text size="sm" c="dimmed" mb="lg">
          修改后对团队的所有成员生效。
        </Text>
        <Stack gap="md" maw={480}>
          <TextInput label="团队名称" defaultValue="Acme 科技" />
          <Textarea label="简介" placeholder="一句话介绍你的团队" autosize minRows={2} />
          <Switch label="要求成员使用二次验证" defaultChecked />
          <Group>
            <Button>保存</Button>
          </Group>
        </Stack>
      </Card>
    </Page>
  )
}

function consoleApp() {
  const root = createRootRouteWithContext<RouterContext>()({ component: Root })
  const layout = createRoute({
    getParentRoute: () => root,
    id: "_app",
    beforeLoad: requireAuth,
    component: ConsoleShell,
  })
  let order = 0
  const page = (path: string, title: string, icon: string) =>
    createRoute({
      getParentRoute: () => layout,
      path,
      staticData: { title, icon, order: order++ },
      component: path === "/" ? ConsoleHome : () => <AdminPlaceholder title={title} />,
    })
  const routeTree = root.addChildren([
    layout.addChildren([
      page("/", "概览", "home"),
      page("/projects", "项目", "apps"),
      page("/members", "成员", "team"),
      page("/api-keys", "API 密钥", "key"),
      page("/billing", "账单", "payment"),
      page("/settings", "设置", "settings"),
    ]),
  ])
  return createApp({
    id: "playground-console",
    routeTree,
    session,
    permissions,
    tenancy,
    router: { basepath: "/console", defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFoundPage },
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
          <Button variant="subtle" color="gray" size="sm" component="a" href="/admin/login">
            登录
          </Button>
          <Button size="sm" component="a" href="/console/">
            免费试用
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

const path = location.pathname
const preset = path.startsWith("/admin") ? "admin" : path.startsWith("/console") ? "console" : "site"
const app: App = preset === "admin" ? adminApp() : preset === "console" ? consoleApp() : siteApp()
const scheme = new URLSearchParams(location.search).get("scheme")

/** 按右下角选择的主题色生成主题，换颜色时不重新加载页面。 */
function Playground(): ReactNode {
  const [primary, setPrimary] = useState(savedPrimary)
  const theme = useMemo(() => createUiTheme({ preset, primary }), [primary])
  return (
    <UIProvider theme={theme} defaultColorScheme={scheme === "dark" ? "dark" : "light"}>
      <AppRoot app={app} />
      <PrimaryPicker value={primary} onChange={setPrimary} />
    </UIProvider>
  )
}

createRoot(document.getElementById("root") as HTMLElement).render(<Playground />)
