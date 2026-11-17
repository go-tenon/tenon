// 落地页的区块：首屏、产品截图、代码窗口、客户标识、特性、数据、用户评价和行动号召。
// 它们放在 SiteLayout 之中，外观随主题的落地页风格（createUiTheme 的 landing：classic、dusk、ink、mist）变化，
// 内容由应用传入。按钮直接用 Mantine 的 Button，风格同样作用于它们。

import { Container } from "@mantine/core"
import type { CSSProperties, ReactNode } from "react"

/** Section 的选项：区块的标题区。 */
export interface SectionProps {
  /** 标题上方的一行小字，例如“为什么是我们”。 */
  eyebrow?: ReactNode
  title?: ReactNode
  description?: ReactNode
  children?: ReactNode
}

/** 一个区块：标题区和内容，统一宽度和上下间距。 */
export function Section({ eyebrow, title, description, children }: SectionProps): ReactNode {
  return (
    <section className="tn-lp-section">
      <Container size="lg">
        {(eyebrow || title || description) && (
          <div className="tn-lp-head">
            {eyebrow && <p className="tn-lp-eyebrow">{eyebrow}</p>}
            {title && <h2 className="tn-lp-h2">{title}</h2>}
            {description && <p className="tn-lp-desc">{description}</p>}
          </div>
        )}
        {children}
      </Container>
    </section>
  )
}

/** Hero 的选项。 */
export interface HeroProps {
  /** 标题上方的短公告，例如“1.0 正式发布”。 */
  badge?: ReactNode
  /** 主标题；需要换行时用 <br />。 */
  title: ReactNode
  description?: ReactNode
  /** 按钮，通常是一个主要按钮和一个次要按钮（variant="default"）。 */
  actions?: ReactNode
  /** 按钮下方的一行，例如安装命令、几个卖点。 */
  note?: ReactNode
  /** 首屏下方的画面，例如 ProductShot、CodeWindow。 */
  visual?: ReactNode
}

/** 首屏：公告、主标题、说明、按钮和产品画面。 */
export function Hero({ badge, title, description, actions, note, visual }: HeroProps): ReactNode {
  return (
    <section className="tn-lp-hero">
      <div className="tn-lp-hero-bg" aria-hidden />
      <Container size="lg" className="tn-lp-hero-inner">
        {badge && <div className="tn-lp-badge">{badge}</div>}
        <h1 className="tn-lp-title">{title}</h1>
        {description && <p className="tn-lp-lead">{description}</p>}
        {actions && <div className="tn-lp-actions">{actions}</div>}
        {note && <div className="tn-lp-note">{note}</div>}
      </Container>
      {visual && (
        <Container size="lg" className="tn-lp-visual">
          {visual}
        </Container>
      )}
    </section>
  )
}

/** ProductShot 的选项。 */
export interface ProductShotProps {
  /** 地址栏中显示的地址。 */
  url?: string
  /** 截图（<img>）或者界面。 */
  children: ReactNode
}

/** 产品截图：带浏览器窗口的边框。 */
export function ProductShot({ url, children }: ProductShotProps): ReactNode {
  return (
    <div className="tn-lp-shot">
      <div className="tn-lp-shot-window">
        <div className="tn-lp-window-bar">
          <i />
          <i />
          <i />
          {url && <span>{url}</span>}
        </div>
        <div className="tn-lp-shot-body">{children}</div>
      </div>
    </div>
  )
}

/** CodeWindow 的选项。 */
export interface CodeWindowProps {
  /** 标题栏，例如文件名。 */
  title?: ReactNode
  /** 代码，原样显示。 */
  code: string
  /** terminal 总是暗色的终端；editor（默认）随页面的配色。 */
  tone?: "editor" | "terminal"
}

/** 代码或终端窗口。 */
export function CodeWindow({ title, code, tone = "editor" }: CodeWindowProps): ReactNode {
  return (
    <div className="tn-lp-code" data-tone={tone}>
      {title !== undefined && <div className="tn-lp-code-bar">{title}</div>}
      <pre>{code}</pre>
    </div>
  )
}

/** LogoCloud 的选项。 */
export interface LogoCloudProps {
  /** 上方的一行小字，例如“正在使用的团队”。 */
  title?: ReactNode
  /** 标识：图片或文字。 */
  items: ReactNode[]
}

