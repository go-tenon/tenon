// E2E 用的应用：几个页面放在开启多标签的布局之下，各自有需要保留的状态（输入框、计数、计时器）。
import { createSession } from "@go-tenon/client"
import {
  AppRoot,
  createApp,
  type RouterContext,
  TabsOutlet,
  useTabActive,
  useTabShortcuts,
  useTabs,
  useTabTitle,
  useUnsavedChanges,
} from "@go-tenon/shell"
import { createRootRouteWithContext, createRoute, Link, Outlet, useNavigate } from "@tanstack/react-router"
import { type ReactNode, useEffect, useState } from "react"
import { createRoot } from "react-dom/client"

function Layout(): ReactNode {
  const tabs = useTabs()
  useTabShortcuts()
  return (
    <div>
      <nav aria-label="菜单" style={{ display: "flex", gap: 8 }}>
        <Link to="/">首页</Link>
        <Link to="/about">关于</Link>
        <Link to="/counter">计数器</Link>
        <Link to="/edit/$id" params={{ id: "a" }}>
          编辑 A
        </Link>
        <Link to="/items/$id" params={{ id: "a" }}>
          实体 A
        </Link>
        <Link to="/items/$id" params={{ id: "b" }}>
          实体 B
        </Link>
        <Link to="/list">列表</Link>
        <Link to="/ticker">计时器</Link>
        <Link to="/result">结果</Link>
      </nav>
      <div role="tablist" aria-label="标签" style={{ display: "flex", gap: 8 }}>
        {tabs.tabs.map((t) => (
          <span key={t.key} data-testid="tab" data-pinned={t.pinned} data-dirty={t.dirty}>
            <button type="button" role="tab" aria-selected={t.active} onClick={() => void tabs.activate(t.key)}>
              {t.title}
            </button>
            {t.closable && (
              <button type="button" aria-label={`关闭 ${t.title}`} onClick={() => void tabs.close(t.key)}>
                ×
              </button>
            )}
            <button type="button" aria-label={`刷新 ${t.title}`} onClick={() => void tabs.refresh(t.key)}>
              ↻
            </button>
            <button
              type="button"
              aria-label={`${t.pinned ? "取消固定" : "固定"} ${t.title}`}
              onClick={() => (t.pinned ? tabs.unpin(t.key) : tabs.pin(t.key))}
            >
              📌
            </button>
          </span>
        ))}
        <button type="button" onClick={() => void tabs.closeAll()}>
          关闭全部
        </button>
      </div>
      <main>
        <TabsOutlet />
      </main>
    </div>
  )
}

function Counter(): ReactNode {
  const [n, setN] = useState(0)
  return (
    <div>
      <output data-testid="count">{n}</output>
      <button type="button" onClick={() => setN(n + 1)}>
        加一
      </button>
    </div>
  )
}

function Edit(): ReactNode {
  const { id } = edit.useParams()
  const [draft, setDraft] = useState("")
  useTabTitle(`编辑 ${id.toUpperCase()}`)
  useUnsavedChanges(draft !== "")
  return (
    <label>
      草稿
      <input value={draft} onChange={(e) => setDraft(e.target.value)} />
    </label>
  )
}

function Item(): ReactNode {
  const { id } = item.useParams()
  useTabTitle(`实体 ${id.toUpperCase()}`)
  return (
    <div>
      <p data-testid="item-id">{id}</p>
      <label>
        备注
        <input />
      </label>
    </div>
  )
}

function List(): ReactNode {
  const { keyword = "", page = 1 } = list.useSearch()
  const navigate = useNavigate({ from: "/list" })
  const [input, setInput] = useState(keyword)
  return (
    <div>
      <label>
        关键词
        <input value={input} onChange={(e) => setInput(e.target.value)} />
      </label>
      <button type="button" onClick={() => void navigate({ search: { keyword: input, page: 1 } })}>
        搜索
      </button>
      <button type="button" onClick={() => void navigate({ search: { keyword, page: page + 1 } })}>
        下一页
      </button>
      <p data-testid="page">{`page=${page}`}</p>
      <Link to="/items/$id" params={{ id: "a" }}>
        打开实体 A
      </Link>
    </div>
  )
}

function Ticker(): ReactNode {
  const [n, setN] = useState(0)
  const active = useTabActive()
  useEffect(() => {
    const timer = setInterval(() => setN((v) => v + 1), 50)
    return () => clearInterval(timer)
  }, [])
  return (
    <div>
      <output data-testid="ticks">{n}</output>
      <p data-testid="ticker-active">{String(active)}</p>
    </div>
  )
}

const root = createRootRouteWithContext<RouterContext>()({ component: Outlet })
const layout = createRoute({ getParentRoute: () => root, id: "_app", component: Layout })
const page = <P extends string>(path: P, title: string, component: () => ReactNode, tab?: boolean) =>
  createRoute({ getParentRoute: () => layout, path, staticData: { title, tab }, component })
const edit = createRoute({
  getParentRoute: () => layout,
  path: "/edit/$id",
  staticData: { title: "编辑" },
  component: Edit,
})
const item = createRoute({
  getParentRoute: () => layout,
  path: "/items/$id",
  staticData: { title: "实体" },
  component: Item,
})
const list = createRoute({
  getParentRoute: () => layout,
  path: "/list",
  staticData: { title: "列表" },
  validateSearch: (s: Record<string, unknown>): { keyword?: string; page?: number } => ({
    keyword: typeof s.keyword === "string" && s.keyword ? s.keyword : undefined,
    page: Number(s.page) > 1 ? Number(s.page) : undefined,
  }),
  component: List,
})
const routeTree = root.addChildren([
  layout.addChildren([
    page("/", "首页", () => <p>首页内容</p>),
    page("/about", "关于", () => <p>关于内容</p>),
    page("/counter", "计数器", Counter),
    page("/ticker", "计时器", Ticker),
    page("/result", "结果", () => <p>结果内容</p>, false),
    edit,
    item,
    list,
  ]),
])

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createApp<typeof routeTree>>["router"]
  }
}

const session = createSession({ load: async () => ({ account: "u1" }) })
const app = createApp({ id: "e2e", routeTree, session })

void session.signIn({ account: "u1" }).then(() => {
  const el = document.getElementById("root")
  if (el) createRoot(el).render(<AppRoot app={app} />)
})
