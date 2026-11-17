// 主题：三套样式预设和由设计令牌生成主题。外观只通过 Mantine 的主题表达，组件不写死颜色和尺寸。

import {
  Button,
  createTheme,
  Input,
  type MantineColorsTuple,
  type MantineThemeOverride,
  mergeThemeOverrides,
} from "@mantine/core"

// ———————————————————————————— 色板 ————————————————————————————

// 比第 6 级浅的 6 级：从输入颜色走向接近白色的比例（第 0 级最浅）。
const lighter = [0.97, 0.88, 0.72, 0.55, 0.36, 0.18]
// 比第 6 级深的 3 级：从输入颜色走向最深的比例。
const darker = [0.25, 0.5, 0.75]
// 每一级相对输入颜色的彩度：浅色降低彩度，避免发灰或过艳。
const chroma = [0.18, 0.32, 0.55, 0.75, 0.9, 0.97, 1, 0.95, 0.88, 0.8]

/**
 * 由一个十六进制颜色生成 Mantine 的 10 级色板：同一色相，按感知亮度（OKLCH）相对输入颜色分级，
 * 第 6 级就是输入的颜色，所以很浅或很深的品牌色也从浅到深单调。
 */
export function palette(hex: string): MantineColorsTuple {
  const [l, c, h] = toOklch(parseHex(hex))
  const top = Math.max(0.985, l)
  const bottom = Math.min(Math.max(0.12, l * 0.45), l)
  const shades = Array.from({ length: 10 }, (_, i) => {
    if (i === 6) return normalizeHex(hex)
    const target = i < 6 ? l + (top - l) * lighter[i] : l - (l - bottom) * darker[i - 7]
    return toHex(fromOklch([target, c * chroma[i], h]))
  })
  return shades as unknown as MantineColorsTuple
}

function normalizeHex(hex: string): string {
  return toHex(parseHex(hex))
}

type RGB = [number, number, number]

function parseHex(hex: string): RGB {
  const m = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(hex.trim())
  if (!m) throw new Error(`@go-tenon/ui: invalid color ${JSON.stringify(hex)}, expected #rrggbb`)
  const s = m[1].length === 3 ? [...m[1]].map((c) => c + c).join("") : m[1]
  return [0, 2, 4].map((i) => Number.parseInt(s.slice(i, i + 2), 16) / 255) as RGB
}

function toHex(rgb: RGB): string {
  return `#${rgb
    .map((v) =>
      Math.round(Math.min(1, Math.max(0, v)) * 255)
        .toString(16)
        .padStart(2, "0"),
    )
    .join("")}`
}

const toLinear = (v: number) => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4)
const fromLinear = (v: number) => (v <= 0.0031308 ? v * 12.92 : 1.055 * v ** (1 / 2.4) - 0.055)

function toOklch(rgb: RGB): [number, number, number] {
  const [r, g, b] = rgb.map(toLinear)
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b)
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b)
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b)
  const L = 0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s
  const A = 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s
  const B = 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s
  return [L, Math.hypot(A, B), Math.atan2(B, A)]
}

function fromOklch([L, C, H]: [number, number, number]): RGB {
  // 超出 sRGB 时逐步降低彩度，保持亮度和色相。
  for (let c = C; ; c *= 0.92) {
    const a = c * Math.cos(H)
    const b = c * Math.sin(H)
    const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
    const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
    const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
    const rgb = [
      4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
      -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
      -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
    ].map(fromLinear) as RGB
    if (rgb.every((v) => v >= -0.001 && v <= 1.001) || c < 0.001) return rgb
  }
}

/** 样式预设的名称。 */
export type Preset = "admin" | "console" | "site"

/**
 * 落地页的风格，作用于 SiteLayout 和落地页区块（Hero、Features 等）：
 *   - classic：朴素，跟随 Mantine 的主题；
 *   - dusk：暗色优先，近黑底、光晕与细边框；
 *   - ink：黑白高对比，网格、实线和等宽数字；
 *   - mist：柔和的渐变背景、毛玻璃和大圆角。
 */
export type Landing = "classic" | "dusk" | "ink" | "mist"

/** 落地页的风格。 */
export const landings: readonly Landing[] = ["classic", "dusk", "ink", "mist"]

