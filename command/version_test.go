package command

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildInfoAndVersionCommand(t *testing.T) {
	origV, origC, origT := buildVersion, buildCommit, buildTime
	t.Cleanup(func() {
		buildVersion, buildCommit, buildTime = origV, origC, origT
	})
	buildVersion = "v1.2.3"
	buildCommit = "830eab5"
	buildTime = "2026-11-26T03:00:00Z"

	meta := BuildInfo()
	if meta.Version != "v1.2.3" || meta.Commit != "830eab5" || meta.Time != "2026-11-26T03:00:00Z" || meta.Dirty {
		t.Fatalf("BuildInfo = %+v", meta)
	}
	if got := meta.String(); got != "v1.2.3 (830eab5) 2026-11-26T03:00:00Z" {
		t.Fatalf("String() = %q", got)
	}

	root := Root("blog", "test")
	root.AddCommand(Version())

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"version", "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("version json: %v", err)
	}
	var decoded BuildMetadata
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decode json: %v\n%s", err, out.String())
	}
	if decoded.Version != "v1.2.3" || decoded.Commit != "830eab5" {
		t.Fatalf("decoded = %+v", decoded)
	}

	out.Reset()
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if !strings.Contains(out.String(), "v1.2.3") {
		t.Fatalf("--version output = %q", out.String())
	}
}
