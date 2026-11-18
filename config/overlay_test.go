package config_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/config"
)

// loadFiles 在临时目录写入 files，按 order 的顺序加载。
func loadFiles(t *testing.T, files map[string]string, order ...string) (appConfig, error) {
	t.Helper()
	dir := writeFiles(t, files)
	paths := make([]string, len(order))
	for i, name := range order {
		paths[i] = filepath.Join(dir, name)
	}
	return config.Load[appConfig](paths...)
}

func TestLaterFilesOverrideEarlier(t *testing.T) {
	cfg, err := loadFiles(t, map[string]string{
		"app.yaml":   "http:\n  addr: :8000\n  port: 1\ndb:\n  dsn: base\n",
		"prod.yaml":  "http:\n  port: 2\n",
		"local.yaml": "db:\n  password: local\n",
	}, "app.yaml", "prod.yaml", "local.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := defaults()
	want.HTTP.Addr = ":8000"
	want.HTTP.Port = 2
	want.DB = dbConfig{DSN: "base", Password: "local"}
	if cfg != want {
		t.Fatalf("got %+v want %+v", cfg, want)
	}
}

func TestOverlayMergesMapsAndReplacesSlices(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"app.yaml":  "tags: [a, b]\nlabels:\n  env: dev\n  tier: web\n",
		"prod.yaml": "tags: [c]\nlabels:\n  env: prod\n",
	})
	cfg, err := config.Load[collections](filepath.Join(dir, "app.yaml"), filepath.Join(dir, "prod.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Tags, ",") != "c" || cfg.Labels["env"] != "prod" || cfg.Labels["tier"] != "web" || cfg.Labels["team"] != "core" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestOverlayErrorsNameTheFile(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"unknown field": {"app.yaml": "http:\n  port: 1\n", "prod.yaml": "http:\n  prot: 2\n"},
		"missing env":   {"app.yaml": "http:\n  port: 1\n", "prod.yaml": "db:\n  dsn: ${TENON_TEST_UNSET}\n"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadFiles(t, files, "app.yaml", "prod.yaml")
			if err == nil || !strings.Contains(err.Error(), "prod.yaml") {
				t.Fatalf("want error naming prod.yaml, got %v", err)
			}
		})
	}
	_, err := loadFiles(t, map[string]string{"app.yaml": ""}, "app.yaml", "prod.yaml")
	if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), "prod.yaml") {
		t.Fatalf("want missing prod.yaml, got %v", err)
	}
}

func TestValidatesMergedResult(t *testing.T) {
	// 单独看 app.yaml 不合法，prod.yaml 补齐后合法：只校验合并后的结果
	files := map[string]string{
		"app.yaml":  "inner:\n  name: \"\"\n",
		"prod.yaml": "inner:\n  name: ok\n",
	}
	dir := writeFiles(t, files)
	if _, err := config.Load[validated](filepath.Join(dir, "app.yaml"), filepath.Join(dir, "prod.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load[validated](filepath.Join(dir, "app.yaml")); err == nil {
		t.Fatal("invalid config accepted")
	}
}

func TestLoadRequiresAFile(t *testing.T) {
	if _, err := config.Load[appConfig](); err == nil {
		t.Fatal("Load without files succeeded")
	}
}

// unsetEnv 在测试期间清除 keys，测试结束后恢复原值。
func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
}

