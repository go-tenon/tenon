// Package assets 打包页面的静态资源：Tailwind 编译出的 css/output.css 与
// shadcn-templ 组件的 js（由 shadcn-templ bundle 生成）。修改样式或组件后用 task css / task bundle 重新生成。
package assets

import "embed"

//go:embed css/output.css js
var FS embed.FS
