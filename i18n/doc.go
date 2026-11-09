// Package i18n 提供基于 go-i18n 的目录组合与校验辅助。
//
// 直接构建 *goi18n.Bundle / *goi18n.Localizer；构建完成后 Bundle 约定不再修改。
// 覆盖顺序：框架默认 → 模块 → 产品；相同优先级下重复消息 ID 直接报错。
// 不提供每次翻译的 Catalog 门面；context 只携带语言标记。
//
// 第一期请求语言取 Accept-Language 与默认语言，不读取 Cookie。
package i18n
