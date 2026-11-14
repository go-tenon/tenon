package clientgen_test

import (
	"context"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/httpx/rest/clientgen"
	"github.com/go-tenon/tenon/identity"
)

type noteInput struct {
	Body struct {
		Title string `json:"title" minLength:"1" maxLength:"80"`
	}
}

// adminAPI 是带权限目录的小 API：note-create 要求 notes:write，note-list 公开。
func adminAPI(permissions []identity.Permission, required ...string) *rest.API {
	api := rest.New(chi.NewRouter(), rest.Options{Title: "Admin", BasePath: "/api/admin",
		Accounts: identity.Accounts{"staff": nil}, Permissions: permissions})
	validate := func(context.Context, string) (identity.Subject, error) {
		return identity.Subject{Type: "staff", ID: "a"}, nil
	}
	create := huma.Operation{OperationID: "note-create", Method: http.MethodPost, Path: "/notes"}
	rest.RequireBearer(api, validate, required...)(&create)
	rest.Register(api, create, func(context.Context, *noteInput) (*struct{}, error) { return nil, nil })
	list := huma.Operation{OperationID: "note-list", Method: http.MethodGet, Path: "/notes"}
	rest.Public()(&list)
	rest.Register(api, list, func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })
	return api
}

var catalog = []identity.Permission{
	{Name: "notes:write", Description: "写笔记"},
	{Name: "notes:admin", Description: "管理所有笔记"},
}

func TestTSMetadata(t *testing.T) {
	app := fstest.MapFS{"zh-CN.yaml": {Data: []byte("validation.filled: 必须填写\nnote.missing: 没有这条笔记\n")}}
	res, err := clientgen.GenerateTS(adminAPI(catalog, "notes:write"), clientgen.TSOptions{Name: "admin", Translations: []fs.FS{app}, Main: exampleMain})
	if err != nil {
		t.Fatal(err)
	}
	for file, wants := range map[string][]string{
		// 目录中的权限按名称排序，带说明；接口要求的权限按分组列出，公开的接口不出现。
		"permissions.ts": {"  \"notes:admin\": \"管理所有笔记\",\n  \"notes:write\": \"写笔记\",\n", "  note: {\n    create: [\"notes:write\"],\n  },\n"},
		// 应用的翻译覆盖内置文案；只有 validation. 开头的消息。
		"messages.ts": {`"validation.filled": "必须填写"`, `"validation.min_length": "至少 {{.Min}} 个字符"`},
		// 请求体的校验规则在 components 中，说明等字段不保留。
		"schemas.ts": {`"$ref": "#/components/schemas/NoteInputBody"`, `"maxLength": 80`, `"minLength": 1`},
		"queries.ts": {`[...(options.scope?.() ?? []), "admin", ...parts]`, `queryKey: key("note", "list", ...args)`, `onSuccess: () => invalidate("note")`},
	} {
		got := string(res.Files[file])
		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf("%s missing %q:\n%s", file, want, got)
			}
		}
	}
	if strings.Contains(string(res.Files["messages.ts"]), "note.missing") {
		t.Error("messages.ts includes non-validation messages")
	}
	if strings.Contains(string(res.Files["permissions.ts"]), "list:") {
		t.Error("public operation listed in required permissions")
	}

	// 没有权限目录时，只列出接口要求的权限。
	res, err = clientgen.GenerateTS(adminAPI(nil, "notes:write"), clientgen.TSOptions{Main: exampleMain})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(res.Files["permissions.ts"]); !strings.Contains(got, `"notes:write": "",`) || strings.Contains(got, "notes:admin") {
		t.Errorf("permissions without a catalog:\n%s", got)
	}
	// 有权限目录时，接口要求的权限必须在目录中。
	if _, err := clientgen.GenerateTS(adminAPI(catalog, "notes:delete"), clientgen.TSOptions{Main: exampleMain}); err == nil ||
		!strings.Contains(err.Error(), `note-create requires permission "notes:delete"`) {
		t.Errorf("unknown permission: %v", err)
	}
}

// 枚举的显示文字：一种语言只有部分取值有显示文字，或者所有语言都没有时警告。
func TestTSEnumWarnings(t *testing.T) {
	for name, c := range map[string]struct {
		translations fstest.MapFS
		want         string
	}{
		"partial": {fstest.MapFS{"zh-CN.yaml": {Data: []byte("article.status.draft: 草稿\n")}}, "enum ArticleStatus has no zh-CN label for published"},
		"none":    {fstest.MapFS{}, "enum ArticleStatus has no labels"},
	} {
		res, err := clientgen.GenerateTS(exampleAPI(nil), clientgen.TSOptions{Main: exampleMain, Translations: []fs.FS{c.translations}})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], c.want) {
			t.Errorf("%s: warnings %q", name, res.Warnings)
		}
	}
}

// 权限目录写入 OpenAPI 的 x-tenon-permissions，按名称排序。
func TestPermissionsInOpenAPI(t *testing.T) {
	data, err := adminAPI(catalog, "notes:write").OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `"x-tenon-permissions":[{"name":"notes:admin","description":"管理所有笔记"},{"name":"notes:write","description":"写笔记"}]`
	if !strings.Contains(string(data), want) {
		t.Errorf("openapi.json missing %s:\n%s", want, data)
	}
}
