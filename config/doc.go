// Package config 解析 YAML 文档并按顶层段解码配置。
//
// 应用先创建默认配置，再调用 ReadFile 或 Parse，最后用 Document.Decode
// 展开所选段的环境变量、检查字段并调用 Validate。
//
//	doc, err := config.ReadFile("config.yaml",
//		config.WithSections("http", "database"),
//		config.WithLookup(os.LookupEnv),
//	)
//	if err != nil { return err }
//	httpCfg := DefaultHTTPConfig()
//	if err := doc.Decode("http", &httpCfg); err != nil { return err }
//
// 映射按键合并，序列整体替换；显式 null 与多文档输入返回错误。
// Secret 在诊断输出中脱敏，Reveal 返回原值。
package config
