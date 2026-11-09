// Package config 提供类型化配置的文档解析与按段解码。
//
// 默认值由应用的 DefaultConfig 设置；ReadFile / Parse 得到 Document，
// 此时不展开环境变量。Document.Decode 解码指定顶层段时才展开变量、
// 拒绝未知字段并调用 Validate。
//
//	doc, err := config.ReadFile("config.yaml",
//		config.WithSections("http", "database", "logging"),
//		config.WithLookup(os.LookupEnv),
//	)
//	httpCfg := DefaultHTTPConfig()
//	if err := doc.Decode("http", &httpCfg); err != nil { ... }
//
// 环境变量仅出现在 YAML 标量中的 ${VAR} / ${VAR:-default}；查找函数由
// WithLookup 注入，本包不调用 os.Getenv。映射合并、序列替换；拒绝显式 null
// 与多文档。Secret 在诊断输出中脱敏，真实值通过 Reveal 取得。
package config
