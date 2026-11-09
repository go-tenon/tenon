package main

import (
	"context"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	redis_store "github.com/eko/gocache/store/redis/v4"
	gocache "github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"

	"github.com/ryan053857/mygoravel/cleanup"
)

// The application owns native clients; the service receives the upstream cache interface.
func openCache(ctx context.Context, cfg cacheConfig, stack *cleanup.Stack) (cache.CacheInterface[string], error) {
	switch cfg.Driver {
	case "memory":
		// Own the expiry loop explicitly: go-cache's built-in janitor has no Close.
		client := gocache.New(cfg.TTL, 0)
		ticker := time.NewTicker(cfg.TTL)
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-ticker.C:
					client.DeleteExpired()
				case <-stop:
					return
				}
			}
		}()
		_ = stack.Add("memory cache", func(context.Context) error { ticker.Stop(); close(stop); <-done; client.Flush(); return nil })
		return cache.New[string](gocache_store.NewGoCache(client)), nil
	case "redis":
		client := redis.NewClient(&redis.Options{Addr: cfg.Addr, DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, MaxRetries: -1})
		_ = stack.Add("redis", func(context.Context) error { return client.Close() })
		probe, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := client.Ping(probe).Err(); err != nil {
			return nil, err
		}
		return cache.New[string](redis_store.NewRedis(client)), nil
	default:
		return nil, nil
	}
}
