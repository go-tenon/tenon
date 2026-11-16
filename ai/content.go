package ai

import (
	"strings"

	"github.com/cockroachdb/errors"
)

// ErrUnsupportedInput 表示模型不接受这种输入，例如只支持文字的向量模型收到了图片。用 errors.Is 判断。
var ErrUnsupportedInput = errors.New("ai: unsupported input")

// Modality 是输入的种类。
type Modality string

// 输入的种类。
const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityAudio Modality = "audio"
	ModalityVideo Modality = "video"
)

// Modality 按 MIME 类型返回媒体的种类：图片、音频或视频，其他类型为空。
func (m Media) Modality() Modality {
	kind, _, _ := strings.Cut(m.MIME, "/")
	switch kind {
	case "image":
		return ModalityImage
	case "audio":
		return ModalityAudio
	case "video":
		return ModalityVideo
	}
	return ""
}

// Part 是 Content 的一段：Text 和 Media 二选一。
type Part struct {
	Text  string
	Media *Media
}

// TextPart 返回一段文字。
func TextPart(s string) Part { return Part{Text: s} }

// MediaPart 返回一段媒体，例如一张图片。
func MediaPart(m Media) Part { return Part{Media: &m} }

// Content 是计算向量或重排的一项输入：一段文字、一张图片，或者文字和图片放在一起。
// 一项得到一个向量（或一个分数）：文字和图片放在一起时，模型把它们融合为一个向量，例如商品的标题和主图。
// 模型能接受哪些组合由各个实现说明，不接受时返回 ErrUnsupportedInput。
type Content []Part

// Text 返回只有一段文字的 Content。
func Text(s string) Content { return Content{{Text: s}} }

// Text 返回其中的文字，各段之间用换行连接。
func (c Content) Text() string {
	var texts []string
	for _, p := range c {
		if p.Media == nil && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// Media 返回其中的媒体。
func (c Content) Media() []Media {
	var out []Media
	for _, p := range c {
		if p.Media != nil {
			out = append(out, *p.Media)
		}
	}
	return out
}

// Modalities 返回其中有的种类，按文字、图片、音频、视频的顺序，每种一次。不认识的媒体类型不计入。
func (c Content) Modalities() []Modality {
	has := map[Modality]bool{}
	for _, p := range c {
		switch {
		case p.Media != nil:
			if k := p.Media.Modality(); k != "" {
				has[k] = true
			}
		case p.Text != "":
			has[ModalityText] = true
		}
	}
	var out []Modality
	for _, k := range []Modality{ModalityText, ModalityImage, ModalityAudio, ModalityVideo} {
		if has[k] {
			out = append(out, k)
		}
	}
	return out
}

// Validate 检查 Content 不为空，每一段只有文字或只有媒体，媒体有内容和 MIME 类型。
func (c Content) Validate() error {
	if len(c) == 0 {
		return errors.New("ai: empty content")
	}
	for i, p := range c {
		switch {
		case p.Media != nil && p.Text != "":
			return errors.Newf("ai: content part %d has both text and media", i)
		case p.Media != nil && (len(p.Media.Data) == 0 || p.Media.MIME == ""):
			return errors.Newf("ai: content part %d needs media data and a MIME type", i)
		case p.Media == nil && p.Text == "":
			return errors.Newf("ai: content part %d is empty", i)
		}
	}
	return nil
}

// Texts 把只有文字的输入转换为字符串，供只支持文字的模型使用。有媒体时返回 ErrUnsupportedInput。
func Texts(inputs []Content) ([]string, error) {
	out := make([]string, len(inputs))
	for i, c := range inputs {
		if err := c.Validate(); err != nil {
			return nil, errors.Wrapf(err, "input %d", i)
		}
		if len(c.Media()) > 0 {
			return nil, errors.Wrapf(ErrUnsupportedInput, "input %d has media, the model only accepts text", i)
		}
		out[i] = c.Text()
	}
	return out, nil
}
