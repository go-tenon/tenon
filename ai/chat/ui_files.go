package chat

import (
	"strings"

	"github.com/cockroachdb/errors"
	"trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/go-tenon/tenon/ai"
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

// contentPart 把 useChat 的文件片段转换为模型消息的一部分。
func contentPart(p UIPart) (model.ContentPart, error) {
	m := ai.Media{MIME: p.MediaType, Name: p.Filename}
	link := ""
	switch {
	case strings.HasPrefix(p.URL, "data:"):
		d, err := ai.ParseDataURL(p.URL)
		if err != nil {
			return model.ContentPart{}, ErrBadRequest.Wrap(err)
		}
		m.Data = d.Data
		if m.MIME == "" {
			m.MIME = d.MIME
		}
	case strings.HasPrefix(p.URL, "https://") || strings.HasPrefix(p.URL, "http://"):
		link = p.URL
	default:
		return model.ContentPart{}, ErrBadRequest.Wrap(errors.New("chat: file url must be a data URL or an http(s) link"))
	}
	if m.MIME == "" {
		return model.ContentPart{}, ErrBadRequest.Wrap(errors.New("chat: file needs a mediaType"))
	}
	return partOf(m, link), nil
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

// uiFilePart 把消息中的媒体转换为 useChat 的文件片段，保存在对话中的内容还原为 data URL。
func uiFilePart(c model.ContentPart) (UIPart, bool) {
	m, link, ok := mediaOf(c)
	if !ok {
		return UIPart{}, false
	}
	if link == "" {
		link = m.DataURL()
	}
	return UIPart{Type: "file", MediaType: m.MIME, Filename: m.Name, URL: link}, true
}
