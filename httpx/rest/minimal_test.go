package rest_test

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/mygoravel/httpx"
	"github.com/ryan053857/mygoravel/httpx/rest"
)

func TestMinimalHTTPWithoutDatabaseOrCache(t *testing.T) {
	api := rest.New(rest.Options{Title: "minimal"})
	rest.Register(api, huma.Operation{Security: []map[string][]string{},
		OperationID: "ping",
		Method:      http.MethodGet,
		Path:        "/ping",
	}, func() func(context.Context, *struct{}) (*struct {
		Body struct {
			OK bool `json:"ok"`
		}
	}, error) {
		return func(context.Context, *struct{}) (*struct {
			Body struct {
				OK bool `json:"ok"`
			}
		}, error) {
			out := &struct {
				Body struct {
					OK bool `json:"ok"`
				}
			}{}
			out.Body.OK = true
			return out, nil
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: api.Handler()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpx.Serve(ctx, srv, ln, httpx.ServeOptions{ShutdownTimeout: time.Second})
	}()

	resp, err := http.Get("http://" + ln.Addr().String() + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}
