package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/cockroachdb/errors"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/go-tenon/tenon/ai"
	"github.com/go-tenon/tenon/errorx"
)

// ErrAttachmentNotFound 表示对话中没有这个 ID 的附件，通常是模型写错了 ID。
var ErrAttachmentNotFound = errorx.Define(errorx.NotFound, "ai.attachment_not_found")

// Attachment 是用户在对话中发送的一个附件：图片、音频、视频或文件。
type Attachment struct {
	ID    string   // 标识附件，由内容计算，同一个附件总是同一个 ID。模型在消息中看到 [attachment <ID>]，调用工具时用它指明附件。
	Media ai.Media // 附件的内容和 MIME 类型；以链接发送的附件没有内容，见 URL。
	URL   string   // 以链接发送的附件的地址。
}

// attachmentLabel 是写在附件前面、让模型知道附件 ID 的文字。
const attachmentLabel = "[attachment "

// labelOf 返回附件 id 在消息中的标签。
func labelOf(id string) string { return attachmentLabel + id + "]" }

// attachmentOf 把消息的一部分转换为附件，ID 由内容（以链接发送的是链接）计算；文字和没有内容的部分返回 false。
func attachmentOf(p model.ContentPart) (Attachment, bool) {
	m, link, ok := mediaOf(p)
	if !ok {
		return Attachment{}, false
	}
	key := m.Data
	if link != "" {
		key = []byte(link)
	}
	sum := sha256.Sum256(key)
	return Attachment{ID: hex.EncodeToString(sum[:])[:12], Media: m, URL: link}, true
}

// labelAttachments 在用户消息的每个附件前面加上 [attachment <ID>]，模型调用工具时用 ID 指明附件。
// 已经有标签的附件不再添加，所以可以对同一条消息重复调用。不修改 m 原来的 ContentParts。
func labelAttachments(m model.Message) model.Message {
	if m.Role != model.RoleUser || len(m.ContentParts) == 0 {
		return m
	}
	parts := make([]model.ContentPart, 0, len(m.ContentParts)*2)
	for _, p := range m.ContentParts {
		if a, ok := attachmentOf(p); ok {
			label := labelOf(a.ID)
			if n := len(parts); n == 0 || parts[n-1].Text == nil || *parts[n-1].Text != label {
				parts = append(parts, model.ContentPart{Type: model.ContentTypeText, Text: &label})
			}
		}
		parts = append(parts, p)
	}
	m.ContentParts = parts
	return m
}

// Attachments 返回对话中用户发送过的附件，按发送的先后，相同的附件只出现一次。在工具中调用，ctx 是工具收到的 ctx：
//
//	function.NewFunctionTool(func(ctx context.Context, in searchByImage) ([]Product, error) {
//	    a, err := chat.FindAttachment(ctx, in.AttachmentID)
//	    ...
//	})
//
// 不在 agent 的运行中时返回空。
func Attachments(ctx context.Context) []Attachment {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil
	}
	var msgs []model.Message
	if inv.Session != nil {
		for _, ev := range inv.Session.GetEvents() {
			if ev.Response == nil {
				continue
			}
			for _, c := range ev.Choices {
				msgs = append(msgs, c.Message)
			}
		}
	}
	msgs = append(msgs, inv.RunOptions.Messages...) // 使用 UseHistory 时的历史
	msgs = append(msgs, inv.Message)
	var out []Attachment
	seen := map[string]bool{}
	for _, m := range msgs {
		if m.Role != model.RoleUser {
			continue
		}
		for _, p := range m.ContentParts {
			if a, ok := attachmentOf(p); ok && !seen[a.ID] {
				seen[a.ID] = true
				out = append(out, a)
			}
		}
	}
	return out
}

// FindAttachment 返回对话中 ID 为 id 的附件，没有时返回 ErrAttachmentNotFound。id 可以带上模型抄写时留下的
// [attachment …] 外壳。
func FindAttachment(ctx context.Context, id string) (Attachment, error) {
	id = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(id), attachmentLabel), "]")
	for _, a := range Attachments(ctx) {
		if a.ID == id {
			return a, nil
		}
	}
	return Attachment{}, ErrAttachmentNotFound.Wrap(errors.Newf("chat: no attachment %q in the conversation", id))
}
