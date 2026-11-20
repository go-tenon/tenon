package rest_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/httpx/rest"
)

// TestDocsTemplatedBasePath 检查 API 挂在带参数的 BasePath 下时，文档页和 OpenAPI 文档中的地址换成这次请求实际的前缀；
// BasePath 不带参数时文档保持原样。
func TestDocsTemplatedBasePath(t *testing.T) {
	t.Parallel()
	root := chi.NewRouter()
	root.Route("/t/{slug}/api", func(r chi.Router) {
		rest.New(r, rest.Options{Title: "tenant", BasePath: "/t/{slug}/api"})
	})
	root.Route("/api", func(r chi.Router) {
		rest.New(r, rest.Options{Title: "plain", BasePath: "/api"})
	})
	for path, want := range map[string]string{
		"/t/demo/api/docs":             `apiDescriptionUrl="/t/demo/api/openapi.yaml"`,
		"/t/demo/api/openapi.json":     `"url":"/t/demo/api"`,
		"/t/demo/api/openapi.yaml":     `/t/demo/api`,
		"/t/demo/api/openapi-3.0.json": `"url":"/t/demo/api"`,
		"/api/docs":                    `apiDescriptionUrl="/api/openapi.yaml"`,
	} {
		w := httptest.NewRecorder()
		root.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		body := w.Body.String()
		if w.Code != http.StatusOK || !strings.Contains(body, want) || strings.Contains(body, "{slug}") {
			t.Errorf("%s：%d %s", path, w.Code, body[:min(300, len(body))])
		}
	}
	// 文档页的内容安全策略保持原样
	w := httptest.NewRecorder()
	root.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/t/demo/api/docs", nil))
	if w.Header().Get("Content-Security-Policy") == "" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("文档页的响应头：%v", w.Header())
	}
}
