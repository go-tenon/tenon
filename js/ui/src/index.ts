// @go-tenon/ui 是框架的前端应用层，基于 Mantine。代码分成六层，上层只引用下层，blocks 和 features 互相不引用：
//   - theme：三套样式预设（compact、comfortable、spacious）、由设计令牌生成主题、UIProvider、图标、配色切换；
//   - i18n：最下层，框架组件内置的简体中文、英文文字，组件按应用的 I18n（createApp 的 i18n）取文字；
//   - app：应用骨架，装配 Router、QueryClient 和会话，登录与权限检查、由路由生成菜单和面包屑、插槽、多标签、租户、新版本提示；
//   - components：页面中用的部件，表格与地址中的列表状态、按生成的规则校验的表单、页面标题区、描述列表、确认与提示；
//   - blocks：整块的界面，只描述样子，彼此独立、任意组合：布局、登录页的布局、错误页、用户菜单、语言菜单；
//   - features：框架功能的前端，逻辑和外观放在一起：资源与设置、登录表单、通知、后台任务、导入导出、操作记录、队列和定时任务面板。
// 把这些组合成应用的是 tenon init 生成的项目。样式：import "@go-tenon/ui/styles.css"。
// AI 对话是单独的入口 @go-tenon/ui/chat，只有用到它的前端才需要安装 ai 和 @ai-sdk/react。

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
export * from "./app/tabs/tab-bar"
export * from "./app/tabs/tabs"
export * from "./app/tenant"
export * from "./app/version"
export * from "./app/version-notice"
// ———————————————————————————— blocks ————————————————————————————
export * from "./blocks/auth-layout"
export * from "./blocks/docs-layout"
export * from "./blocks/error-page"
export * from "./blocks/frame-layout"
export * from "./blocks/language-menu"
export * from "./blocks/rail-layout"
export * from "./blocks/site-layout"
export * from "./blocks/user-menu"
// ———————————————————————————— components ————————————————————————————
export * from "./components/data-table/data-table"
export * from "./components/data-table/list"
export * from "./components/data-table/saved-view"
export * from "./components/description-list"
export * from "./components/feedback"
export * from "./components/form/autosave"
export * from "./components/form/form"
export * from "./components/form/rules"
export * from "./components/form/secret-input"
export * from "./components/image-lightbox"
export * from "./components/links"
export * from "./components/menu-badge"
export * from "./components/page"
export * from "./components/period/period"
export * from "./components/stat-card"
export * from "./components/status-dot"
export * from "./components/text-trigger/text-trigger"
export * from "./components/truncated-text"
export * from "./components/upload-queue"
// ———————————————————————————— features ————————————————————————————
export * from "./features/activity/activity-log"
export * from "./features/activity/model"
export * from "./features/browser-sign-in"
export * from "./features/login-form"
export * from "./features/notifications/model"
export * from "./features/notifications/notifications-menu"
export * from "./features/operations/model"
export * from "./features/operations/operations-menu"
export {
  type QueueApi,
  type QueueFailedJobData,
  type QueueFailedJobDetailData,
  type QueueFailedListData,
  QueuePanel,
  type QueuePanelProps,
  type QueueStatusData,
  type QueueTopicData,
} from "./features/queue-panel"
export * from "./features/resources/fields"
export * from "./features/resources/model"
export * from "./features/resources/resource-page"
export * from "./features/resources/resource-select"
export * from "./features/resources/settings-form"
export * from "./features/resources/settings-page"
export {
  type ScheduleApi,
  SchedulePanel,
  type SchedulePanelProps,
  type ScheduleRunData,
  type ScheduleStatus,
  type ScheduleTaskData,
} from "./features/schedule-panel"
export * from "./features/transfer/model"
export * from "./features/transfer/transfer-buttons"
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
export * from "./theme/format"
export * from "./theme/icons"
export * from "./theme/provider"
export * from "./theme/theme"