/** 布局用到的尺寸，放在主题的 other.tenon 中，布局组件据此排版。 */
export interface LayoutMetrics {
  preset: Preset
  /** 落地页的风格，默认 classic。 */
  landing?: Landing
  /** 顶栏的高度（像素）。 */
  headerHeight: number
  /** 侧栏展开时的宽度（像素）。 */
  navbarWidth: number
  /** 侧栏收起为图标时的宽度（像素）。 */
  navbarCollapsedWidth: number
  /** 内容区的内边距（Mantine 的间距名）。 */
  contentPadding: "sm" | "md" | "lg" | "xl"
  /** 表单控件、按钮的默认尺寸。 */
  controlSize: "xs" | "sm" | "md"
  /** 日期时间的显示格式（dayjs 的写法），例如 MM/DD HH:mm。表格、操作记录、通知中的时间都用它。 */
  datetimeFormat: string
}

// 系统字体：中文优先用系统自带的苹方、思源黑体、微软雅黑，不额外加载字体。
const fontFamily =
  '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Noto Sans SC", "Noto Sans CJK SC", "Microsoft YaHei", Roboto, "Helvetica Neue", Arial, sans-serif'
// 等宽字体：代码中的中文（注释、输出）回落到中文字体显示。
const fontFamilyMonospace =
  'ui-monospace, SFMono-Regular, "JetBrains Mono", Menlo, Consolas, "Liberation Mono", "PingFang SC", "Noto Sans SC", "Noto Sans CJK SC", "Microsoft YaHei", monospace'

// 默认的品牌色：偏冷的蓝紫，在亮色和暗色背景上都清楚，又不刺眼。
const brand = "#4f5bd5"

// 中性色：比 Mantine 默认的灰略冷一点，线条和背景更干净。
const slate: MantineColorsTuple = [
  "#f8f9fb",
  "#f1f3f6",
  "#e5e8ee",
  "#d4d9e1",
  "#aab2bf",
  "#8892a2",
  "#5f6878",
  "#4a5262",
  "#343a46",
  "#1f232b",
]

// 暗色：Mantine 的 dark 色板从浅到深，[7] 是页面背景、[6] 是面板、[4] 是边框。偏中性略冷。
const dark: MantineColorsTuple = [
  "#d6d9df",
  "#b4b9c3",
  "#8b919e",
  "#5f6572",
  "#383c46",
  "#2c3039",
  "#23262e",
  "#1a1d23",
  "#14161b",
  "#0e0f13",
]

function metrics(m: LayoutMetrics): MantineThemeOverride {
  return { other: { tenon: m } }
}

/** 三套预设共用的部分：字体、色板、焦点、组件的默认属性。 */
function base(m: LayoutMetrics): MantineThemeOverride {
  const size = m.controlSize
  return createTheme({
    fontFamily,
    fontFamilyMonospace,
    primaryColor: "brand",
    primaryShade: { light: 6, dark: 5 },
    colors: { brand: palette(brand), gray: slate, dark },
    cursorType: "pointer",
    focusRing: "auto",
    autoContrast: true,
    defaultGradient: { from: "brand.5", to: "brand.7", deg: 135 },
    headings: { fontFamily, fontWeight: "600" },
    components: {
      Button: { defaultProps: { size } },
      ActionIcon: { defaultProps: { variant: "subtle", color: "gray" } },
      TextInput: { defaultProps: { size } },
      PasswordInput: { defaultProps: { size } },
      NumberInput: { defaultProps: { size } },
      Textarea: { defaultProps: { size } },
      Select: { defaultProps: { size } },
      MultiSelect: { defaultProps: { size } },
      Checkbox: { defaultProps: { size: size === "md" ? "sm" : "xs" } },
      Switch: { defaultProps: { size: size === "md" ? "sm" : "xs" } },
      SegmentedControl: { defaultProps: { size } },
      Card: { defaultProps: { withBorder: true, shadow: "none", padding: m.contentPadding } },
      Paper: { defaultProps: { withBorder: true, shadow: "none" } },
      Menu: { defaultProps: { shadow: "md", position: "bottom-end", offset: 6, transitionProps: { duration: 120 } } },
      Popover: { defaultProps: { shadow: "md", offset: 8, transitionProps: { duration: 120 } } },
      Tooltip: { defaultProps: { openDelay: 300, withArrow: false, color: "dark", fz: "xs" } },
      Badge: { defaultProps: { variant: "light", radius: "sm", tt: "none", fw: 500 } },
      Modal: {
        defaultProps: { centered: true, overlayProps: { backgroundOpacity: 0.4, blur: 2 } },
        styles: { title: { fontWeight: 600 } },
      },
      Drawer: {
        defaultProps: { overlayProps: { backgroundOpacity: 0.4, blur: 2 } },
        styles: { title: { fontWeight: 600 } },
      },
      Notification: { defaultProps: { withBorder: true } },
    },
    ...metrics(m),
  })
}

