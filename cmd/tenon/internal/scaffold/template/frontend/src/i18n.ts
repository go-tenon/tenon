import { createI18n } from "@go-tenon/client"
import { messages } from "./messages.gen"

// i18n 是这个前端的界面文字：项目 locales 目录中这个前端的翻译文件，由 tenon generate 生成为 messages.gen.ts。
// 它交给 createApp（页面、菜单和组件的文字）、生成客户端的 headers（服务端的提示）和 createKit（字段名、枚举、验证提示）。
// 初始语言按浏览器的语言偏好选择；登录后可以按用户的设置切换：i18n.setLanguage(me.language)。
export const i18n = createI18n({ messages, defaultLanguage: "zh-CN" })
