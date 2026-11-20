// Package llmstxt 由文档生成给大模型读的 llms.txt（格式见 https://llmstxt.org）：一段英文简介加上 docs/README.md 的目录。
// 其中的相对链接都换成 Base 下的绝对地址，在线对话的 AI 拿到 llms.txt 就能顺着链接读到每一页的 Markdown。
package llmstxt

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Base 是仓库中文件的原始地址，相对链接按仓库中的路径接在它后面。
const Base = "https://raw.githubusercontent.com/go-tenon/tenon/main/"

// Index 是 docs 的目录，它链接到 docs 下除各组 README.md 以外的每一页。
const Index = "docs/README.md"

// header 是 llms.txt 的开头：标题、英文简介，以及文档的语言和本地文档的位置。
const header = `# Tenon

> Tenon is a Go framework for building backend services. Business logic is written as actions; HTTP APIs, web pages (React, Mantine and TanStack Start), queue workers, scheduled jobs, CLI commands, MCP tools and RPC are entry points that call them. The application, front end included, compiles to a single binary.

The documentation is written in Chinese and organized by topic. Each link below is the Markdown source of one page on the main branch; read only the pages a task needs.

In a Tenon project, the documentation that matches the framework version in go.mod is in the Go module cache: ` + "`go list -m -f '{{.Dir}}' github.com/go-tenon/tenon`" + ` prints the directory, and the pages are in its docs directory.

`

// link 匹配 Markdown 链接的目标。
var link = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// Build 读取仓库 root 中的文档，返回 llms.txt。
// docs 下有页面没有列在 Index 中时返回错误：它在 llms.txt 中会缺失。
func Build(root string) ([]byte, error) {
	index, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(Index)))
	if err != nil {
		return nil, err
	}
	// 去掉目录自己的标题，用 header 中的标题和简介代替。
	_, body, ok := strings.Cut(string(index), "\n")
	if !ok || !strings.HasPrefix(string(index), "# ") {
		return nil, fmt.Errorf("%s does not start with a title", Index)
	}
	pages := linked(body, path.Dir(Index))
	listed := map[string]bool{}
	for _, p := range pages {
		listed[p] = true
	}
	err = filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") || d.Name() == "README.md" {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); !listed[rel] {
			return fmt.Errorf("%s is not listed in %s", rel, Index)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.WriteString(header)
	out.WriteString(strings.TrimLeft(rewrite(body, path.Dir(Index)), "\n"))
	return out.Bytes(), nil
}

// linked 返回 text 中相对链接指向的文件在仓库中的路径，按第一次出现的顺序去重。dir 是 text 所在的目录。
func linked(text, dir string) []string {
	var pages []string
	seen := map[string]bool{}
	prose(text, func(s string) string {
		for _, m := range link.FindAllStringSubmatch(s, -1) {
			p, _, _ := strings.Cut(m[1], "#")
			if p == "" || external(p) {
				continue
			}
			if p = path.Join(dir, p); !seen[p] {
				seen[p] = true
				pages = append(pages, p)
			}
		}
		return s
	})
	return pages
}

// rewrite 把 text 中的相对链接换成 Base 下的绝对地址。dir 是 text 所在的目录；只有锚点的链接指向这一页自己，
// 这时 page 是空的，按 dir 无法还原，保持原样。
func rewrite(text, dir string) string {
	return prose(text, func(s string) string {
		return link.ReplaceAllStringFunc(s, func(m string) string {
			target := link.FindStringSubmatch(m)[1]
			p, frag, hasFrag := strings.Cut(target, "#")
			if p == "" || external(p) {
				return m
			}
			url := Base + path.Join(dir, p)
			if hasFrag {
				url += "#" + frag
			}
			return "](" + url + ")"
		})
	})
}

// external 报告链接目标是否是带协议的地址，例如 https:// 或 mailto:。
func external(target string) bool {
	return strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:")
}

// prose 对 text 中代码块和行内代码以外的部分调用 f，返回替换后的文本：代码中的 a[i](x) 不是链接。
func prose(text string, f func(string) string) string {
	lines := strings.SplitAfter(text, "\n")
	fenced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		parts := strings.Split(line, "`")
		for j := 0; j < len(parts); j += 2 {
			parts[j] = f(parts[j])
		}
		lines[i] = strings.Join(parts, "`")
	}
	return strings.Join(lines, "")
}
