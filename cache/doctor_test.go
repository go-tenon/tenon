package cache_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/go-tenon/tenon/cache"
	"github.com/go-tenon/tenon/doctor"
)

func TestDoctor(t *testing.T) {
	t.Parallel()
	if res := doctor.Run(context.Background(), cache.Doctor(openRedis(t))).Results[0]; res.Status != doctor.Passed {
		t.Errorf("reachable server: %+v", res)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	ln.Close()
	rdb := redis.NewClient(&redis.Options{Addr: closed, DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	if res := doctor.Run(context.Background(), cache.Doctor(rdb)).Results[0]; res.Status != doctor.Warning {
		t.Errorf("server down: %+v", res)
	}
}
