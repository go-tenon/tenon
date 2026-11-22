package filesystem

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/cockroachdb/errors"
)

const (
	sniffBytes     = 8 << 10  // 文本和魔数探测读取的前缀长度
	maxZipInspect  = 64 << 20 // 校验 OOXML（docx/xlsx/pptx）结构时允许读取的最大体积
	maxJSONInspect = 32 << 20 // 校验 JSON 文件合法性时允许读取的最大体积
)

// ole2Magic 是旧版 Office 二进制文档（doc/xls/ppt）的复合文档魔数。
var ole2Magic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// builtinExtMIME 是常见扩展名对应的规范 MIME 类型，避免依赖容器镜像中可能缺失的系统 mime.types。
var builtinExtMIME = map[string]string{
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".webp":  "image/webp",
	".gif":   "image/gif",
	".bmp":   "image/bmp",
	".ico":   "image/x-icon",
	".pdf":   "application/pdf",
	".zip":   "application/zip",
	".docx":  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx":  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx":  "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".doc":   "application/msword",
	".xls":   "application/vnd.ms-excel",
	".ppt":   "application/vnd.ms-powerpoint",
	".txt":   "text/plain",
	".log":   "text/plain",
	".csv":   "text/csv",
	".md":    "text/markdown",
	".json":  "application/json",
	".html":  "text/html",
	".htm":   "text/html",
	".xhtml": "application/xhtml+xml",
	".svg":   "image/svg+xml",
	".xml":   "text/xml",
	".js":    "text/javascript",
	".mjs":   "text/javascript",
	".css":   "text/css",
}

