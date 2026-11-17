// 应用骨架的逻辑：装配 Router、QueryClient 和会话，登录与权限检查、菜单、面包屑、布局约定、多标签、租户切换、表单、列表和新版本提示。
// 这个目录不导入 Mantine，外观在上一级的组件中；测试不必渲染组件就能覆盖逻辑。
// 只导出应用会用到的部分：标签状态的纯函数、由路由生成菜单的函数等内部实现直接从各自的文件导入。
export * from "./activity"
export * from "./app"
export * from "./autosave"
export * from "./form"
export {
  Fill,
  type LayoutState,
  type Navigation,
  Slot,
  type SlotName,
  useLayoutState,
  useMenuBadge,
  useNarrow,
  useNavigation,
  useSlotFilled,
} from "./layout"
export * from "./list"
export { type Crumb, firstPage, type MenuItem, pageUnder } from "./menu"
export * from "./notifications"
export * from "./operations"
export * from "./redirect"
export * from "./resource"
export * from "./storage"
export * from "./tabs"
export * from "./tenant"
export * from "./transfer"
export * from "./version"
