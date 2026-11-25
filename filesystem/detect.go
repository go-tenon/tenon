package filesystem

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
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
	".mp4":   "video/mp4",
	".m4v":   "video/mp4",
	".mov":   "video/quicktime",
	".webm":  "video/webm",
	".mp3":   "audio/mpeg",
	".m4a":   "audio/mp4",
	".ogg":   "audio/ogg",
	".oga":   "audio/ogg",
	".opus":  "audio/ogg",
	".wav":   "audio/wav",
	".aac":   "audio/aac",
	".flac":  "audio/flac",
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

// ContentTypeByExt 返回常见扩展名对应的内置规范 MIME 类型，不在内置表中时返回空字符串。
func ContentTypeByExt(ext string) string {
	return builtinExtMIME[strings.ToLower(ext)]
}

// extMIME 返回扩展名 ext 对应的规范 MIME 类型，优先查内置映射表，再回退到系统表。
func extMIME(ext string) string {
	ext = strings.ToLower(ext)
	if m, ok := builtinExtMIME[ext]; ok {
		return m
	}
	return mediaType(mime.TypeByExtension(ext))
}

// extCompatible 报告扩展名 ext 是否与探测出的真实类型 actual 兼容：
// 支持 .webm、.ogg、.mp4 等多语义容器同时对应音频或视频，其余扩展名按规范 MIME 精确匹配。
func extCompatible(ext, actual string) bool {
	ext = strings.ToLower(ext)
	switch ext {
	case ".webm":
		return actual == "video/webm" || actual == "audio/webm"
	case ".ogg":
		return actual == "audio/ogg" || actual == "video/ogg" || actual == "application/ogg"
	case ".mp4":
		return actual == "video/mp4" || actual == "audio/mp4"
	}
	return extMIME(ext) == actual
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

// detectContentType 从已上传的对象内容识别真实 MIME 类型：在 http.DetectContentType 之外，
// 进一步识别主流音视频容器（MP4/QuickTime/WebM/Ogg/WAV/MP3/AAC/FLAC）、OOXML（docx/xlsx/pptx）、
// OLE2（doc/xls/ppt）以及安全的纯文本格式（txt/log/csv/md/json）。
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
	declared := mediaType(r.ContentType)

	// 音视频容器魔数优先于通用兜底探测，准确区分 MP4/MOV/M4A、WebM 音视频、Ogg 音频与 WAV/MP3/AAC/FLAC。
	if media := detectMediaType(sample, ext, declared); media != "" {
		return media, nil
	}

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

// detectMediaType 按常见音视频容器的文件头魔数识别规范 MIME 类型；不是已知音视频容器时返回空字符串。
func detectMediaType(sample []byte, ext, declared string) string {
	// ISO BMFF（MP4 / QuickTime MOV / M4A）：偏移 4 为 "ftyp"，后跟 major brand 与 compatible brands。
	if len(sample) >= 12 && bytes.Equal(sample[4:8], []byte("ftyp")) {
		boxSize := int(binary.BigEndian.Uint32(sample[0:4]))
		if boxSize >= 12 {
			end := min(boxSize, len(sample))
			major := string(sample[8:12])
			hasBrand := func(target string) bool {
				if major == target {
					return true
				}
				for off := 16; off+4 <= end; off += 4 {
					if string(sample[off:off+4]) == target {
						return true
					}
				}
				return false
			}
			switch major {
			case "qt  ":
				return "video/quicktime"
			case "M4A ", "M4B ", "F4A ":
				return "audio/mp4"
			case "M4V ", "mp41", "mp42", "isom", "iso2", "iso5", "iso6", "avc1", "mp71", "dash", "MSNV", "3gp4", "3gp5", "3gp6", "f4v ":
				if ext == ".mov" && hasBrand("qt  ") {
					return "video/quicktime"
				}
				if ext == ".m4a" || declared == "audio/mp4" || declared == "audio/x-m4a" || declared == "audio/m4a" {
					return "audio/mp4"
				}
				return "video/mp4"
			}
		}
	}

	// EBML / WebM：按轨道编解码器与声明区分 video/webm 与 audio/webm。
	if len(sample) >= 4 && sample[0] == 0x1A && sample[1] == 0x45 && sample[2] == 0xDF && sample[3] == 0xA3 && bytes.Contains(sample, []byte("webm")) {
		hasVideo := bytes.Contains(sample, []byte("V_VP8")) ||
			bytes.Contains(sample, []byte("V_VP9")) ||
			bytes.Contains(sample, []byte("V_AV1")) ||
			bytes.Contains(sample, []byte("V_MPEG4"))
		hasAudio := bytes.Contains(sample, []byte("A_OPUS")) || bytes.Contains(sample, []byte("A_VORBIS"))
		if !hasVideo && (hasAudio || declared == "audio/webm") {
			return "audio/webm"
		}
		return "video/webm"
	}

	// Ogg 容器：按首包编解码器签名区分 audio/ogg（Opus/Vorbis/Speex/FLAC）、video/ogg（Theora）与通用 application/ogg。
	if len(sample) >= 28 && bytes.Equal(sample[0:4], []byte("OggS")) {
		if bytes.Contains(sample, []byte("\x80theora")) {
			return "video/ogg"
		}
		if bytes.Contains(sample, []byte("OpusHead")) ||
			bytes.Contains(sample, []byte("\x01vorbis")) ||
			bytes.Contains(sample, []byte("Speex   ")) ||
			bytes.Contains(sample, []byte("\x7fFLAC")) {
			return "audio/ogg"
		}
		return "application/ogg"
	}

	// RIFF WAVE 音频，统一返回规范类型 audio/wav。
	if len(sample) >= 12 && bytes.Equal(sample[0:4], []byte("RIFF")) && bytes.Equal(sample[8:12], []byte("WAVE")) {
		return "audio/wav"
	}

	// FLAC 无损音频。
	if len(sample) >= 4 && bytes.Equal(sample[0:4], []byte("fLaC")) {
		return "audio/flac"
	}

	// MP3（ID3v2 标签头或裸 MPEG 音频帧同步头）与 ADTS AAC。
	if len(sample) >= 3 && bytes.Equal(sample[0:3], []byte("ID3")) {
		return "audio/mpeg"
	}
	if len(sample) >= 4 && sample[0] == 0xFF && (sample[1]&0xE0) == 0xE0 {
		ver := (sample[1] >> 3) & 0x03
		layer := (sample[1] >> 1) & 0x03
		if layer == 0 && (ver == 2 || ver == 3) {
			// ADTS AAC 帧头：采样率索引 0..12 为合法取值。
			if freqIdx := (sample[2] >> 2) & 0x0F; freqIdx <= 12 {
				return "audio/aac"
			}
		} else if ver != 1 && layer != 0 {
			// 裸 MPEG Layer I/II/III 音频帧：排除保留的比特率（0xF）和采样率（0x3）。
			bitrate := (sample[2] >> 4) & 0x0F
			sampleRate := (sample[2] >> 2) & 0x03
			if bitrate != 0 && bitrate != 0x0F && sampleRate != 0x03 {
				return "audio/mpeg"
			}
		}
	}

	return ""
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
	case "audio/wav":
		return declared == "audio/wave" || declared == "audio/x-wav" || declared == "audio/vnd.wave"
	case "audio/mpeg":
		return declared == "audio/mp3" || declared == "audio/x-mp3"
	case "audio/mp4":
		return declared == "audio/x-m4a" || declared == "audio/m4a"
	case "audio/ogg":
		return declared == "application/ogg" || declared == "audio/opus"
	case "audio/aac":
		return declared == "audio/x-aac"
	case "audio/flac":
		return declared == "audio/x-flac"
	case "audio/webm":
		return declared == "video/webm"
	}
	return false
}