/** 客户标识。 */
export function LogoCloud({ title, items }: LogoCloudProps): ReactNode {
  return (
    <section className="tn-lp-logos">
      <Container size="lg">
        {title && <p className="tn-lp-logos-title">{title}</p>}
        <div className="tn-lp-logos-row">
          {items.map((item, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: 标识是固定的列表
            <div key={i}>{item}</div>
          ))}
        </div>
      </Container>
    </section>
  )
}

/** 一项特性。 */
export interface FeatureItem {
  /** 图标，例如 <IconBolt size={20} />。 */
  icon?: ReactNode
  title: ReactNode
  description?: ReactNode
  /** 占两列，例如放一段代码。 */
  wide?: boolean
  /** 说明下方的内容，例如 CodeWindow。 */
  children?: ReactNode
}

/** Features 的选项。 */
export interface FeaturesProps extends Omit<SectionProps, "children"> {
  items: FeatureItem[]
}

/** 特性：标题区和三列的卡片。 */
export function Features({ items, ...head }: FeaturesProps): ReactNode {
  return (
    <Section {...head}>
      <div className="tn-lp-features">
        {items.map((f, i) => (
          <div
            // biome-ignore lint/suspicious/noArrayIndexKey: 特性是固定的列表
            key={i}
            className="tn-lp-feature"
            data-wide={f.wide || undefined}
            data-index={String(i + 1).padStart(2, "0")}
            data-hue={i % 6}
          >
            {f.icon && <div className="tn-lp-feature-icon">{f.icon}</div>}
            <h3>{f.title}</h3>
            {f.description && <p>{f.description}</p>}
            {f.children && <div className="tn-lp-feature-extra">{f.children}</div>}
          </div>
        ))}
      </div>
    </Section>
  )
}

/** 一项数据。 */
export interface StatItem {
  value: ReactNode
  /** 数字后面的单位，例如“ms”“个”。 */
  unit?: ReactNode
  label: ReactNode
}

/** 一行关键数据。 */
export function Stats({ items }: { items: StatItem[] }): ReactNode {
  return (
    <section className="tn-lp-section tn-lp-stats-section">
      <Container size="lg">
        <div className="tn-lp-stats" style={{ "--tn-lp-cols": items.length } as CSSProperties}>
          {items.map((s, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: 数据是固定的列表
            <div key={i} className="tn-lp-stat">
              <strong>
                {s.value}
                {s.unit && <small>{s.unit}</small>}
              </strong>
              <span>{s.label}</span>
            </div>
          ))}
        </div>
      </Container>
    </section>
  )
}

/** Testimonial 的选项。 */
export interface TestimonialProps {
  quote: ReactNode
  name: ReactNode
  /** 职位和公司。 */
  title?: ReactNode
  /** 头像；没有时显示名字的第一个字。 */
  avatar?: ReactNode
}

/** 用户评价。 */
export function Testimonial({ quote, name, title, avatar }: TestimonialProps): ReactNode {
  const initial = typeof name === "string" ? name.slice(0, 1) : null
  return (
    <section className="tn-lp-section">
      <Container size="lg">
        <figure className="tn-lp-quote">
          <blockquote>{quote}</blockquote>
          <figcaption>
            <span className="tn-lp-avatar">{avatar ?? initial}</span>
            <span>
              <b>{name}</b>
              {title && <span>{title}</span>}
            </span>
          </figcaption>
        </figure>
      </Container>
    </section>
  )
}

/** CallToAction 的选项。 */
export interface CallToActionProps {
  title: ReactNode
  description?: ReactNode
  actions: ReactNode
}

/** 页面底部的行动号召。 */
export function CallToAction({ title, description, actions }: CallToActionProps): ReactNode {
  return (
    <section className="tn-lp-section">
      <Container size="lg">
        <div className="tn-lp-cta">
          <div className="tn-lp-cta-text">
            <h2 className="tn-lp-h2">{title}</h2>
            {description && <p>{description}</p>}
          </div>
          <div className="tn-lp-actions">{actions}</div>
        </div>
      </Container>
    </section>
  )
}
