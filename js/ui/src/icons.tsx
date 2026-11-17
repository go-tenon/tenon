// 图标：路由的 staticData.icon 是图标的名称（字符串），这里把名称对应到 Tabler 图标。
// 应用可以通过 UIProvider 的 icons 增加或替换名称。

import {
  IconApps,
  IconArticle,
  IconBell,
  IconBook,
  IconBox,
  IconBuildingStore,
  IconCalendar,
  IconChartBar,
  IconChartPie,
  IconChecklist,
  IconCircle,
  IconClipboardList,
  IconClock,
  IconCloudUpload,
  IconCode,
  IconCreditCard,
  IconDatabase,
  IconFile,
  IconFileText,
  IconFolder,
  IconHeadset,
  IconHome,
  IconKey,
  IconLayoutDashboard,
  IconListDetails,
  IconLock,
  IconMail,
  IconMessage,
  IconPackage,
  IconPhoto,
  IconPlug,
  IconReceipt,
  IconReportAnalytics,
  IconRobot,
  IconSettings,
  IconShieldLock,
  IconShoppingCart,
  IconSparkles,
  IconTags,
  IconTruck,
  IconUser,
  IconUsers,
  IconUsersGroup,
  IconWallet,
  IconWorld,
  type Icon as TablerIcon,
} from "@tabler/icons-react"
import { type ComponentType, createContext, type ReactNode, useContext } from "react"

/** 图标组件：接受 size 和 stroke，Tabler 图标都符合。 */
export type IconComponent = ComponentType<{ size?: number | string; stroke?: number; className?: string }>

/**
 * 内置的图标名称。名称与常见组件库的写法保持一致，例如 setting 和 settings、team 和 users 都可以。
 * 完整的 Tabler 图标见 https://tabler.io/icons，需要其他图标时通过 UIProvider 的 icons 增加。
 */
export const defaultIcons: Record<string, IconComponent> = {
  home: IconHome,
  dashboard: IconLayoutDashboard,
  apps: IconApps,
  article: IconArticle,
  file: IconFile,
  "file-text": IconFileText,
  folder: IconFolder,
  book: IconBook,
  docs: IconBook,
  user: IconUser,
  users: IconUsers,
  team: IconUsersGroup,
  group: IconUsersGroup,
  setting: IconSettings,
  settings: IconSettings,
  safety: IconShieldLock,
  security: IconShieldLock,
  shield: IconShieldLock,
  lock: IconLock,
  key: IconKey,
  bell: IconBell,
  notification: IconBell,
  mail: IconMail,
  message: IconMessage,
  chart: IconChartBar,
  "bar-chart": IconChartBar,
  "pie-chart": IconChartPie,
  report: IconReportAnalytics,
  order: IconReceipt,
  receipt: IconReceipt,
  cart: IconShoppingCart,
  "shopping-cart": IconShoppingCart,
  shop: IconBuildingStore,
  store: IconBuildingStore,
  product: IconPackage,
  package: IconPackage,
  box: IconBox,
  truck: IconTruck,
  wallet: IconWallet,
  payment: IconCreditCard,
  "credit-card": IconCreditCard,
  tag: IconTags,
  tags: IconTags,
  calendar: IconCalendar,
  clock: IconClock,
  schedule: IconClock,
  task: IconChecklist,
  todo: IconChecklist,
  list: IconListDetails,
  form: IconClipboardList,
  picture: IconPhoto,
  image: IconPhoto,
  upload: IconCloudUpload,
  database: IconDatabase,
  api: IconPlug,
  plugin: IconPlug,
  code: IconCode,
  global: IconWorld,
  world: IconWorld,
  robot: IconRobot,
  ai: IconSparkles,
  support: IconHeadset,
}

const IconsContext = createContext<Record<string, IconComponent>>(defaultIcons)

/** 提供图标名称的对应表，UIProvider 已经安装了它。 */
export function IconsProvider({ icons, children }: { icons?: Record<string, IconComponent>; children: ReactNode }) {
  return (
    <IconsContext.Provider value={icons ? { ...defaultIcons, ...icons } : defaultIcons}>
      {children}
    </IconsContext.Provider>
  )
}

/** 按名称取图标组件；没有这个名称时返回一个空心圆，不让菜单因为写错名称而报错。 */
export function useIcon(name: string | undefined): IconComponent | undefined {
  const icons = useContext(IconsContext)
  if (!name) return undefined
  return icons[name] ?? (IconCircle as TablerIcon)
}

/** 按名称渲染图标；name 为空时不渲染。 */
export function Icon({ name, size = 18, stroke = 1.6 }: { name?: string; size?: number; stroke?: number }): ReactNode {
  const C = useIcon(name)
  return C ? <C size={size} stroke={stroke} /> : null
}
