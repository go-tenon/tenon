// Command gen 由文档生成仓库根目录的 llms.txt。在仓库根目录运行：
//
//	go run ./internal/llmstxt/gen
package main

import (
	"fmt"
	"os"

	"github.com/go-tenon/tenon/internal/llmstxt"
)

// main 生成 llms.txt。
func main() {
	b, err := llmstxt.Build(".")
	if err == nil {
		err = os.WriteFile("llms.txt", b, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "llmstxt:", err)
		os.Exit(1)
	}
}
