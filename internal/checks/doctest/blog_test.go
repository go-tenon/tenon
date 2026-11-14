package doctest_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docFile 是文档中给出完整代码的一个博客示例文件。
type docFile struct {
	path string // 在博客应用中的路径，正文在代码块之前写出它
	doc  string // docs 目录下的文档
}

// blogFiles 是要编译的完整文件。其余的包在 testdata/blog 中只声明用到的部分。
var blogFiles = []docFile{
	{"internal/auth/auth.go", "security/authentication.md"},
	{"internal/auth/permissions.go", "security/authorization.md"},
	{"internal/auth/social/social.go", "security/social-login.md"},
	{"internal/auth/authhttp/web_social.go", "security/social-login.md"},
	{"internal/auth/authhttp/social.go", "security/social-login.md"},
	{"internal/article/import.go", "features/import-export.md"},
	{"internal/article/export.go", "features/import-export.md"},
	{"internal/article/queue_import.go", "features/import-export.md"},
	{"internal/article/queue_export.go", "features/import-export.md"},
	{"internal/article/bulk_publish.go", "features/operations.md"},
	{"internal/article/search.go", "features/search.md"},
	{"internal/article/tools.go", "ai/mcp.md"},
	{"internal/attachment/vector.go", "ai/vector.md"},
	{"internal/editor/editor.go", "security/account-examples.md"},
	{"internal/reader/reader.go", "security/account-examples.md"},
	{"internal/accounts/accounts.go", "security/account-examples.md"},
	{"internal/editor/editorapi/routes.go", "security/account-examples.md"},
	{"internal/reader/readerapi/routes.go", "security/account-examples.md"},
}

// blogFragments 是文档中没有 package 和 import 的片段，补上它们后作为文件编译。
var blogFragments = []struct {
	path, doc, prefix, header string
}{
	{"internal/article/import_template.go", "features/import-export.md", "type TemplateInput struct", `package article

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/sheet"
)
`},
	{"internal/attachment/find.go", "ai/vector.md", "type Find struct", `package attachment

import (
	"context"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/vector"
)
`},
	{"internal/article/publish_tool.go", "ai/mcp.md", "type PublishArgs struct", `package article

import (
	"context"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/identity"
)
`},
	{"internal/article/search_tool.go", "ai/mcp.md", "type SearchArgs struct", `package article

import (
	"context"

	"github.com/go-tenon/tenon/page"
	"github.com/go-tenon/tenon/search"
)
`},
	{"internal/bootstrap/mailer.go", "libraries/mail.md", "func newMailer(", `package bootstrap

import (
	"example.com/blog/internal/settings"
	"github.com/go-tenon/tenon/mail"
	"github.com/go-tenon/tenon/mail/logmail"
	"github.com/go-tenon/tenon/mail/resend"
	"github.com/go-tenon/tenon/mail/ses"
	"github.com/go-tenon/tenon/mail/smtp"
)
`},
}

var goBlock = regexp.MustCompile("(?s)```go\n(.*?)```")

// TestBlogExampleCompiles 把文档中博客示例的入口代码提取到一个临时 module 中编译，
// 用 go vet 检查（包含编译），防止文档中的完整文件与框架 API 或彼此之间不一致。
func TestBlogExampleCompiles(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("testdata/blog")); err != nil {
		t.Fatal(err)
	}

	for _, f := range blogFiles {
		writeFile(t, dir, f.path, completeFile(t, root, f))
	}
	for _, f := range blogFragments {
		writeFile(t, dir, f.path, f.header+"\n"+fragment(t, root, f.doc, f.prefix))
	}

	gomod := "module example.com/blog\n\ngo 1.27.3\n\nrequire github.com/go-tenon/tenon v0.0.0\n\nreplace github.com/go-tenon/tenon => " + root + "\n"
	writeFile(t, dir, "go.mod", gomod)
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "go.sum", string(sum))

	cmd := exec.Command("go", "vet", "-tags", "integration", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
}

// completeFile 返回 f.doc 中路径为 f.path 的完整文件：以 package 或构建标签开头、
// 并且在它与上一个代码块之间的正文中写出了 f.path 的代码块。每个路径只能出现一次。
func completeFile(t *testing.T, root string, f docFile) string {
	t.Helper()
	text := readDoc(t, root, f.doc)
	var found []string
	prev := 0
	for _, m := range goBlock.FindAllStringSubmatchIndex(text, -1) {
		prose, body := text[prev:m[0]], text[m[2]:m[3]]
		prev = m[1]
		isFile := strings.HasPrefix(body, "package ") || strings.HasPrefix(body, "//go:build ")
		if isFile && strings.Contains(prose, "`"+f.path+"`") {
			found = append(found, body)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s: found %d complete files for %s, want 1", f.doc, len(found), f.path)
	}
	return found[0]
}

// fragment 返回 doc 中以 prefix 开头的唯一代码块。
func fragment(t *testing.T, root, doc, prefix string) string {
	t.Helper()
	var found []string
	for _, m := range goBlock.FindAllStringSubmatch(readDoc(t, root, doc), -1) {
		if strings.HasPrefix(m[1], prefix) {
			found = append(found, m[1])
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s: found %d blocks starting with %q, want 1", doc, len(found), prefix)
	}
	return found[0]
}

func readDoc(t *testing.T, root, name string) string {
	t.Helper()
	b, err := fs.ReadFile(os.DirFS(filepath.Join(root, "docs")), name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
