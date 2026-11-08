package httpclient

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/shellphy-kit/tools/errs"
)

type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error { b.closed = true; return nil }

func response(status int, body string) (*http.Response, *trackingBody) {
	b := &trackingBody{Reader: strings.NewReader(body)}
	u, _ := url.Parse("https://api.example.com/v1/orders?token=secret")
	return &http.Response{
		StatusCode: status,
		Body:       b,
		Request:    &http.Request{Method: http.MethodPost, URL: u},
	}, b
}

func TestCheckSuccess(t *testing.T) {
	for _, status := range []int{200, 201, 204, 299} {
		resp, body := response(status, "ok")
		if err := Check(resp); err != nil {
			t.Errorf("%d: unexpected error %v", status, err)
		}
		if body.closed {
			t.Errorf("%d: body must stay open for the caller", status)
		}
	}
}

func TestCheckClassification(t *testing.T) {
	cases := []struct {
		status int
		want   errs.Code
	}{
		{301, errs.Internal},
		{400, errs.Internal},
		{401, errs.Internal},
		{404, errs.Internal},
		{429, errs.ResourceExhausted},
		{500, errs.Internal},
		{502, errs.Unavailable},
		{503, errs.Unavailable},
		{504, errs.Unavailable},
	}
	for _, c := range cases {
		resp, body := response(c.status, `{"error":"x"}`)
		err := Check(resp)
		if got := errs.CodeOf(err); got != c.want {
			t.Errorf("%d: CodeOf = %q, want %q", c.status, got, c.want)
		}
		if !body.closed {
			t.Errorf("%d: body must be closed on error", c.status)
		}
		se, ok := errors.AsType[*StatusError](err)
		if !ok || se.StatusCode != c.status {
			t.Errorf("%d: want *StatusError, got %v", c.status, err)
		}
		if errs.MessageOf(err) != "" {
			t.Errorf("%d: downstream details must not become a user message", c.status)
		}
	}
}

func TestStatusError(t *testing.T) {
	resp, _ := response(404, `{"error":"not found"}`)
	se, _ := errors.AsType[*StatusError](Check(resp))
	want := `POST api.example.com/v1/orders: 404 Not Found: {"error":"not found"}`
	if se.Error() != want {
		t.Errorf("got  %q\nwant %q", se.Error(), want)
	}
	if strings.Contains(se.Error(), "secret") {
		t.Error("query must not appear in the error")
	}
}

func TestCheckTruncatesBody(t *testing.T) {
	resp, _ := response(500, strings.Repeat("x", 10*maxErrorBody))
	se, _ := errors.AsType[*StatusError](Check(resp))
	if len(se.Body) != maxErrorBody {
		t.Errorf("len(Body) = %d, want %d", len(se.Body), maxErrorBody)
	}
}

func TestCheckWithoutRequest(t *testing.T) {
	resp, _ := response(500, "")
	resp.Request = nil
	se, _ := errors.AsType[*StatusError](Check(resp))
	if se == nil || se.Error() != " : 500 Internal Server Error" {
		t.Errorf("got %v", se)
	}
}
