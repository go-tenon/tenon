// 官网的演示：落地页区块在四种风格下的样子。地址的 ?landing= 选择风格，?scheme=dark 用暗色。

import "./landing.css"
import {
  CallToAction,
  CodeWindow,
  Features,
  Hero,
  type Landing as LandingStyle,
  LogoCloud,
  landings,
  ProductShot,
  Stats,
  Testimonial,
} from "@go-tenon/ui"
import { Button } from "@mantine/core"
import {
  IconArrowRight,
  IconBox,
  IconBraces,
  IconLayoutDashboard,
  IconShieldLock,
  IconSparkles,
  IconStack2,
} from "@tabler/icons-react"
import type { ReactNode } from "react"

const code = `api.Resource(rest.Resource{
    Name:    "order",
    List:    "order-list",
    Update:  "order-update",
    Columns: []string{"number", "customer", "status", "amount"},
    Filters: []string{"status"},
    Actions: []rest.Action{{Name: "refund", Confirm: true}},
})`

const terminal = `$ tenon init shop
  ✓ 生成 Go 后端、迁移和配置
$ tenon add frontend admin --style admin
  ✓ 管理后台挂载在 /admin/
$ tenon build
  ✓ 编译 2 个前端，嵌入 bin/shop（38 MB）`

/** 当前地址选择的落地页风格。 */
export function landingStyle(): LandingStyle {
  const l = new URLSearchParams(location.search).get("landing")
  return landings.includes(l as LandingStyle) ? (l as LandingStyle) : "classic"
}

export function Landing(): ReactNode {
  const style = landingStyle()
  const features = [
    {
      icon: <IconBraces size={20} />,
      title: "接口即类型",
      description: "在 Go 中声明接口，生成 TypeScript 客户端、校验规则和权限。前端写错一个字段，编译时就报错。",
    },
    {
      icon: <IconLayoutDashboard size={20} />,
      title: "后台按声明渲染",
      description: "声明一个资源，列表、筛选、表单和按权限显示的按钮全部生成，不用写页面代码。",
      wide: style === "dusk",
      children: style === "dusk" ? <CodeWindow title="routes.go" code={code} /> : undefined,
    },
    {
      icon: <IconBox size={20} />,
      title: "一个二进制",
      description: "前端编译后嵌入 Go 程序，服务端渲染由 Go 托管。部署只拷贝一个文件，回滚也是。",
    },
    {
      icon: <IconShieldLock size={20} />,
      title: "安全是默认值",
      description: "HttpOnly 会话、CSRF、限流、审计日志和二次验证开箱即用，不需要再拼凑中间件。",
    },
    {
      icon: <IconStack2 size={20} />,
      title: "可靠的后台任务",
      description: "基于 Postgres 的队列，与业务在同一个事务中投递；失败自动重试，可以在面板中排查。",
    },
    {
      icon: <IconSparkles size={20} />,
      title: "为 AI 准备好",
      description: "流式对话、工具调用、人工审批和向量检索，与权限和审计接在一起。",
    },
  ]
  const visual =
    style === "ink" ? (
      <div className="pg-split">
        <CodeWindow tone="terminal" title="~/code" code={terminal} />
        <CodeWindow title="internal/order/routes.go" code={code} />
      </div>
    ) : (
      <ProductShot url="admin.acme.cn/orders">
        <AppMock />
      </ProductShot>
    )
  return (
    <>
      <Hero
        badge="1.0 正式发布 · 页面服务端渲染"
        title={
          <>
            一个后端，
            <br />
            <em>交付整个产品</em>
          </>
        }
        description="Tenon 是面向中小团队的 Go 框架。接口、后台、官网和任务队列在同一个仓库、同一个二进制里，前后端类型一致，部署只有一个文件。"
        actions={
          <>
            <Button size="md" rightSection={<IconArrowRight size={16} />}>
              开始使用
            </Button>
            <Button size="md" variant="default">
              阅读文档
            </Button>
          </>
        }
        note="go install github.com/go-tenon/tenon/cmd/tenon@latest"
        visual={visual}
      />
      <LogoCloud
        title="正在生产环境中使用 Tenon 的团队"
        items={["云栈科技", "北辰物流", "青禾教育", "拾光电商", "远帆医疗", "衡石数据"]}
      />
      <Features
        eyebrow="为什么是 Tenon"
        title="少写一半代码，多交付一倍产品"
        description="Go 是唯一的事实来源。改一次后端，客户端、校验、权限和后台页面全部跟上。"
        items={features}
      />
      <Stats
        items={[
          { value: "1", unit: "个文件", label: "部署产物" },
          { value: "97", unit: "ms", label: "SSR 进程就绪" },
          { value: "0", unit: "份", label: "重复的校验规则" },
          { value: "3", unit: "套", label: "开箱的界面预设" },
        ]}
      />
      <Testimonial
        quote="以前一个需求要在后端、管理后台和官网改三遍。换到 Tenon 之后，Go 里改一次，三个前端的类型和表单一起更新。"
        name="林舟"
        title="拾光电商 技术负责人"
      />
      <CallToAction
        title="今天就开始构建"
        description="五分钟生成一个带登录、后台和官网的完整项目。"
        actions={
          <Button size="md" rightSection={<IconArrowRight size={16} />}>
            免费开始
          </Button>
        }
      />
    </>
  )
}

