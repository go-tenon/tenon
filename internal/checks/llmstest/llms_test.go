package llmstest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/internal/llmstxt"
)

// TestUpToDate 检查仓库根目录的 llms.txt 与 docs/README.md 生成的结果一致，docs 中每一页都列在目录中。
// 改了文档的目录后，在仓库根目录运行 go run ./internal/llmstxt/gen 重新生成。
func TestUpToDate(t *testing.T) {
	t.Parallel()
	root := "../../.."
	want, err := llmstxt.Build(root, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "llms.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("llms.txt is out of date; run go run ./internal/llmstxt/gen in the repository root")
	}
}

// TestFull 检查 llms-full.txt 附上了每一页，页中的相对链接都换成了绝对地址。
func TestFull(t *testing.T) {
	t.Parallel()
	b, err := llmstxt.Build("../../..", true)
	if err != nil {
		t.Fatal(err)
	}
	full := string(b)
	for _, page := range []string{"docs/http/routing.md", "docs/tutorial/08-admin.md", "docs/security/tenancy.md"} {
		if !strings.Contains(full, "Source: "+llmstxt.Base+page+"\n") {
			t.Errorf("llms-full.txt has no %s", page)
		}
	}
	if !strings.Contains(full, "]("+llmstxt.Base+"docs/concepts/actions.md") {
		t.Error("relative links are not rewritten")
	}
}
