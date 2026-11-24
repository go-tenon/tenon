// @go-tenon/ui/core 是轻量前端入口：导出主题、国际化、应用骨架、基础组件与官网/文档/认证/错误页布局，
// 仅依赖 @mantine/core、@mantine/hooks、@tabler/icons-react、@tanstack/react-query、@tanstack/react-router，
// 配合 @go-tenon/ui/base.css 使用，适合官网、文档站、登录门户和嵌入式组件。

// ———————————————————————————— app ————————————————————————————
export * from "./app/app"
export * from "./app/document-title"
export {
  Fill,
  type LayoutState,
  type NavControl,
  NavControlProvider,
  type Navigation,
  Slot,
  type SlotName,
  useCollapseNav,
  useFullBleed,
  useLayoutState,
  useMenuBadge,
  useNarrow,
  useNavCollapseRequested,
  useNavControl,
  useNavigation,
  useSlotFilled,
} from "./app/layout"
export { type Crumb, firstPage, type MenuItem, pageUnder } from "./app/menu"
export * from "./app/redirect"
export * from "./app/storage"
export * from "./app/tenant"
export * from "./app/version"
// ———————————————————————————— blocks ————————————————————————————
export * from "./blocks/auth-layout"
export * from "./blocks/docs-layout"
export * from "./blocks/error-page"
export * from "./blocks/language-menu"
export * from "./blocks/site-layout"
export * from "./blocks/user-menu"
// ———————————————————————————— components ————————————————————————————
export * from "./components/description-list"
export * from "./components/links"
export * from "./components/loading"
export * from "./components/menu-badge"
export * from "./components/page"
export * from "./components/stat-card"
export * from "./components/status-dot"
export * from "./components/truncated-text"
// ———————————————————————————— i18n ————————————————————————————
export {
  I18nProvider,
  type I18nRegister,
  type MessageId,
  type UiMessageId,
  uiMessages,
  useI18n,
  useLanguage,
  useT,
} from "./i18n/i18n"
// ———————————————————————————— theme ————————————————————————————
export * from "./theme/color-scheme"
export * from "./theme/core-provider"
export * from "./theme/icons"
export * from "./theme/theme"
