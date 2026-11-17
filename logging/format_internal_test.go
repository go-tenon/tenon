package logging

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestTextFormat(t *testing.T) {
	cases := []struct {
		format   string
		terminal bool
		want     bool
	}{
		{"", true, true},           // 没有指定：开发时输出到终端，用 text
		{"", false, false},         // 没有指定：文件、管道、systemd、容器，用 json
		{FormatJSON, true, false},  // 显式指定的优先
		{FormatText, false, true},  //
		{FormatJSON, false, false}, //
	}
	for _, c := range cases {
		if got := textFormat(c.format, c.terminal); got != c.want {
			t.Errorf("textFormat(%q, %v) = %v, want %v", c.format, c.terminal, got, c.want)
		}
	}
}

// 默认配置写到不是终端的文件时输出 JSON，与生产环境（systemd、容器采集标准错误）相同。
func TestDefaultFormatIsJSONWhenNotATerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg := DefaultConfig()
	cfg.Writer = f
	New(cfg).Info("hello")
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil || rec["msg"] != "hello" {
		t.Fatalf("output=%q err=%v", data, err)
	}
}
