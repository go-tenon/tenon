package filesystem

import (
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type serveConfig struct {
	attachment   bool
	redirectTTL  time.Duration
	cacheControl string
}

// ServeOption 是 Serve 的响应选项。
type ServeOption func(*serveConfig)

// ServeAttachment 强制浏览器以附件形式下载文件（Content-Disposition: attachment）。
func ServeAttachment() ServeOption {
	return func(c *serveConfig) { c.attachment = true }
}

// ServeRedirectSigned 让支持预签名读取的磁盘（例如私有 S3 桶）返回 302 限时签名地址，免去服务端中转流量。
func ServeRedirectSigned(ttl time.Duration) ServeOption {
	return func(c *serveConfig) { c.redirectTTL = ttl }
}

// ServeCacheControl 覆盖响应的 Cache-Control 头，默认为 private, max-age=300。
func ServeCacheControl(value string) ServeOption {
	return func(c *serveConfig) { c.cacheControl = value }
}

// Serve 把文件 id 经安全响应头写出到 HTTP 响应；文件不存在或不可读时直接返回错误（不写响应头）。
// 对 HTML/SVG/XML/JavaScript 等活跃类型自动附带 Content-Security-Policy: sandbox 与附件下载头；
// 对本地可定位文件流式使用 http.ServeContent 支持 HTTP Range 断点与音视频拖动播放。
func Serve(w http.ResponseWriter, r *http.Request, files *Service, id uuid.UUID, opts ...ServeOption) error {
	cfg := serveConfig{cacheControl: "private, max-age=300"}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	f, err := files.Get(r.Context(), id)
	if err != nil {
		return err
	}
	contentType := effectiveContentType(f)
	forceAttach := cfg.attachment || IsActiveType(contentType)
	disposition := ""
	if forceAttach {
		disposition = attachmentDisposition(f.Filename)
	}

	if cfg.redirectTTL > 0 {
		disk := files.disk(f)
		if rgetter, ok := disk.(ResponseSignGetter); ok {
			signed, err := rgetter.SignGetWith(r.Context(), f.Key, cfg.redirectTTL, SignedGetOptions{
				ContentType:        contentType,
				ContentDisposition: disposition,
			})
			if err != nil {
				return err
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, signed.URL, http.StatusFound)
			return nil
		}
		if getter, ok := disk.(SignGetter); ok && !forceAttach {
			signed, err := getter.SignGet(r.Context(), f.Key, cfg.redirectTTL)
			if err != nil {
				return err
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, signed.URL, http.StatusFound)
			return nil
		}
	}

	body, _, err := files.Open(r.Context(), id)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()

	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	if cfg.cacheControl != "" {
		h.Set("Cache-Control", cfg.cacheControl)
	}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	if forceAttach {
		h.Set("Content-Security-Policy", "sandbox")
		h.Set("Content-Disposition", disposition)
	}

	if rs, ok := body.(io.ReadSeeker); ok {
		http.ServeContent(w, r, f.Filename, f.UpdatedAt, rs)
		return nil
	}
	if f.Size >= 0 {
		h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, body)
	return err
}

// effectiveContentType 返回文件下发时的安全 Content-Type。
func effectiveContentType(f File) string {
	ct := mediaType(f.ContentType)
	if ct == "" || ct == "application/octet-stream" {
		if builtin := ContentTypeByExt(path.Ext(f.Key)); builtin != "" {
			return builtin
		}
	}
	if ct == "" {
		return "application/octet-stream"
	}
	return f.ContentType
}

// attachmentDisposition 生成规范转义的附件下载头。
func attachmentDisposition(filename string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(filename))
	clean = strings.ReplaceAll(clean, "/", "_")
	clean = strings.ReplaceAll(clean, "\\", "_")
	if clean == "" || clean == "." || clean == ".." {
		clean = "download"
	}
	formatted := mime.FormatMediaType("attachment", map[string]string{"filename": clean})
	if formatted == "" {
		return "attachment"
	}
	return formatted
}
