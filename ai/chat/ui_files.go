package chat

import (
	"encoding/base64"
	"mime"
	"net/url"
	"strings"

	"github.com/cockroachdb/errors"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// userMessage 把 useChat 的用户消息转换为模型消息：文本片段拼成 Content，文件片段（图片、音频、视频、PDF 等）
// 依次成为 ContentParts。文件片段的 url 是 data URL（useChat 发送 FileList 时的默认做法）或 http(s) 链接；
// 链接原样交给模型服务，由它下载，应用的服务端不访问它。
func (m UIMessage) userMessage() (model.Message, error) {
	msg := model.Message{Role: model.RoleUser, Content: m.text()}
	for _, p := range m.Parts {
		if p.Type != "file" {
			continue
		}
		part, err := contentPart(p)
		if err != nil {
			return model.Message{}, err
		}
		msg.ContentParts = append(msg.ContentParts, part)
	}
	if msg.Content == "" && len(msg.ContentParts) == 0 {
		return model.Message{}, ErrBadRequest.New()
	}
	return msg, nil
}

// contentPart 把 useChat 的文件片段转换为模型消息的一部分，按 MIME 类型分为图片、音频、视频和其他文件。
func contentPart(p UIPart) (model.ContentPart, error) {
	mediaType := p.MediaType
	var data []byte
	link := ""
	switch {
	case strings.HasPrefix(p.URL, "data:"):
		t, d, err := parseDataURL(p.URL)
		if err != nil {
			return model.ContentPart{}, ErrBadRequest.Wrap(err)
		}
		if mediaType == "" {
			mediaType = t
		}
		data = d
	case strings.HasPrefix(p.URL, "https://") || strings.HasPrefix(p.URL, "http://"):
		link = p.URL
	default:
		return model.ContentPart{}, ErrBadRequest.Wrap(errors.New("chat: file url must be a data URL or an http(s) link"))
	}
	if mediaType == "" {
		return model.ContentPart{}, ErrBadRequest.Wrap(errors.New("chat: file needs a mediaType"))
	}
	kind, _, _ := strings.Cut(mediaType, "/")
	switch kind {
	case "image":
		return model.ContentPart{Type: model.ContentTypeImage, Image: &model.Image{URL: link, Data: data, Format: mediaType}}, nil
	case "audio":
		format := mediaType
		if link == "" {
			// Chat Completions 的 input_audio 只接受 wav、mp3 这样的简写。
			format = audioFormat(mediaType)
		}
		return model.ContentPart{Type: model.ContentTypeAudio, Audio: &model.Audio{URL: link, Data: data, Format: format}}, nil
	case "video":
		return model.ContentPart{Type: model.ContentTypeVideo, Video: &model.Video{URL: link, Data: data, Format: mediaType}}, nil
	}
	return model.ContentPart{Type: model.ContentTypeFile, File: &model.File{Name: p.Filename, URL: link, Data: data, MimeType: mediaType}}, nil
}

// parseDataURL 解析 data:[<mediatype>][;base64],<data>。
func parseDataURL(s string) (string, []byte, error) {
	header, payload, ok := strings.Cut(strings.TrimPrefix(s, "data:"), ",")
	if !ok {
		return "", nil, errors.New("chat: malformed data URL")
	}
	header, isBase64 := strings.CutSuffix(header, ";base64")
	mediaType := ""
	if header != "" {
		t, _, err := mime.ParseMediaType(header)
		if err != nil {
			return "", nil, errors.Wrap(err, "chat: malformed data URL")
		}
		mediaType = t
	}
	if isBase64 {
		data, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return "", nil, errors.Wrap(err, "chat: malformed data URL")
		}
		return mediaType, data, nil
	}
	data, err := url.PathUnescape(payload)
	if err != nil {
		return "", nil, errors.Wrap(err, "chat: malformed data URL")
	}
	return mediaType, []byte(data), nil
}

// audioFormat 把音频的 MIME 类型换成 wav、mp3 这样的简写。
func audioFormat(mediaType string) string {
	switch mediaType {
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave":
		return "wav"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	}
	_, sub, _ := strings.Cut(mediaType, "/")
	return sub
}

// uiParts 把用户消息转换为 useChat 的片段：文本在前，文件依次在后。文件内容保存在对话中的还原为 data URL。
func uiParts(m model.Message) []UIPart {
	parts := []UIPart{}
	if m.Content != "" || len(m.ContentParts) == 0 {
		parts = append(parts, UIPart{Type: "text", Text: m.Content})
	}
	for _, c := range m.ContentParts {
		if p, ok := uiFilePart(c); ok {
			parts = append(parts, p)
		}
	}
	return parts
}

func uiFilePart(c model.ContentPart) (UIPart, bool) {
	var link, mediaType, name string
	var data []byte
	switch {
	case c.Image != nil:
		link, data, mediaType = c.Image.URL, c.Image.Data, fullType("image", c.Image.Format)
	case c.Audio != nil:
		f := c.Audio.Format
		switch f {
		case "mp3":
			f = "audio/mpeg"
		case "wav":
			f = "audio/wav"
		}
		link, data, mediaType = c.Audio.URL, c.Audio.Data, fullType("audio", f)
	case c.Video != nil:
		link, data, mediaType = c.Video.URL, c.Video.Data, fullType("video", c.Video.Format)
	case c.File != nil:
		link, data, mediaType, name = c.File.URL, c.File.Data, c.File.MimeType, c.File.Name
	default:
		return UIPart{}, false
	}
	if link == "" {
		if len(data) == 0 {
			return UIPart{}, false
		}
		link = "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	return UIPart{Type: "file", MediaType: mediaType, Filename: name, URL: link}, true
}

// fullType 把 png 这样的简写补全为 image/png；已经是完整 MIME 类型的原样返回。
func fullType(kind, format string) string {
	if format == "" || strings.Contains(format, "/") {
		return format
	}
	return kind + "/" + format
}
