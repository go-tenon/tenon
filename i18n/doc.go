// Package i18n 组合 go-i18n 消息目录并校验默认语言中的必需消息。
//
// New 返回原生 Bundle，翻译使用 go-i18n Localizer。
// 消息按框架、模块、产品的优先级覆盖；同一语言、同一优先级的重复 ID 返回错误。
// 请求通过 Accept-Language 选择目录语言，context 携带选择结果。
package i18n
