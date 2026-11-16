package natsrpc

import (
	"testing"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx"
)

// 状态码与 HTTP 接口相同，调用方能把它还原为同一个分类。
func TestStatusMatchesHTTP(t *testing.T) {
	t.Parallel()
	for _, kind := range []errorx.Kind{errorx.InvalidArgument, errorx.Unauthenticated, errorx.PermissionDenied, errorx.NotFound,
		errorx.Conflict, errorx.ResourceExhausted, errorx.Unavailable, errorx.Internal} {
		err := errorx.Define(kind, "test.kind").New()
		if got, want := statusOf(kind), httpx.StatusOf(err); got != want {
			t.Errorf("%v: status %d, HTTP %d", kind, got, want)
		}
		if back := kindOf(statusOf(kind)); back != kind {
			t.Errorf("%v: maps back to %v", kind, back)
		}
	}
	if kindOf(400) != errorx.InvalidArgument || kindOf(418) != errorx.Internal {
		t.Error("other statuses")
	}
}

func TestValidRequestID(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]bool{"abc-123": true, "": false, "has space": false, string(make([]byte, 129)): false, "é": false} {
		if validRequestID(id) != want {
			t.Errorf("%q: want %v", id, want)
		}
	}
}