// 运营后台的主色：沉稳的深蓝灰。
const adminBrand = "#34507f"

// 运营后台的暗色：带一点主色调的深灰。[7] 是页面背景、[8] 是侧栏、[6] 是悬停、[4] 是边框。
const adminDark: MantineColorsTuple = [
  "#d5d9e0",
  "#b0b7c3",
  "#8a93a3",
  "#5f6878",
  "#3d4452",
  "#2c323d",
  "#232831",
  "#1b1f26",
  "#15181e",
  "#0f1115",
]

// 运营后台默认尺寸（sm）的按钮和输入框：32px 高、13px 字（Mantine 默认 36px、14px）。
const adminControl = { height: "32px", fz: "13px" }

/** 运营后台：信息密度高，偏表格和表单。正文 14px，控件 32px 高。 */
const admin: MantineThemeOverride = mergeThemeOverrides(
  base({
    preset: "admin",
    headerHeight: 52,
    navbarWidth: 224,
    navbarCollapsedWidth: 56,
    contentPadding: "md",
    controlSize: "sm",
    datetimeFormat: "MM/DD HH:mm",
  }),
  createTheme({
    colors: { brand: palette(adminBrand), dark: adminDark },
    defaultRadius: "sm",
    radius: { xs: "2px", sm: "4px", md: "8px", lg: "12px", xl: "16px" },
    fontSizes: { xs: "12px", sm: "13px", md: "14px", lg: "16px", xl: "18px" },
    spacing: { xs: "8px", sm: "10px", md: "14px", lg: "18px", xl: "24px" },
    headings: {
      sizes: {
        h1: { fontSize: "26px", lineHeight: "1.3" },
        h2: { fontSize: "20px", lineHeight: "1.35" },
        h3: { fontSize: "17px", lineHeight: "1.4" },
        h4: { fontSize: "15px", lineHeight: "1.45" },
      },
    },
    components: {
      Button: Button.extend({
        vars: (_, props) =>
          props.size === undefined || props.size === "sm"
            ? {
                root: {
                  "--button-height": adminControl.height,
                  "--button-padding-x": "12px",
                  "--button-fz": adminControl.fz,
                },
              }
            : { root: {} },
      }),
      Input: Input.extend({
        vars: (_, props) =>
          props.size === undefined || props.size === "sm"
            ? { wrapper: { "--input-height": adminControl.height, "--input-fz": adminControl.fz } }
            : { wrapper: {} },
      }),
    },
  }),
)

/** SaaS 控制台：舒展的留白、柔和的圆角，适合面向客户的后台。 */
const consolePreset: MantineThemeOverride = mergeThemeOverrides(
  base({
    preset: "console",
    headerHeight: 56,
    navbarWidth: 248,
    navbarCollapsedWidth: 64,
    contentPadding: "lg",
    controlSize: "sm",
    datetimeFormat: "YYYY-MM-DD HH:mm",
  }),
  createTheme({
    defaultRadius: "md",
    radius: { xs: "4px", sm: "6px", md: "8px", lg: "12px", xl: "16px" },
    fontSizes: { xs: "12px", sm: "13px", md: "14px", lg: "16px", xl: "18px" },
    headings: {
      sizes: {
        h1: { fontSize: "26px", lineHeight: "1.25" },
        h2: { fontSize: "20px", lineHeight: "1.3" },
        h3: { fontSize: "16px", lineHeight: "1.4" },
        h4: { fontSize: "14px", lineHeight: "1.45" },
      },
    },
  }),
)

// 官网的字体：拉丁字母优先用 Inter（项目引入 @fontsource-variable/inter 时），中文仍是系统字体。
const siteFontFamily = `"Inter Variable", Inter, ${fontFamily}`

