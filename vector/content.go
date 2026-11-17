package vector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"

	"github.com/go-tenon/tenon/ai"
	"github.com/go-tenon/tenon/filesystem"
)

// MaxFileSize 是 FilePart 引用的文件的最大字节数，更大的文件不计算向量，文档同步失败。
// 与百炼的视频上限（约 50 MB）相当；模型自己的上限可能更小，以模型为准。
const MaxFileSize = 50 << 20

// Part 是 Content 的一段：文字、文件或媒体，三者选一，用 TextPart、FilePart、MediaPart 创建。
type Part struct {
	Text  string    // 一段文字。
	File  uuid.UUID // 文件存储中的文件 ID，计算向量时通过 WithFiles 传入的 Files 读取内容。
	Media *ai.Media // 直接给出的内容，例如用户上传的查询图片。
}

// TextPart 返回一段文字。
func TextPart(s string) Part { return Part{Text: s} }

// FilePart 返回文件存储中的一个文件，例如商品的主图。文件只在需要计算向量时读取：
// 同步时文件 ID 没有变化的文档沿用原来的向量，不读取文件，也不调用模型。
//
// 文件存储中替换文件会产生新的 ID，所以同一个 ID 的内容不会变化，向量也就不用重新计算。
func FilePart(id uuid.UUID) Part { return Part{File: id} }

// MediaPart 返回直接给出内容的媒体，例如用户上传的查询图片。它适合查询；用在文档中时，内容不保存在索引中，
// 每次同步都要读出内容才能判断有没有变化，文件存储中的文件用 FilePart。
func MediaPart(m ai.Media) Part { return Part{Media: &m} }

// Content 是计算向量的内容：一段文字、一张图片，或者文字和图片放在一起，得到一个向量。
// 文档类型的 embed 字段可以是 Content，Query.Input 也是 Content。
type Content []Part

// Text 返回只有一段文字的 Content。
func Text(s string) Content { return Content{TextPart(s)} }

// MarshalJSON 把一段编码为 {"text": …}、{"file": …} 或 {"media": MIME 类型}，媒体的内容不编码。
func (p Part) MarshalJSON() ([]byte, error) {
	switch {
	case p.Media != nil:
		return json.Marshal(map[string]string{"media": p.Media.MIME})
	case p.File != uuid.Nil:
		return json.Marshal(map[string]string{"file": p.File.String()})
	}
	return json.Marshal(map[string]string{"text": p.Text})
}

// UnmarshalJSON 解码 MarshalJSON 的结果。媒体只有 MIME 类型，没有内容。
func (p *Part) UnmarshalJSON(b []byte) error {
	var v struct {
		Text  string    `json:"text"`
		File  uuid.UUID `json:"file"`
		Media string    `json:"media"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*p = Part{Text: v.Text, File: v.File}
	if v.Media != "" {
		p.Media = &ai.Media{MIME: v.Media}
	}
	return nil
}

// validate 检查 Content 不为空，每一段只有一种内容。
func (c Content) validate() error {
	if len(c) == 0 {
		return errors.New("vector: empty content")
	}
	for i, p := range c {
		n := 0
		if p.Text != "" {
			n++
		}
		if p.File != uuid.Nil {
			n++
		}
		if p.Media != nil {
			n++
			if len(p.Media.Data) == 0 || p.Media.MIME == "" {
				return errors.Newf("vector: content part %d needs media data and a MIME type", i)
			}
		}
		if n != 1 {
			return errors.Newf("vector: content part %d must have exactly one of text, file and media", i)
		}
	}
	return nil
}

// text 返回其中的文字，各段之间用换行连接，用于混合索引的关键词。
func (c Content) text() string {
	var texts []string
	for _, p := range c {
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// fingerprint 是内容的 SHA-256，内容不变时沿用原来的向量。只有一段文字时与这段文字的哈希相同；
// 文件按 ID 计算，媒体按内容计算。
func (c Content) fingerprint() string {
	h := sha256.New()
	if len(c) == 1 && c[0].Text != "" {
		h.Write([]byte(c[0].Text))
	} else {
		for _, p := range c {
			switch {
			case p.Media != nil:
				sum := sha256.Sum256(p.Media.Data)
				fmt.Fprintf(h, "media:%s:%x\x00", p.Media.MIME, sum)
			case p.File != uuid.Nil:
				fmt.Fprintf(h, "file:%s\x00", p.File)
			default:
				fmt.Fprintf(h, "text:%d:%s\x00", len(p.Text), p.Text)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Files 读取 FilePart 引用的文件，*filesystem.Service 实现它。
type Files interface {
	Open(ctx context.Context, id uuid.UUID) (io.ReadCloser, filesystem.File, error)
}

// resolve 把 Content 转换为交给模型的 ai.Content，读取引用的文件。文件不存在、太大时返回的错误标记了 errInvalid。
func (c Content) resolve(ctx context.Context, files Files) (ai.Content, error) {
	out := make(ai.Content, len(c))
	for i, p := range c {
		switch {
		case p.Media != nil:
			out[i] = ai.MediaPart(*p.Media)
		case p.File != uuid.Nil:
			m, err := readFile(ctx, files, p.File)
			if err != nil {
				return nil, err
			}
			out[i] = ai.MediaPart(m)
		default:
			out[i] = ai.TextPart(p.Text)
		}
	}
	return out, nil
}

func readFile(ctx context.Context, files Files, id uuid.UUID) (ai.Media, error) {
	if files == nil {
		return ai.Media{}, errors.Newf("vector: content refers to file %s, but the store has no files (vector.WithFiles)", id)
	}
	body, f, err := files.Open(ctx, id)
	if errors.Is(err, filesystem.ErrNotFound) {
		return ai.Media{}, errors.Mark(errors.Newf("vector: file %s not found", id), errInvalid)
	}
	if err != nil {
		return ai.Media{}, errors.Wrapf(err, "vector: open file %s", id)
	}
	defer func() { _ = body.Close() }()
	if f.Size > MaxFileSize {
		return ai.Media{}, errors.Mark(errors.Newf("vector: file %s has %d bytes, more than %d", id, f.Size, MaxFileSize), errInvalid)
	}
	data, err := io.ReadAll(io.LimitReader(body, MaxFileSize+1))
	if err != nil {
		return ai.Media{}, errors.Wrapf(err, "vector: read file %s", id)
	}
	if len(data) > MaxFileSize {
		return ai.Media{}, errors.Mark(errors.Newf("vector: file %s has more than %d bytes", id, MaxFileSize), errInvalid)
	}
	return ai.Media{Data: data, MIME: f.ContentType, Name: f.Filename}, nil
}
