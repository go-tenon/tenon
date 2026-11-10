package files_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryan053857/mygoravel/config"
	"github.com/ryan053857/mygoravel/files"
)

type appConfig struct {
	Files files.Config `yaml:"files"`
}

func loadFileConfig(t *testing.T, documents ...string) (appConfig, error) {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, len(documents))
	for i, document := range documents {
		paths[i] = filepath.Join(root, fmt.Sprintf("config-%d.yaml", i))
		if err := os.WriteFile(paths[i], []byte(document), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return config.Load[appConfig](paths...)
}

func TestConfigLoadDefaultsAndOverlays(t *testing.T) {
	cfg, err := loadFileConfig(t, "{}")
	if err != nil || cfg.Files != files.DefaultConfig() {
		t.Fatalf("defaults=%+v err=%v", cfg.Files, err)
	}
	cfg, err = loadFileConfig(t, `files:
  default_disk: attachments
  max_upload_bytes: 1073741824
  temp_ttl: 48h
`, `files:
  part_size: 16777216
  sign_ttl: 10m
`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Files.DefaultDisk != "attachments" || cfg.Files.MaxUploadBytes != 1<<30 || cfg.Files.TempTTL != 48*time.Hour || cfg.Files.PartSize != 16<<20 || cfg.Files.SignTTL != 10*time.Minute {
		t.Fatalf("merged configuration=%+v", cfg.Files)
	}
}

func TestConfigLoadRejectsExplicitInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		name, document, want string
	}{
		{"empty disk", "default_disk: ''", "default_disk"},
		{"zero file size", "max_upload_bytes: 0", "max_upload_bytes"},
		{"negative file size", "max_upload_bytes: -1", "max_upload_bytes"},
		{"zero part size", "part_size: 0", "part_size"},
		{"small part", "part_size: 5242879", "part_size"},
		{"large part", "part_size: 5368709121", "part_size"},
		{"too many parts", "part_size: 5242880\n  max_upload_bytes: 52428800001", "10000 parts"},
		{"zero lifetime", "temp_ttl: 0s", "temp_ttl"},
		{"zero signature", "sign_ttl: 0s", "sign_ttl"},
		{"subsecond signature", "sign_ttl: 999ms", "sign_ttl"},
		{"signature outlives session", "temp_ttl: 1m\n  sign_ttl: 2m", "temp_ttl"},
		{"signature over seven days", "temp_ttl: 200h\n  sign_ttl: 169h", "7d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadFileConfig(t, "files:\n  "+tc.document+"\n")
			if err == nil || !strings.Contains(err.Error(), "files:") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want files validation mentioning %q, got %v", tc.want, err)
			}
		})
	}
}

func TestConfigLoadAcceptsMultipartBoundary(t *testing.T) {
	cfg, err := loadFileConfig(t, `files:
  part_size: 5242880
  max_upload_bytes: 52428800000
  temp_ttl: 168h
  sign_ttl: 168h
`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Files.MaxUploadBytes/cfg.Files.PartSize != files.MaxParts || cfg.Files.SignTTL != 7*24*time.Hour {
		t.Fatalf("boundary configuration=%+v", cfg.Files)
	}
}

func TestConfigLoadRejectsUnknownField(t *testing.T) {
	_, err := loadFileConfig(t, "files:\n  part_sizes: 8388608\n")
	if err == nil || !strings.Contains(err.Error(), "part_sizes") {
		t.Fatalf("unknown field error=%v", err)
	}
}