/** 切换风格和配色：改地址后重新加载。 */
export function LandingSwitcher(): ReactNode {
  const q = new URLSearchParams(location.search)
  const current = landingStyle()
  const dark = q.get("scheme") === "dark"
  const href = (changes: Record<string, string>) => {
    const next = new URLSearchParams(q)
    for (const [k, v] of Object.entries(changes)) next.set(k, v)
    return `?${next}`
  }
  const names: Record<LandingStyle, string> = { classic: "经典", dusk: "暮光", ink: "墨白", mist: "云雾" }
  return (
    <nav className="pg-switcher" aria-label="落地页风格">
      {landings.map((l) => (
        <a key={l} href={href({ landing: l })} data-active={l === current || undefined}>
          {names[l]}
        </a>
      ))}
      <span />
      <a href={href({ scheme: dark ? "light" : "dark" })}>{dark ? "暗色" : "亮色"}</a>
    </nav>
  )
}

const rows = [
  { no: "#10482", who: "陈思远", status: "已支付", tone: "ok", amount: "¥1,280.00" },
  { no: "#10481", who: "王一凡", status: "待发货", tone: "info", amount: "¥5,059.00" },
  { no: "#10480", who: "李若溪", status: "已支付", tone: "ok", amount: "¥8,838.00" },
  { no: "#10479", who: "赵嘉禾", status: "待支付", tone: "warn", amount: "¥12,617.00" },
  { no: "#10478", who: "周子墨", status: "已退款", tone: "muted", amount: "¥16,396.00" },
]
const bars = [38, 52, 44, 61, 58, 72, 66, 80, 74, 88, 83, 96]

/** 用 div 画的后台界面，代替产品截图。 */
function AppMock(): ReactNode {
  return (
    <div className="pg-mock">
      <aside>
        <div className="pg-mock-brand">
          <b>A</b>Acme
        </div>
        {["概览", "订单", "客户", "商品", "报表", "设置"].map((t, i) => (
          <div key={t} className="pg-mock-nav" data-active={i === 1 || undefined}>
            <span />
            {t}
          </div>
        ))}
      </aside>
      <main>
        <div className="pg-mock-title">
          <div>
            <h4>订单</h4>
            <p>全部渠道，按下单时间倒序</p>
          </div>
          <div className="pg-mock-btn">新建订单</div>
        </div>
        <div className="pg-mock-kpis">
          {[
            ["今日成交额", "¥128,430", "+12.4%"],
            ["订单数", "1,284", "+8.1%"],
            ["退款率", "1.8%", "-0.3%"],
          ].map(([k, v, d]) => (
            <div key={k}>
              <span>{k}</span>
              <strong>{v}</strong>
              <em>{d}</em>
            </div>
          ))}
          <div className="pg-mock-chart">
            {bars.map((h, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: 固定的演示数据
              <i key={i} style={{ height: `${h}%` }} />
            ))}
          </div>
        </div>
        <div className="pg-mock-table">
          {rows.map((r) => (
            <div key={r.no}>
              <span>{r.no}</span>
              <span>{r.who}</span>
              <span className="pg-mock-status" data-tone={r.tone}>
                {r.status}
              </span>
              <span>{r.amount}</span>
            </div>
          ))}
        </div>
      </main>
    </div>
  )
}
