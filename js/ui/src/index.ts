// @go-tenon/ui 是框架的前端应用层，基于 Mantine：应用骨架（装配 Router、QueryClient 和会话，登录与权限检查、
// 菜单、多标签、租户、表单、列表、资源模型），三套样式预设（运营后台、SaaS 控制台、官网和内容站），以及布局和部件。
// 逻辑在 shell/ 目录中，不导入 Mantine；组件在本目录中。样式：import "@go-tenon/ui/styles.css"。
// AI 对话是单独的入口 @go-tenon/ui/chat，只有用到它的前端才需要安装 ai 和 @ai-sdk/react。

export * from "./activity"
export * from "./app-layout"
export * from "./auth"
export * from "./color-scheme"
export * from "./data-table"
export * from "./description-list"
export * from "./docs"
export * from "./errors"
export * from "./feedback"
export * from "./fields"
export * from "./form"
export * from "./icons"
export * from "./links"
export * from "./menus"
export * from "./page"
export * from "./provider"
export {
  type QueueApi,
  type QueueFailedJobData,
  type QueueFailedJobDetailData,
  type QueueFailedListData,
  QueuePanel,
  type QueuePanelProps,
  type QueueStatusData,
  type QueueTopicData,
} from "./queue-panel"
export * from "./rail-layout"
export * from "./resource"
export {
  type ScheduleApi,
  SchedulePanel,
  type SchedulePanelProps,
  type ScheduleRunData,
  type ScheduleStatus,
  type ScheduleTaskData,
} from "./schedule-panel"
export * from "./settings"
export * from "./shell"
export * from "./site"
export * from "./tab-bar"
export * from "./theme"
export * from "./topbar-layout"
export * from "./transfer"
export * from "./version"
