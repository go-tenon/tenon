package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed vendor/htmx.min.js vendor/htmx.LICENSE
var vendorFS embed.FS

// Vendor 包含内置的前端依赖，目前为 htmx.min.js（版本 HTMXVersion）及其许可证 htmx.LICENSE。
// 用 NewAssets 提供服务：
//
//	vendor, err := web.NewAssets("/vendor/", web.Vendor)
var Vendor fs.FS = mustSub(vendorFS, "vendor")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// Assets 提供静态资源服务，并生成带内容版本号的 URL。
//
// go:embed 的文件没有修改时间，直接用 http.FileServer 时浏览器既无法缓存也无法条件请求。
// Assets 在创建时计算每个文件的内容哈希：
//
//   - URL(name) 返回 "<prefix><name>?v=<hash>"，内容变化时 URL 随之变化；
//   - 带有正确 v 参数的请求设置一年的 immutable 缓存；
//   - 其他请求设置 Cache-Control: no-cache，并以哈希作为 ETag 支持 304。
//
// 只提供创建时存在的文件，目录与不存在的文件均返回 404。
type Assets struct {
	prefix string
	fsys   fs.FS
	hashes map[string]string
}

// NewAssets 返回以 prefix（如 "/static/"）为 URL 前缀、提供 fsys 中文件的 Assets。
// 用 chi 挂载时路由为 prefix + "*"：
//
//	static, err := web.NewAssets("/static/", assets.FS)
//	r.Handle("/static/*", static)
func NewAssets(prefix string, fsys fs.FS) (*Assets, error) {
	if !strings.HasPrefix(prefix, "/") || !strings.HasSuffix(prefix, "/") {
		return nil, fmt.Errorf("web: assets prefix %q must start and end with /", prefix)
	}
	a := &Assets{prefix: prefix, fsys: fsys, hashes: map[string]string{}}
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		a.hashes[name] = hex.EncodeToString(sum[:6])
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("web: assets: %w", err)
	}
	return a, nil
}

// URL 返回 name（相对于 fsys 根目录，如 "css/app.css"）带版本号的 URL。
// name 不存在时返回不带版本号的 URL。
func (a *Assets) URL(name string) string {
	name = strings.TrimPrefix(name, "/")
	if h, ok := a.hashes[name]; ok {
		return a.prefix + name + "?v=" + h
	}
	return a.prefix + name
}

func (a *Assets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutPrefix(r.URL.Path, a.prefix)
	hash, exists := a.hashes[name]
	if !ok || !exists {
		http.NotFound(w, r)
		return
	}

	h := w.Header()
	if r.URL.Query().Get("v") == hash {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	h.Set("ETag", `"`+hash+`"`)
	http.ServeFileFS(w, r, a.fsys, name)
}
