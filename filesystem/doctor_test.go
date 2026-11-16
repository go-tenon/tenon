package filesystem_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/filesystem/local"
)

func openLocal(t *testing.T, dir string) *local.Disk {
	t.Helper()
	disk, err := local.Open(filesystem.LocalConfig{Root: dir, PublicURL: "/files"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = disk.Close() })
	return disk
}

func TestDoctor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	disk := openLocal(t, dir)
	res := doctor.Run(context.Background(), filesystem.Doctor(disk)).Results[0]
	if res.Status != doctor.Passed {
		t.Fatalf("writable directory: %+v", res)
	}
	left, err := filepath.Glob(filepath.Join(dir, "tenon-doctor", "*"))
	if err != nil || len(left) != 0 {
		t.Errorf("probe objects left behind: %v %v", left, err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	readOnly := t.TempDir()
	disk = openLocal(t, readOnly)
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o755) })
	res = doctor.Run(context.Background(), filesystem.Doctor(disk)).Results[0]
	if res.Status != doctor.Failed || !strings.Contains(res.Summary, "cannot write") || !strings.Contains(res.Fix, "writable") {
		t.Errorf("read-only directory: %+v", res)
	}
}