/** 官网和内容站：大标题、宽松的行距，适合落地页、文章和文档。 */
const site: MantineThemeOverride = mergeThemeOverrides(
  base({
    preset: "site",
    headerHeight: 64,
    navbarWidth: 260,
    navbarCollapsedWidth: 64,
    contentPadding: "xl",
    controlSize: "md",
    datetimeFormat: "YYYY-MM-DD HH:mm",
  }),
  createTheme({
    fontFamily: siteFontFamily,
    defaultRadius: "md",
    radius: { xs: "4px", sm: "6px", md: "10px", lg: "14px", xl: "20px" },
    fontSizes: { xs: "13px", sm: "14px", md: "16px", lg: "18px", xl: "20px" },
    lineHeights: { xs: "1.5", sm: "1.6", md: "1.7", lg: "1.7", xl: "1.65" },
    headings: {
      fontFamily: siteFontFamily,
      fontWeight: "700",
      sizes: {
        h1: { fontSize: "clamp(32px, 5vw, 52px)", lineHeight: "1.1" },
        h2: { fontSize: "clamp(24px, 3.2vw, 34px)", lineHeight: "1.2" },
        h3: { fontSize: "20px", lineHeight: "1.35" },
        h4: { fontSize: "17px", lineHeight: "1.4" },
      },
    },
  }),
)

/** 三套样式预设：运营后台、SaaS 控制台、官网和内容站。 */
export const themes: Record<Preset, MantineThemeOverride> = { admin, console: consolePreset, site }

/** createUiTheme 的选项，通常来自项目的设计令牌（theme/tokens.json）。 */
export interface UiThemeOptions {
  /** 样式预设，默认 console。 */
  preset?: Preset
  /** 品牌色，十六进制，例如 #0f766e。生成 10 级色板，第 6 级（亮色模式的主色）就是它。 */
  primary?: string
  /** 基础圆角（像素），其他圆角按比例缩放。 */
  radius?: number
  /** 字体，不设置时使用系统字体。 */
  fontFamily?: string
  /** 落地页的风格，只对 site 预设有意义，默认 classic。 */
  landing?: Landing
  /** 日期时间的显示格式（dayjs 的写法），默认随预设：admin 是 MM/DD HH:mm，其他是 YYYY-MM-DD HH:mm。 */
  datetimeFormat?: string
  /** 再合并的 Mantine 主题覆盖，用于令牌之外的调整。 */
  override?: MantineThemeOverride
}

/** 由预设和设计令牌生成 Mantine 主题，交给 UIProvider。 */
export function createUiTheme(options: UiThemeOptions = {}): MantineThemeOverride {
  const parts: MantineThemeOverride[] = [themes[options.preset ?? "console"]]
  if (options.landing && !landings.includes(options.landing)) {
    throw new Error(
      `@go-tenon/ui: unknown landing ${JSON.stringify(options.landing)}, expected one of ${landings.join(", ")}`,
    )
  }
  if (options.landing || options.datetimeFormat) {
    const m = layoutMetrics(parts[0] as { other?: Record<string, unknown> })
    parts.push({
      other: {
        tenon: {
          ...m,
          ...(options.landing && { landing: options.landing }),
          ...(options.datetimeFormat && { datetimeFormat: options.datetimeFormat }),
        },
      },
    })
  }
  if (options.primary) parts.push({ colors: { brand: palette(options.primary) } })
  if (options.radius !== undefined) {
    const r = options.radius
    parts.push({
      radius: {
        xs: `${Math.round(r * 0.5)}px`,
        sm: `${Math.round(r * 0.75)}px`,
        md: `${r}px`,
        lg: `${Math.round(r * 1.5)}px`,
        xl: `${r * 2}px`,
      },
    })
  }
  if (options.fontFamily) parts.push({ fontFamily: options.fontFamily, headings: { fontFamily: options.fontFamily } })
  if (options.override) parts.push(options.override)
  return mergeThemeOverrides(...parts)
}

/** 主题中的布局尺寸；没有时（没有用 createUiTheme 或 themes）返回控制台的尺寸。 */
export function layoutMetrics(theme: { other?: Record<string, unknown> }): LayoutMetrics {
  return (theme.other?.tenon as LayoutMetrics | undefined) ?? (consolePreset.other?.tenon as LayoutMetrics)
}
