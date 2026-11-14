package web_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/web"
)

func TestRoutesRejectNilHandler(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	pages().Routes(chi.NewRouter()).Get("/", nil)
}

func registerPages(r chi.Router, p *web.Pages, h web.HandlerFunc) {
	pages := p.Routes(r)
	pages.Get("/", h)
	pages.Get("/articles/{slug}", h)
	pages.Post("/subscribe", h)
	r.Handle(web.AssetsPath+"*", web.Assets())
	r.Get("/healthz", func(http.ResponseWriter, *http.Request) {})
}

func TestRoutesDescribeAccess(t *testing.T) {
	router := chi.NewRouter()
	registerPages(router, pages(), func(http.ResponseWriter, *http.Request) error {
		t.Fatal("route inspection invoked a page handler")
		return nil
	})
	want := []httpx.Route{
		{Method: "GET", Path: "/", Access: "public"},
		{Method: "*", Path: "/_web/*", Access: "public"},
		{Method: "GET", Path: "/articles/{slug}", Access: "public"},
		{Method: "GET", Path: "/healthz", Access: "unknown"},
		{Method: "POST", Path: "/subscribe", Access: "public"},
	}
	if got := httpx.Routes(router); !reflect.DeepEqual(got, want) {
		t.Fatalf("routes = %#v", got)
	}
}
