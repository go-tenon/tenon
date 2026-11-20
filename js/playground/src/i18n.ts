// /frame/ 演示的界面文字：应用里这一份由 tenon generate 从 locales/<前端>.<语言>.yaml 生成为 messages.gen.ts。
// 菜单标题写消息 ID（nav.trade），按当前语言显示；框架组件的文字由框架内置，切换语言时一起变化。
import { createI18n } from "@go-tenon/client"

export const messages = {
  "zh-CN": { "nav.trade": "交易", "nav.customers": "客户" },
  en: { "nav.trade": "Trade", "nav.customers": "Customers" },
} as const

export const i18n = createI18n({ messages, defaultLanguage: "zh-CN", preferred: () => ["zh-CN"] })
