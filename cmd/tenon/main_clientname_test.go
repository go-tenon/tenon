package main

import (
	"testing"

	"github.com/go-tenon/tenon/clientgen"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// TestClientNameMatchesClientgen 检查 add frontend 找客户端用的目录名与 tenon generate 生成客户端的目录名相同。
func TestClientNameMatchesClientgen(t *testing.T) {
	t.Parallel()
	cfg := &project.Config{APIs: map[string]*project.API{"/v2/api": {Name: "next"}}}
	for _, base := range []string{"/api", "/api/v1", "/api/admin", "/admin/api", "/t/{tenant}/api", "/console"} {
		if got, want := cfg.ClientName(base), clientgen.DefaultTSDir(base); got != want {
			t.Errorf("%s: %q, clientgen %q", base, got, want)
		}
	}
	if got := cfg.ClientName("/v2/api"); got != "next" {
		t.Errorf("tenon.json 中的名称：%q", got)
	}
}
