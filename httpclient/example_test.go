package httpclient_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/shellphy-kit/tools/errs"
	"github.com/shellphy-kit/tools/httpclient"
)

type Order struct {
	ID int `json:"id"`
}

func getOrder(ctx context.Context, c *http.Client, url string) (Order, error) {
	var out Order
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return out, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return out, fmt.Errorf("get order: %w", err)
	}
	defer resp.Body.Close()
	if err := httpclient.Check(resp); err != nil {
		return out, fmt.Errorf("get order: %w", err)
	}
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func Example() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/busy" {
			http.Error(w, "try later", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"id":42}`)
	}))
	defer srv.Close()

	client := httpclient.New(httpclient.Options{})

	order, err := getOrder(context.Background(), client, srv.URL+"/orders/42")
	fmt.Println(order.ID, err)

	_, err = getOrder(context.Background(), client, srv.URL+"/busy")
	fmt.Println(errs.CodeOf(err))

	// Output:
	// 42 <nil>
	// unavailable
}
