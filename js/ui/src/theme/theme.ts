// 主题：三套样式预设和由设计令牌生成主题。外观只通过 Mantine 的主题表达，组件不写死颜色和尺寸。

import { generateColors } from "@mantine/colors-generator"
import {
  Button,
  createTheme,
  Input,
  type MantineColorsTuple,
  type MantineThemeOverride,
  mergeThemeOverrides,
} from "@mantine/core"

// ———————————————————————————— 色板 ————————————————————————————

/**
 * 预设的主题色：Mantine 颜色生成器（mantine.dev/colors-generator）中的 26 种颜色，
 * 名称是官方英文名的小写连字符写法，例如 Sky blue 写作 sky-blue。
 */
export const primaryColors = {
  "blue-gray": "#63687c",
  brown: "#5d4037",
  tomato: "#f06418",
  "deep-orange": "#fc8a08",
  "bright-orange": "#ffa903",
  yellow: "#d9d02f",
  "bright-green": "#6bd731",
  green: "#2bdd66",
  "light-blue": "#00b5ff",
  "sky-blue": "#099cff",
  "pale-blue": "#5474b4",
  "bright-blue": "#0063ff",
  "deep-blue": "#1f32c4",
  "pale-indigo": "#4c5897",
  purple: "#4f23c0",
  "pale-purple": "#504c97",
  violet: "#7b2eda",
  "pale-violet": "#6b31b2",
  "dark-pink": "#8931b2",
  "bright-pink": "#c02adf",
  pink: "#f018e8",
  magenta: "#f01879",
  "bright-red": "#f0185c",
  "pale-red": "#c91a52",
  "deep-red": "#c91a25",
  red: "#f21616",
} as const

/** 预设主题色的名称。 */
export type PrimaryColor = keyof typeof primaryColors

/** 26 种预设的色板，都放进主题的 colors，组件用名称取色，例如 color="deep-orange"。 */
const presetColors = Object.fromEntries(
  Object.entries(primaryColors).map(([name, hex]) => [name, generateColors(hex)]),
) as Record<PrimaryColor, MantineColorsTuple>

/**
 * 由主题色生成 Mantine 的 10 级色板，用 Mantine 官方的颜色生成器（@mantine/colors-generator），
 * 结果与 mantine.dev/colors-generator 一致。color 是预设的名称（见 primaryColors）或者颜色值，例如 #0f766e。
 */
export function palette(color: PrimaryColor | (string & {})): MantineColorsTuple {
  if (Object.hasOwn(presetColors, color)) return presetColors[color as PrimaryColor]
  try {
    return generateColors(color)
  } catch {
    throw new Error(
      `@go-tenon/ui: invalid color ${JSON.stringify(color)}, expected #rrggbb or one of ${Object.keys(primaryColors).join(", ")}`,
    )
  }
}

/**
 * 样式预设的名称，按密度区分：
 *   - compact：紧凑，控件矮、圆角小、行距紧，一屏能看到更多数据；
 *   - comfortable：舒展，Mantine 默认的控件尺寸和柔和的圆角；
 *   - spacious：宽松，大字号、宽行距，适合阅读。
 * 预设只管密度，与布局无关：布局自己的尺寸（顶栏高度、侧栏宽度）由布局决定。
 */
export type Preset = "compact" | "comfortable" | "spacious"

/** 组件按密度排版用到的值，放在主题的 other.tenon 中。 */
export interface LayoutMetrics {
  preset: Preset
  /** 内容区和卡片的内边距（Mantine 的间距名）。 */
  contentPadding: "sm" | "md" | "lg" | "xl"
  /** 表单控件、按钮的默认尺寸。 */
  controlSize: "xs" | "sm" | "md"
  /** 日期时间的显示格式（dayjs 的写法），由应用用 createUiTheme 的 datetimeFormat 设置；没有设置时是 YYYY-MM-DD HH:mm。 */
  datetimeFormat?: string
}

// cjkPunctuation 是字体栈的前缀：中文页面（<html lang="zh-...">）中是 styles.css 定义的 "Tenon CJK Punctuation"，
// 它只包含中英文共用码位的引号、省略号、破折号和间隔号，交给中文字体显示为全角；其他语言的页面中为空。
// 自定义属性在 :root 上计算，所以按 <html> 的 lang 生效。
const cjkPunctuation = "var(--tn-cjk-punctuation,)"

// 系统字体，不额外加载字体。西文字体在前：字母、数字和 •、- 这类西文符号用西文字体；汉字回落到后面的
// 中文字体（苹方、思源黑体、微软雅黑）。Linux 上常见的西文字体是 Noto Sans 和 Liberation Sans。
const systemFonts =
  '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "Noto Sans", "Liberation Sans", "PingFang SC", "Hiragino Sans GB", "Noto Sans SC", "Noto Sans CJK SC", "Microsoft YaHei", sans-serif'
const fontFamily = `${cjkPunctuation} ${systemFonts}`
// 等宽字体：代码中的中文（注释、输出）回落到中文字体显示。
const fontFamilyMonospace =
  'ui-monospace, SFMono-Regular, "JetBrains Mono", Menlo, Consolas, "Liberation Mono", "PingFang SC", "Noto Sans SC", "Noto Sans CJK SC", "Microsoft YaHei", monospace'

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
    colors: { ...presetColors, brand: presetColors["blue-gray"], gray: slate, dark },
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