// readImageDimensions 读取图片文件的像素宽高：支持 PNG、JPEG、GIF（标准库）以及 WebP（VP8/VP8L/VP8X）。
// 无法解码或格式不支持时返回 ok=false 且 err=nil。
func (s *Service) readImageDimensions(ctx context.Context, r File, contentType string) (w, h int, ok bool, err error) {
	body, err := s.disk(r).Open(ctx, r.Key)
	if err != nil {
		return 0, 0, false, errors.Wrap(err, "filesystem: open")
	}
	defer func() { _ = body.Close() }()
	if contentType == "image/webp" {
		head := make([]byte, 64)
		n, rerr := io.ReadFull(body, head)
		if rerr != nil && !errors.Is(rerr, io.ErrUnexpectedEOF) && !errors.Is(rerr, io.EOF) {
			return 0, 0, false, errors.Wrap(rerr, "filesystem: read webp header")
		}
		w, h, ok = parseWebPDimensions(head[:n])
		return w, h, ok, nil
	}
	cfg, _, err := image.DecodeConfig(body)
	if err != nil {
		return 0, 0, false, nil
	}
	return cfg.Width, cfg.Height, true, nil
}

// parseWebPDimensions 解析 WebP 文件头中的宽高（VP8、VP8L、VP8X 三种编码块）。
func parseWebPDimensions(b []byte) (int, int, bool) {
	if len(b) < 30 || !bytes.Equal(b[0:4], []byte("RIFF")) || !bytes.Equal(b[8:12], []byte("WEBP")) {
		return 0, 0, false
	}
	switch string(b[12:16]) {
	case "VP8 ":
		// 有损帧头：3 字节帧标签 + 3 字节起始码 0x9d 0x01 0x2a + 2 字节宽 + 2 字节高
		if b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, false
		}
		w := int(binary.LittleEndian.Uint16(b[26:28]) & 0x3fff)
		h := int(binary.LittleEndian.Uint16(b[28:30]) & 0x3fff)
		return w, h, true
	case "VP8L":
		// 无损编码：1 字节签名 0x2f + 4 字节打包的 (width-1, height-1) 各占 14 位
		if len(b) < 25 || b[20] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(b[21:25])
		w := int(bits&0x3fff) + 1
		h := int((bits>>14)&0x3fff) + 1
		return w, h, true
	case "VP8X":
		// 扩展格式：4 字节标志 + 3 字节 (width-1) + 3 字节 (height-1)
		if len(b) < 30 {
			return 0, 0, false
		}
		w := int(uint32(b[24])|uint32(b[25])<<8|uint32(b[26])<<16) + 1
		h := int(uint32(b[27])|uint32(b[28])<<8|uint32(b[29])<<16) + 1
		return w, h, true
	}
	return 0, 0, false
}
