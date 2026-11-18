package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestColorfulOnlyForTerminals(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if colorful(&bytes.Buffer{}) || colorful(f) {
		t.Fatal("non-terminal writers must not be colored")
	}
	t.Setenv("NO_COLOR", "1")
	if colorful(os.Stderr) {
		t.Fatal("NO_COLOR must disable colors")
	}
}

// FORCE_COLOR 让管道这类非终端的输出也按终端处理：text 格式并着色，NO_COLOR 仍然关闭颜色。
func TestForceColor(t *testing.T) {
	var buf bytes.Buffer
	t.Setenv("FORCE_COLOR", "0")
	if terminal(&buf) {
		t.Fatal("FORCE_COLOR=0 must not force a terminal")
	}
	t.Setenv("FORCE_COLOR", "1")
	if !terminal(&buf) || !colorful(&buf) || !textFormat("", terminal(&buf)) {
		t.Fatal("FORCE_COLOR=1 must treat a pipe as a colored terminal")
	}
	New(Config{Writer: &buf}).Info("hello")
	if !bytes.Contains(buf.Bytes(), []byte("\x1b[")) || bytes.HasPrefix(buf.Bytes(), []byte("{")) {
		t.Fatalf("want colored text, got %q", buf.String())
	}
	t.Setenv("NO_COLOR", "1")
	if colorful(&buf) {
		t.Fatal("NO_COLOR must still disable colors")
	}
}
