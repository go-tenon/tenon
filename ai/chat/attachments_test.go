package chat_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/go-tenon/tenon/ai/chat"
	"github.com/go-tenon/tenon/ai/chat/chattest"
)

type lookArgs struct {
	AttachmentID string `json:"attachment_id"`
}

type lookResult struct {
	MIME  string `json:"mime"`
	Size  int    `json:"size"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// lookTool 按 ID 取出对话中的附件，返回它的类型、大小和对话中附件的总数。
func lookTool() tool.CallableTool {
	return function.NewFunctionTool(func(ctx context.Context, a lookArgs) (lookResult, error) {
		att, err := chat.FindAttachment(ctx, a.AttachmentID)
		if err != nil {
			return lookResult{}, err
		}
		return lookResult{MIME: att.Media.MIME, Size: len(att.Media.Data), Name: att.Media.Name, Count: len(chat.Attachments(ctx))}, nil
	}, function.WithName("look"), function.WithDescription("查看附件"))
}

func idOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}

var (
	pngData = []byte("PNG-DATA")
	pdfData = []byte("%PDF-1.7")
)

func imageMessage(text string) model.Message {
	msg := model.NewUserMessage(text)
	msg.AddImageData(pngData, "", "image/png")
	msg.AddFileData("合同.pdf", pdfData, "application/pdf")
	return msg
}

// lastUser 返回请求中最后一条用户消息。
func lastUser(req *model.Request) model.Message {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == model.RoleUser {
			return req.Messages[i]
		}
	}
	return model.Message{}
}

// toolOutputs 返回请求中工具结果的内容。
func toolOutputs(req *model.Request) []string {
	var out []string
	for _, m := range req.Messages {
		if m.Role == model.RoleTool {
			out = append(out, m.Content)
		}
	}
	return out
}

// partsOf 把消息的各部分写成字符串：文字原样，其余写出种类。
func partsOf(m model.Message) []string {
	var out []string
	for _, p := range m.ContentParts {
		if p.Text != nil {
			out = append(out, *p.Text)
		} else {
			out = append(out, string(p.Type))
		}
	}
	return out
}

func TestAttachmentsInTools(t *testing.T) {
	t.Parallel()
	img, pdf := idOf(pngData), idOf(pdfData)
	m := chattest.NewModel(
		chattest.Calls(chattest.Call{Name: "look", Args: lookArgs{"[attachment " + img + "]"}}),
		chattest.Text("是一只猫"),
		// 下一轮：查看上一轮发送的 PDF，再用一个不存在的 ID。
		chattest.Calls(chattest.Call{Name: "look", Args: lookArgs{pdf}}, chattest.Call{Name: "look", Args: lookArgs{"nope"}}),
		chattest.Text("好的"),
	)
	rs := newRuns(t, m, []tool.Tool{lookTool()})
	ctx := context.Background()

	msg := imageMessage("这是什么")
	r, err := rs.Start(ctx, "u1", "c1", msg)
	if err != nil {
		t.Fatal(err)
	}
	<-r.Done()
	if len(msg.ContentParts) != 2 {
		t.Fatalf("caller's message was modified: %+v", msg.ContentParts)
	}
	// 模型看到每个附件前面的标签（trpc-agent-go 还会在开头列出文件名）。
	got := partsOf(lastUser(m.Requests()[0]))
	want := []string{"[attachment " + img + "]", "image", "[attachment " + pdf + "]", "file"}
	if !strings.HasSuffix(strings.Join(got, "|"), strings.Join(want, "|")) {
		t.Fatalf("model saw parts %q, want suffix %q", got, want)
	}
	if out := toolOutputs(m.Requests()[1]); len(out) != 1 || out[0] != `{"mime":"image/png","size":8,"name":"","count":2}` {
		t.Fatalf("tool output %q", out)
	}

	r, err = rs.Start(ctx, "u1", "c1", model.NewUserMessage("合同呢"))
	if err != nil {
		t.Fatal(err)
	}
	<-r.Done()
	out := toolOutputs(m.Requests()[3])
	if len(out) != 3 || out[1] != `{"mime":"application/pdf","size":8,"name":"合同.pdf","count":2}` || !strings.Contains(out[2], `no attachment "nope" in the conversation`) {
		t.Fatalf("tool outputs %q", out)
	}

	// 历史中看不到标签。
	history, err := rs.UIMessages(ctx, "u1", "c1")
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, p := range history[0].Parts {
		types = append(types, p.Type)
	}
	if strings.Join(types, ",") != "text,file,file" || history[0].Parts[0].Text != "这是什么" {
		t.Fatalf("history parts %+v", history[0].Parts)
	}
}

func TestAttachmentsWithHistory(t *testing.T) {
	t.Parallel()
	img := idOf(pngData)
	m := chattest.NewModel(
		chattest.Calls(chattest.Call{Name: "look", Args: lookArgs{img}}),
		chattest.Text("是一只猫"),
	)
	table := newMessageTable()
	rs := newHistoryRuns(t, m, []tool.Tool{lookTool()}, table)

	// 应用保存的消息没有标签，读出历史时加上。
	table.write(group, imageMessage("[张三] 这是什么"))
	r, err := rs.Notify(context.Background(), group.UserID, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	<-r.Done()
	if got := partsOf(lastUser(m.Requests()[0])); !strings.Contains(strings.Join(got, "|"), "[attachment "+img+"]|image") {
		t.Fatalf("model saw parts %q", got)
	}
	if out := toolOutputs(m.Requests()[1]); len(out) != 1 || !strings.Contains(out[0], `"mime":"image/png"`) {
		t.Fatalf("tool output %q", out)
	}
}

func TestLabelAttachments(t *testing.T) {
	t.Parallel()
	once := chat.LabelAttachments(imageMessage("看图"))
	twice := chat.LabelAttachments(once)
	if strings.Join(partsOf(once), "|") != strings.Join(partsOf(twice), "|") || len(once.ContentParts) != 4 {
		t.Fatalf("labels added twice: %q", partsOf(twice))
	}
	// 助手的消息和没有附件的消息不变。
	a := model.NewAssistantMessage("好的")
	if got := chat.LabelAttachments(a); got.Content != "好的" || len(got.ContentParts) != 0 {
		t.Fatalf("assistant message changed: %+v", got)
	}

	// 不在 agent 的运行中时没有附件。
	if got := chat.Attachments(context.Background()); got != nil {
		t.Fatalf("attachments outside a run: %+v", got)
	}
	_, err := chat.FindAttachment(context.Background(), "x")
	if !errors.Is(err, chat.ErrAttachmentNotFound) {
		t.Fatalf("err = %v", err)
	}
}
