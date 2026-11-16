package cachex_test

import (
	"context"
	"net"
	"os"
	"testing"

	"github.com/bradfitz/gomemcache/memcache"

	"github.com/go-tenon/tenon/cachex"
	"github.com/go-tenon/tenon/doctor"
)

func TestDoctor(t *testing.T) {
	t.Parallel()
	if res := doctor.Run(context.Background(), cachex.Doctor(openMemcached(t))).Results[0]; res.Status != doctor.Passed {
		t.Errorf("reachable server: %+v", res)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	ln.Close()
	mc := memcache.New(os.Getenv("MEMCACHED_TEST_ADDR"), closed)
	t.Cleanup(func() { _ = mc.Close() })
	if res := doctor.Run(context.Background(), cachex.Doctor(mc)).Results[0]; res.Status != doctor.Warning {
		t.Errorf("one server down: %+v", res)
	}
}
