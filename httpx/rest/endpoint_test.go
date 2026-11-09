package rest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/rest"
)

type echoInput struct {
	Body struct {
		Text string `json:"text"`
	}
}

type echoOutput struct {
	Body struct {
		Text string `json:"text"`
	}
}

// echo 模拟 go-kit 模块提供的 Endpoint。
func echo() rest.Endpoint[echoInput, echoOutput] {
	return rest.Endpoint[echoInput, echoOutput]{
		Operation: huma.Operation{Summary: "回显", Tags: []string{"demo"}},
		Handler: func(_ context.Context, in *echoInput) (*echoOutput, error) {
			out := &echoOutput{}
			out.Body.Text = in.Body.Text
			return out, nil
		},
	}
}

func TestEndpoint(t *testing.T) {
	r := chi.NewRouter()
	api := humachi.New(r, huma.DefaultConfig("Test", "1.0.0"))
	v1 := huma.NewGroup(api, "/api/v1")

	rest.Post(v1, "/echo", echo())
	rest.Put(v1, "/echo2", echo(), func(o *huma.Operation) {
		o.OperationID = "custom-echo"
		o.Tags = append(o.Tags, "extra")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/echo", strings.NewReader(`{"text":"hi"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"text":"hi"`) {
		t.Fatalf("POST /echo = %d %s", rec.Code, rec.Body)
	}

	paths := api.OpenAPI().Paths
	post := paths["/api/v1/echo"].Post
	if post == nil || post.Summary != "回显" || post.OperationID == "" || post.Tags[0] != "demo" {
		t.Errorf("post operation = %+v", post)
	}
	put := paths["/api/v1/echo2"].Put
	if put == nil || put.OperationID != "custom-echo" || len(put.Tags) != 2 {
		t.Errorf("put operation = %+v", put)
	}
	// 同一个 Endpoint 挂载两次，元数据互不影响。
	if len(post.Tags) != 1 {
		t.Errorf("tags leaked between mounts: %v", post.Tags)
	}
}
