package dev_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/cmd/tenon/internal/dev"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// TestWriteMessages 检查前端的界面文字目录：只读取这个前端的翻译文件，复数消息保留各个形式，缺少的消息给出提示，
// 没有翻译文件的前端不生成，翻译文件写错时返回错误。
func TestWriteMessages(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("locales/zh-CN.yaml", "article.not_found: 文章不存在\n")
	write("locales/web.zh-CN.yaml", "inbox.empty: 没有会话\ninbox.waiting: \"{{.count}} 个会话等待回复\"\n")
	write("locales/web.en.yaml", "inbox.empty: No conversations\ninbox.waiting:\n  one: \"{{.count}} conversation waiting\"\n  other: \"{{.count}} conversations waiting\"\n")
	write("apps/web/src/.keep", "")
	write("apps/admin/src/.keep", "")
	cfg := &project.Config{Frontends: []project.Frontend{{Name: "web", Dir: "apps/web"}, {Name: "admin", Dir: "apps/admin"}}}

	warnings, err := dev.WriteMessages(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("提示：%v", warnings)
	}
	got, err := os.ReadFile(filepath.Join(root, "apps/web/src", dev.MessagesFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"zh-CN": {`,
		`"inbox.empty": "没有会话",`,
		`"inbox.waiting": { one: "{{.count}} conversation waiting", other: "{{.count}} conversations waiting" },`,
		"export type MessageId =\n  | \"inbox.empty\"\n  | \"inbox.waiting\"\n",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("messages.gen.ts 中没有 %q：\n%s", want, got)
		}
	}
	if strings.Contains(string(got), "article.not_found") {
		t.Error("前端的目录中不应有后端的消息")
	}
	if _, err := os.Stat(filepath.Join(root, "apps/admin/src", dev.MessagesFile)); !os.IsNotExist(err) {
		t.Errorf("没有翻译文件的前端：%v", err)
	}

	// 英文缺少一条消息时给出提示
	write("locales/web.zh-CN.yaml", "inbox.empty: 没有会话\ninbox.waiting: 等待\ninbox.only: 只有中文\n")
	warnings, err = dev.WriteMessages(root, cfg)
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "web.en.yaml is missing 1 messages: inbox.only") {
		t.Errorf("缺少消息的提示：%v %v", warnings, err)
	}

	// 翻译文件写错
	write("locales/web.en.yaml", "inbox.empty: [\n")
	if _, err := dev.WriteMessages(root, cfg); err == nil || !strings.Contains(err.Error(), "locales/web.en.yaml") {
		t.Errorf("写错的翻译文件：%v", err)
	}
}
