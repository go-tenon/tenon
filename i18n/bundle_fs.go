package i18n

import "embed"

//go:embed locales/*.yaml
var frameworkLocales embed.FS

// FrameworkLayer 返回内置中英文消息层。
func FrameworkLayer() Layer {
	return Layer{Priority: PriorityFramework, FS: frameworkLocales, Name: "framework"}
}
