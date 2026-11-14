package clientgen_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/httpx/rest/clientgen"
)

const exampleTSOut = "internal/example/ts"

// exampleTranslations 是示例应用的翻译文件：文章状态的显示文字。
var exampleTranslations = fstest.MapFS{
	"zh-CN.yaml": {Data: []byte("article.status.draft: 草稿\narticle.status.published: 已发布\n")},
	"en.yaml":    {Data: []byte("article.status.draft: Draft\narticle.status.published: Published\n")},
}

func generateTS(t *testing.T, api *rest.API) *clientgen.Result {
	t.Helper()
	res, err := clientgen.GenerateTS(api, clientgen.TSOptions{Main: exampleMain, Translations: []fs.FS{exampleTranslations}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestGeneratedTSExampleIsUpToDate(t *testing.T) {
	res := generateTS(t, exampleAPI(nil))
	if len(res.Skipped)+len(res.Warnings) > 0 {
		t.Fatalf("skipped %q, warnings %q", res.Skipped, res.Warnings)
	}
	if *update {
		if err := clientgen.Write(exampleTSOut, res.Files); err != nil {
			t.Fatal(err)
		}
		return
	}
	for name, data := range res.Files {
		disk, err := os.ReadFile(filepath.Join(exampleTSOut, name))
		if err != nil || string(disk) != string(data) {
			t.Errorf("%s is out of date; run go test ./httpx/rest/clientgen -run TestGeneratedTSExampleIsUpToDate -update", name)
		}
	}
}

// TestTSClientCompiles 用严格模式的 tsc 检查生成的代码。需要 bun，没有时跳过。
func TestTSClientCompiles(t *testing.T) {
	bun := requireBun(t)
	cmd := exec.Command(bun, "x", "-p", "typescript@5.9.3", "tsc", "--noEmit", "--strict", "--target", "es2022",
		"--lib", "es2022,dom,dom.iterable", "--module", "esnext", "--moduleResolution", "bundler", "--verbatimModuleSyntax",
		"api.ts", "errors.ts", "runtime.ts", "schemas.ts", "messages.ts", "permissions.ts", "queries.ts", "enums.ts")
	cmd.Dir = exampleTSOut
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, out)
	}
}

// TestTSRoundTrip 用 bun 运行 testdata/roundtrip.ts：生成的客户端调用真实的示例服务。
func TestTSRoundTrip(t *testing.T) {
	bun := requireBun(t)
	base := serve(t, tokens{"secret": true})
	client, err := filepath.Abs(filepath.Join(exampleTSOut, "api.ts"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bun, "run", "testdata/roundtrip.ts")
	cmd.Env = append(os.Environ(), "BASE_URL="+base+"/api", "CLIENT="+client)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "roundtrip ok") {
		t.Fatalf("bun: %v\n%s", err, out)
	}
}

// TestTSValidationMatchesServer 用 bun 运行 testdata/validate.ts：同一批输入分别用 @go-tenon/client 的 validate
// 和示例服务校验，位置、错误码和提示必须一致。
func TestTSValidationMatchesServer(t *testing.T) {
	bun := requireBun(t)
	base := serve(t, tokens{"secret": true})
	example, err := filepath.Abs(exampleTSOut)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := filepath.Abs("../../../js/client/src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bun, "run", "testdata/validate.ts")
	cmd.Env = append(os.Environ(), "BASE_URL="+base+"/api", "EXAMPLE="+example, "LIB="+lib)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "validation matches") {
		t.Fatalf("bun: %v\n%s", err, out)
	}
}

func requireBun(t *testing.T) string {
	t.Helper()
	bun, err := exec.LookPath("bun")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("bun is required in CI")
		}
		t.Skip("bun not installed")
	}
	return bun
}