// ooxmlSpecs 描述 OOXML 办公文档的扩展名、内部目录前缀和规范 MIME 类型。
var ooxmlSpecs = map[string]struct {
	prefix string
	mime   string
}{
	".docx": {"word/", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	".xlsx": {"xl/", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	".pptx": {"ppt/", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
}

// ole2MIME 是旧版 Office 扩展名对应的规范 MIME 类型。
var ole2MIME = map[string]string{
	".doc": "application/msword",
	".xls": "application/vnd.ms-excel",
	".ppt": "application/vnd.ms-powerpoint",
}

// activeMarkup 是浏览器打开时可能执行脚本的标记类型，不能伪装成纯文本文件通过校验。
var activeMarkup = map[string]bool{
	"text/html":             true,
	"application/xhtml+xml": true,
	"image/svg+xml":         true,
	"text/xml":              true,
	"application/xml":       true,
}

// extMIME 返回扩展名 ext 对应的规范 MIME 类型，优先查内置映射表，再回退到系统表。
func extMIME(ext string) string {
	ext = strings.ToLower(ext)
	if m, ok := builtinExtMIME[ext]; ok {
		return m
	}
	return mediaType(mime.TypeByExtension(ext))
}

// detectContentType 从已上传的对象内容识别真实 MIME 类型：在 http.DetectContentType 之外，
// 进一步识别 OOXML（docx/xlsx/pptx）、OLE2（doc/xls/ppt）以及安全的纯文本格式（txt/log/csv/md/json）。
func (s *Service) detectContentType(ctx context.Context, r File) (string, error) {
	body, err := s.disk(r).Open(ctx, r.Key)
	if err != nil {
		return "", errors.Wrap(err, "filesystem: open")
	}
	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(body, head)
	_ = body.Close()
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", errors.Wrap(err, "filesystem: read")
	}
	sample := head[:n]
	actual := mediaType(http.DetectContentType(sample))
	ext := strings.ToLower(path.Ext(r.Key))

	// OOXML：ZIP 容器中必须同时包含 [Content_Types].xml 和对应应用的目录，防止普通 ZIP 冒充办公文档。
	if actual == "application/zip" {
		if spec, ok := ooxmlSpecs[ext]; ok {
			matched, err := s.isOOXML(ctx, r, spec.prefix)
			if err != nil {
				return "", err
			}
			if matched {
				return spec.mime, nil
			}
		}
		return actual, nil
	}

	// 旧版 Office：校验 OLE2 复合文档魔数。
	if m, ok := ole2MIME[ext]; ok && bytes.HasPrefix(sample, ole2Magic) {
		return m, nil
	}

	// 纯文本与结构化文本：拒绝包含 NUL、非法 UTF-8 或 HTML/SVG/XML 等可执行标记的内容。
	switch ext {
	case ".txt", ".log", ".csv", ".md", ".json":
		if activeMarkup[actual] || looksLikeActiveMarkup(sample) || !isSafeText(sample, int64(n) < r.Size) {
			return actual, nil
		}
		if ext == ".json" {
			ok, err := s.isValidJSON(ctx, r)
			if err != nil {
				return "", err
			}
			if ok {
				return "application/json", nil
			}
			return "text/plain", nil
		}
		return extMIME(ext), nil
	}

	return actual, nil
}

// isOOXML 校验 ZIP 对象 r 是否包含 [Content_Types].xml 和 prefix 下的文件。
func (s *Service) isOOXML(ctx context.Context, r File, prefix string) (bool, error) {
	if r.Size <= 0 || r.Size > maxZipInspect {
		return false, nil
	}
	body, err := s.disk(r).Open(ctx, r.Key)
	if err != nil {
		return false, errors.Wrap(err, "filesystem: open")
	}
	data, err := io.ReadAll(io.LimitReader(body, maxZipInspect+1))
	_ = body.Close()
	if err != nil {
		return false, errors.Wrap(err, "filesystem: read")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false, nil
	}
	var hasTypes, hasPart bool
	for _, f := range zr.File {
		if f.Name == "[Content_Types].xml" {
			hasTypes = true
		} else if strings.HasPrefix(f.Name, prefix) {
			hasPart = true
		}
		if hasTypes && hasPart {
			return true, nil
		}
	}
	return false, nil
}

// isValidJSON 校验对象 r 的内容是否是合法的 JSON。
func (s *Service) isValidJSON(ctx context.Context, r File) (bool, error) {
	if r.Size <= 0 || r.Size > maxJSONInspect {
		return false, nil
	}
	body, err := s.disk(r).Open(ctx, r.Key)
	if err != nil {
		return false, errors.Wrap(err, "filesystem: open")
	}
	data, err := io.ReadAll(io.LimitReader(body, maxJSONInspect+1))
	_ = body.Close()
	if err != nil {
		return false, errors.Wrap(err, "filesystem: read")
	}
	return json.Valid(data), nil
}

// isSafeText 报告 sample 是否是不含 NUL 字节的合法 UTF-8 文本；truncated 为 true 时允许末尾多字节字符被截断。
func isSafeText(sample []byte, truncated bool) bool {
	if bytes.IndexByte(sample, 0) >= 0 {
		return false
	}
	if truncated {
		for i := 0; i < 3 && len(sample) > 0 && !utf8.Valid(sample); i++ {
			sample = sample[:len(sample)-1]
		}
	}
	return utf8.Valid(sample)
}

// looksLikeActiveMarkup 检查去空白和 UTF-8 BOM 后的前缀是否像 HTML、SVG、XML 或脚本标记。
func looksLikeActiveMarkup(sample []byte) bool {
	s := bytes.TrimPrefix(sample, []byte{0xEF, 0xBB, 0xBF})
	s = bytes.TrimLeft(s, " \t\r\n")
	lower := strings.ToLower(string(s[:min(len(s), 32)]))
	for _, p := range []string{"<!doctype html", "<html", "<script", "<svg", "<?xml"} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// declaredCompatible 报告客户端上传时声明的 MIME 类型 declared 是否与探测到的真实类型 actual 兼容：
// 相同、浏览器未识别出的兜底类型（空或 application/octet-stream），或常见的浏览器别名返回 true；
// 冲突的具体类型（例如把 PNG 声明为 image/jpeg 或 text/html）返回 false。
func declaredCompatible(declared, actual string) bool {
	if declared == "" || declared == "application/octet-stream" || declared == actual {
		return true
	}
	switch actual {
	case "image/jpeg":
		return declared == "image/jpg"
	case "text/csv":
		return declared == "text/plain" || declared == "application/vnd.ms-excel"
	case "text/markdown":
		return declared == "text/plain"
	case "application/json":
		return declared == "text/plain"
	}
	return false
}
