// Command gen 由文档生成仓库根目录的 llms.txt；-full 把 llms-full.txt 写到指定的文件（发布时由 CI 运行）。
// 在仓库根目录运行：
//
//	go run ./internal/llmstxt/gen
//	go run ./internal/llmstxt/gen -full llms-full.txt
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/go-tenon/tenon/internal/llmstxt"
)

// main 生成 llms.txt，或者用 -full 生成 llms-full.txt。
func main() {
	full := flag.String("full", "", "把 llms-full.txt 写到这个文件，不生成 llms.txt")
	flag.Parse()
	name := "llms.txt"
	if *full != "" {
		name = *full
	}
	b, err := llmstxt.Build(".", *full != "")
	if err == nil {
		err = os.WriteFile(name, b, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "llmstxt:", err)
		os.Exit(1)
	}
}