func TestLoadEnv(t *testing.T) {
	unsetEnv(t, "TENON_TEST_ADDR", "TENON_TEST_PASSWORD")
	t.Setenv("TENON_TEST_KEEP", "") // 已设置的变量（包括空字符串）不被覆盖
	dir := writeFiles(t, map[string]string{
		".env":     "# comment\nexport TENON_TEST_ADDR=:7001\nTENON_TEST_PASSWORD='s3cr$ET value'\nTENON_TEST_KEEP=from-file\n",
		"app.yaml": "http:\n  addr: ${TENON_TEST_ADDR}\ndb:\n  password: ${TENON_TEST_PASSWORD}\n  dsn: x${TENON_TEST_KEEP}\n",
	})
	if err := config.LoadEnv(filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load[appConfig](filepath.Join(dir, "app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":7001" || cfg.DB.Password != "s3cr$ET value" || cfg.DB.DSN != "x" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestLoadEnvErrors(t *testing.T) {
	if err := config.LoadEnv(filepath.Join(t.TempDir(), ".env")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
	dir := writeFiles(t, map[string]string{".env": "BAD-KEY=value\n"})
	if err := config.LoadEnv(filepath.Join(dir, ".env")); err == nil || !strings.Contains(err.Error(), ".env") {
		t.Fatalf("got %v", err)
	}
}

type named struct {
	Name string `yaml:"name"`
}

func (n named) Validate() error {
	if n.Name == "" {
		return errors.New("name is required")
	}
	return nil
}

type validated struct {
	Inner named `yaml:"inner"`
}

// 前面文件中引用了未设置的环境变量的项，由后面的文件给出时不是错误，例如 app.yaml 的 ${APP_KEY} 由 local.yaml 给出。
func TestLaterFileSetsUnsetEnv(t *testing.T) {
	cfg, err := loadFiles(t, map[string]string{
		"app.yaml":   "http:\n  port: ${TENON_TEST_UNSET}\n  addr: :8000\ndb:\n  password: ${TENON_TEST_UNSET}\n",
		"local.yaml": "http:\n  port: 2\ndb:\n  password: local\n",
	}, "app.yaml", "local.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Port != 2 || cfg.HTTP.Addr != ":8000" || cfg.DB.Password != "local" {
		t.Fatalf("got %+v", cfg)
	}

	// 列表整体替换：后面的文件给出列表时，前面列表中未设置的项不再需要。
	dir := writeFiles(t, map[string]string{
		"app.yaml":  "tags: [a, '${TENON_TEST_UNSET}']\n",
		"prod.yaml": "tags: [c]\n",
	})
	if c, err := config.Load[collections](filepath.Join(dir, "app.yaml"), filepath.Join(dir, "prod.yaml")); err != nil || strings.Join(c.Tags, ",") != "c" {
		t.Fatalf("tags %+v, %v", c.Tags, err)
	}
}

func TestUnsetEnvNotSetLaterFails(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"not overridden":    {"app.yaml": "db:\n  dsn: base\n  password: ${TENON_TEST_UNSET}\n", "local.yaml": "db:\n  dsn: local\n"},
		"set earlier only":  {"app.yaml": "db:\n  password: base\n", "local.yaml": "db:\n  password: ${TENON_TEST_UNSET}\n"},
		"overridden by map": {"app.yaml": "db:\n  password: ${TENON_TEST_UNSET}\n", "local.yaml": "db: {}\n"},
	} {
		_, err := loadFiles(t, files, "app.yaml", "local.yaml")
		if err == nil || !strings.Contains(err.Error(), "environment variable TENON_TEST_UNSET is not set") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	_, err := loadFiles(t, map[string]string{"app.yaml": "db:\n  password: ${TENON_TEST_UNSET}\n", "local.yaml": "db:\n  dsn: x\n"}, "app.yaml", "local.yaml")
	if err == nil || !strings.Contains(err.Error(), "app.yaml: line 2") {
		t.Errorf("error should name the file and line: %v", err)
	}
}

func TestValue(t *testing.T) {
	t.Setenv("TENON_TEST_PORT", "9100")
	dir := writeFiles(t, map[string]string{
		"app.yaml":   "http:\n  addr: :8000\n  port: ${TENON_TEST_PORT}\ndb:\n  dsn: ${TENON_TEST_UNSET}\n",
		"local.yaml": "http:\n  addr: :8081\n",
		"empty.yaml": "",
	})
	app, local, empty := filepath.Join(dir, "app.yaml"), filepath.Join(dir, "local.yaml"), filepath.Join(dir, "empty.yaml")
	for _, tc := range []struct {
		key   string
		files []string
		want  string
		ok    bool
	}{
		{"http.addr", []string{app}, ":8000", true},
		{"http.addr", []string{app, local}, ":8081", true}, // 后面的文件覆盖前面的
		{"http.port", []string{app, local}, "9100", true},  // 展开环境变量
		{"http.timeout", []string{app, local}, "", false},
		{"logs.level", []string{empty}, "", false},
	} {
		v, ok, err := config.Value(tc.key, tc.files...)
		if err != nil || v != tc.want || ok != tc.ok {
			t.Errorf("Value(%s) = %q, %v, %v", tc.key, v, ok, err)
		}
	}
	if _, _, err := config.Value("db.dsn", app); err == nil || !strings.Contains(err.Error(), "TENON_TEST_UNSET is not set") {
		t.Errorf("unset env: %v", err)
	}
	if _, _, err := config.Value("http", app); err == nil || !strings.Contains(err.Error(), "not a single value") {
		t.Errorf("mapping: %v", err)
	}
}
