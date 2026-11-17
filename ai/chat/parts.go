package chat

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/go-tenon/tenon/ai"
)

// 本文件在 ai.Media 和 trpc-agent-go 的消息片段之间转换。useChat 的文件片段、对话历史和附件都经过这里。

// partOf 把媒体转换为模型消息的一部分，按 MIME 类型分为图片、音频、视频和其他文件。
// link 不为空时媒体以链接发送，m 只有 MIME 类型和名称。
func partOf(m ai.Media, link string) model.ContentPart {
	switch m.Modality() {
	case ai.ModalityImage:
		return model.ContentPart{Type: model.ContentTypeImage, Image: &model.Image{URL: link, Data: m.Data, Format: m.MIME}}
	case ai.ModalityAudio:
		format := m.MIME
		if link == "" {
			format = audioFormat(m.MIME) // Chat Completions 的 input_audio 只接受 wav、mp3 这样的简写
		}
		return model.ContentPart{Type: model.ContentTypeAudio, Audio: &model.Audio{URL: link, Data: m.Data, Format: format}}
	case ai.ModalityVideo:
		return model.ContentPart{Type: model.ContentTypeVideo, Video: &model.Video{URL: link, Data: m.Data, Format: m.MIME}}
	}
	return model.ContentPart{Type: model.ContentTypeFile, File: &model.File{Name: m.Name, URL: link, Data: m.Data, MimeType: m.MIME}}
}

// mediaOf 是 partOf 的反向：取出消息片段中的媒体和链接。文字和没有内容的片段返回 false。
func mediaOf(p model.ContentPart) (m ai.Media, link string, ok bool) {
	switch {
	case p.Image != nil:
		m, link = ai.Media{Data: p.Image.Data, MIME: fullType("image", p.Image.Format)}, p.Image.URL
	case p.Audio != nil:
		m, link = ai.Media{Data: p.Audio.Data, MIME: audioMIME(p.Audio.Format)}, p.Audio.URL
	case p.Video != nil:
		m, link = ai.Media{Data: p.Video.Data, MIME: fullType("video", p.Video.Format)}, p.Video.URL
	case p.File != nil:
		m, link = ai.Media{Data: p.File.Data, MIME: p.File.MimeType, Name: p.File.Name}, p.File.URL
	default:
		return ai.Media{}, "", false
	}
	return m, link, link != "" || len(m.Data) > 0
}

// audioFormat 把音频的 MIME 类型换成 wav、mp3 这样的简写，audioMIME 是它的反向。
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

// audioMIME 返回音频格式 format 的 MIME 类型。
func audioMIME(format string) string {
	switch format {
	case "mp3":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	}
	return fullType("audio", format)
}

// fullType 把 png 这样的简写补全为 image/png；已经是完整 MIME 类型的原样返回。
func fullType(kind, format string) string {
	if format == "" || strings.Contains(format, "/") {
		return format
	}
	return kind + "/" + format
}
