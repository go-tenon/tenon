// Package setting 是系统设置模块：管理员在后台修改、运行时读取的配置，如站点名称、是否开放注册。
//
// 设置按组定义为产品自己的结构体，每组以一个 JSON 文档保存在 system_settings 表的一行中。
// 结构体新增字段时不需要迁移，数据库中没有的字段取 Default 中的值：
//
//	type SiteSettings struct {
//		Name             string `json:"name" minLength:"1" maxLength:"50"`
//		RegistrationOpen bool   `json:"registration_open"`
//	}
//
//	var Site = setting.Group[SiteSettings]{Key: "site", Default: SiteSettings{Name: "我的产品", RegistrationOpen: true}}
//
//	settings := setting.New(a.DB)
//	site, err := Site.Get(ctx, settings)
//	err = Site.Set(ctx, settings, site)
//
// 读取结果在进程内缓存 Store.TTL（默认 10 秒）；Set 会清除本进程的缓存，
// 多实例部署时其他实例最多在 TTL 后读到新值。
//
// 迁移：把 setting.Migrations 加入 app.Options.Publish。
package setting

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations 是本模块的迁移。
var Migrations = database.MigrationSource{Name: "setting", FS: mustSub(migrationsFS, "migrations")}

type row struct {
	bun.BaseModel `bun:"table:system_settings"`

	Key       string          `bun:"key,pk"`
	UpdatedAt time.Time       `bun:"updated_at,notnull"`
	Value     json.RawMessage `bun:"value,type:jsonb,notnull"`
}

type cached struct {
	value []byte // nil 表示数据库中没有
	at    time.Time
}

// Store 读写系统设置。
type Store struct {
	db *bun.DB
	// TTL 是读取结果在进程内的缓存时间，默认 10 秒；为 0 时不缓存。
	TTL time.Duration

	mu    sync.Mutex
	cache map[string]cached
}

// New 返回 Store。
func New(db *bun.DB) *Store {
	return &Store{db: db, TTL: 10 * time.Second, cache: map[string]cached{}}
}

// raw 返回组 key 保存的 JSON，没有时返回 nil。
func (s *Store) raw(ctx context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	c, ok := s.cache[key]
	s.mu.Unlock()
	if ok && time.Since(c.at) < s.TTL {
		return c.value, nil
	}
	r := new(row)
	err := s.db.NewSelect().Model(r).Where("key = ?", key).Scan(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("setting: get %s: %w", key, err)
	}
	if s.TTL > 0 {
		s.mu.Lock()
		s.cache[key] = cached{value: r.Value, at: time.Now()}
		s.mu.Unlock()
	}
	return r.Value, nil
}

func (s *Store) put(ctx context.Context, key string, value []byte) error {
	_, err := s.db.NewInsert().Model(&row{Key: key, UpdatedAt: time.Now(), Value: value}).
		On("CONFLICT (key) DO UPDATE").
		Set("value = EXCLUDED.value, updated_at = EXCLUDED.updated_at").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("setting: set %s: %w", key, err)
	}
	s.mu.Lock()
	delete(s.cache, key)
	s.mu.Unlock()
	return nil
}

// Group 是一组类型化的系统设置。T 必须能被 encoding/json 编解码。
type Group[T any] struct {
	Key     string // 在 system_settings 表中的键，定义后不要修改
	Default T      // 数据库中没有该组或缺少某个字段时使用的值
}

// Get 返回当前设置：以 Default 为底，覆盖数据库中保存的字段。
func (g Group[T]) Get(ctx context.Context, s *Store) (T, error) {
	var v T
	def, err := json.Marshal(g.Default)
	if err != nil {
		return v, fmt.Errorf("setting: %s: %w", g.Key, err)
	}
	if err := json.Unmarshal(def, &v); err != nil { // 深拷贝 Default，避免修改共享的切片与 map
		return v, fmt.Errorf("setting: %s: %w", g.Key, err)
	}
	raw, err := s.raw(ctx, g.Key)
	if err != nil {
		return v, err
	}
	if raw != nil {
		if err := json.Unmarshal(raw, &v); err != nil {
			return v, fmt.Errorf("setting: decode %s: %w", g.Key, err)
		}
	}
	return v, nil
}

// Set 保存整组设置。
func (g Group[T]) Set(ctx context.Context, s *Store, v T) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("setting: encode %s: %w", g.Key, err)
	}
	return s.put(ctx, g.Key, raw)
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