// compact 预设的暗色：带一点主色调的深灰。[7] 是页面背景、[8] 是侧栏、[6] 是悬停、[4] 是边框。
const compactDark: MantineColorsTuple = [
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

// compact 预设默认尺寸（sm）的按钮和输入框：32px 高、13px 字（Mantine 默认 36px、14px）。
const compactControl = { height: "32px", fz: "13px" }

/** compact 预设：紧凑，信息密度高，适合表格和表单多的界面。正文 14px，控件 32px 高。 */
const compact: MantineThemeOverride = mergeThemeOverrides(
  base({ preset: "compact", contentPadding: "md", controlSize: "sm" }),
  createTheme({
    colors: { dark: compactDark },
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
                  "--button-height": compactControl.height,
                  "--button-padding-x": "12px",
                  "--button-fz": compactControl.fz,
                },
              }
            : { root: {} },
      }),
      Input: Input.extend({
        vars: (_, props) =>
          props.size === undefined || props.size === "sm"
            ? { wrapper: { "--input-height": compactControl.height, "--input-fz": compactControl.fz } }
            : { wrapper: {} },
      }),
    },
  }),
)

/** comfortable 预设：舒展的留白、柔和的圆角，适合页面不密集的应用。正文 14px，控件 36px 高。 */
const comfortable: MantineThemeOverride = mergeThemeOverrides(
  base({ preset: "comfortable", contentPadding: "lg", controlSize: "sm" }),
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

// spacious 预设的字体：拉丁字母优先用 Inter（项目引入 @fontsource-variable/inter 时），中文仍是系统字体。
const spaciousFontFamily = `${cjkPunctuation} "Inter Variable", Inter, ${systemFonts}`

/** spacious 预设：大标题、宽松的行距，适合官网、文章和文档。 */
const spacious: MantineThemeOverride = mergeThemeOverrides(
  base({ preset: "spacious", contentPadding: "xl", controlSize: "md" }),
  createTheme({
    fontFamily: spaciousFontFamily,
    defaultRadius: "md",
    radius: { xs: "4px", sm: "6px", md: "10px", lg: "14px", xl: "20px" },
    fontSizes: { xs: "13px", sm: "14px", md: "16px", lg: "18px", xl: "20px" },
    lineHeights: { xs: "1.5", sm: "1.6", md: "1.7", lg: "1.7", xl: "1.65" },
    headings: {
      fontFamily: spaciousFontFamily,
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

/** 三套样式预设：紧凑的 compact、舒展的 comfortable、宽松的 spacious。 */
export const themes: Record<Preset, MantineThemeOverride> = { compact, comfortable, spacious }

/** createUiTheme 的选项，通常来自项目的设计令牌（theme/tokens.json）。 */
export interface UiThemeOptions {
  /** 样式预设，默认 comfortable。 */
  preset?: Preset
  /**
   * 品牌色：预设的名称（见 primaryColors，例如 sky-blue）或者颜色值（例如 #0f766e），默认是第一种预设 blue-gray。
   * 用 Mantine 官方的颜色生成器生成 10 级色板，输入的颜色按亮度落在其中一级。
   */
  primary?: PrimaryColor | (string & {})
  /** 基础圆角（像素），其他圆角按比例缩放。 */
  radius?: number
  /** 字体，不设置时使用系统字体。中文页面中，引号、省略号、破折号和间隔号仍由中文字体显示为全角。 */
  fontFamily?: string
  /** 日期时间的显示格式（dayjs 的写法），例如 MM/DD HH:mm，默认 YYYY-MM-DD HH:mm。表格、操作记录、通知中的时间都用它。 */
  datetimeFormat?: string
  /** 再合并的 Mantine 主题覆盖，用于令牌之外的调整。 */
  override?: MantineThemeOverride
}

/** 由预设和设计令牌生成 Mantine 主题，交给 UIProvider。 */
export function createUiTheme(options: UiThemeOptions = {}): MantineThemeOverride {
  const parts: MantineThemeOverride[] = [themes[options.preset ?? "comfortable"]]
  if (options.datetimeFormat) {
    const m = layoutMetrics(parts[0] as { other?: Record<string, unknown> })
    parts.push({
      other: {
        tenon: {
          ...m,
          datetimeFormat: options.datetimeFormat,
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
  if (options.fontFamily) {
    const family = `${cjkPunctuation} ${options.fontFamily}`
    parts.push({ fontFamily: family, headings: { fontFamily: family } })
  }
  if (options.override) parts.push(options.override)
  return mergeThemeOverrides(...parts)
}

/** 主题中按密度排版用到的值；没有时（没有用 createUiTheme 或 themes）返回 comfortable 预设的值。 */
export function layoutMetrics(theme: { other?: Record<string, unknown> }): LayoutMetrics {
  return (theme.other?.tenon as LayoutMetrics | undefined) ?? (comfortable.other?.tenon as LayoutMetrics)
}
