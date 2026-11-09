package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"go.yaml.in/yaml/v4"
)

type testConfig struct {
	Name string `yaml:"name"`
	HTTP struct {
		Addr    string        `yaml:"addr"`
		Port    int           `yaml:"port"`
		Timeout time.Duration `yaml:"timeout"`
		Debug   bool          `yaml:"debug"`
	} `yaml:"http"`
	DB struct {
		DSN      Secret `yaml:"dsn"`
		Password Secret `yaml:"password"`
	} `yaml:"db"`
	Tags   []string          `yaml:"tags"`
	Labels map[string]string `yaml:"labels"`
}

func defaultTestConfig() testConfig {
	var c testConfig
	c.Name = "demo"
	c.HTTP.Addr = ":8080"
	c.HTTP.Port = 8080
	c.HTTP.Timeout = 5 * time.Second
	c.Tags = []string{"default"}
	c.Labels = map[string]string{"team": "core"}
	return c
}

func TestParseOverridesDefaults(t *testing.T) {
	cfg := defaultTestConfig()
	err := Parse([]byte(`
http:
  port: 9000
  timeout: 10s
tags: [a, b]
labels:
  env: prod
`), &cfg)
	if err != nil {
		t.Fatal(err)
	}

	want := defaultTestConfig()
	want.HTTP.Port = 9000
	want.HTTP.Timeout = 10 * time.Second
	want.Tags = []string{"a", "b"}                                 // sequences replace
	want.Labels = map[string]string{"team": "core", "env": "prod"} // mappings merge
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got  %+v\nwant %+v", cfg, want)
	}
}

func TestParseEmptyKeepsDefaults(t *testing.T) {
	for _, doc := range []string{"", "\n", "  \n\t\n", "# only a comment\n", "---\n"} {
		cfg := defaultTestConfig()
		if err := Parse([]byte(doc), &cfg); err != nil {
			t.Fatalf("%q: %v", doc, err)
		}
		if !reflect.DeepEqual(cfg, defaultTestConfig()) {
			t.Errorf("%q: defaults changed: %+v", doc, cfg)
		}
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	cfg := defaultTestConfig()
	err := Parse([]byte("http:\n  port: 1\n\n\n  prot: 2\n"), &cfg)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "prot") || !strings.Contains(err.Error(), "line 5") {
		t.Errorf("error should name the field and its line: %v", err)
	}
}

func TestParseTypeErrorReportsOriginalLine(t *testing.T) {
	t.Setenv("TEST_NAME", "x")
	cfg := defaultTestConfig()
	// The expanded line comes first so a re-encoded document would shift lines.
	err := Parse([]byte("name: ${TEST_NAME}\n\n\nhttp:\n  port: abc\n"), &cfg)
	if err == nil || !strings.Contains(err.Error(), "line 5") {
		t.Errorf("want type error on line 5, got %v", err)
	}
}

func TestParseRejectsBadDestination(t *testing.T) {
	var cfg testConfig
	var nilPtr *testConfig
	for _, dst := range []any{nil, cfg, nilPtr} {
		if err := Parse([]byte("name: x"), dst); err == nil {
			t.Errorf("%T: expected error", dst)
		}
	}
}

func TestParseInvalidYAML(t *testing.T) {
	cfg := defaultTestConfig()
	if err := Parse([]byte("http: [\n"), &cfg); err == nil {
		t.Fatal("expected syntax error")
	}
}

func TestParseRejectsMultipleDocuments(t *testing.T) {
	cfg := defaultTestConfig()
	err := Parse([]byte("name: a\n---\nname: b\n"), &cfg)
	if err == nil || !strings.Contains(err.Error(), "single YAML document") {
		t.Fatalf("want multiple documents error, got %v", err)
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("TEST_PORT", "9100")
	t.Setenv("TEST_DEBUG", "true")
	t.Setenv("TEST_HOST", "db.local")
	t.Setenv("TEST_PASSWORD", "p@ss #word: {x}") // would break text substitution
	t.Setenv("TEST_EMPTY", "")

	cfg := defaultTestConfig()
	err := Parse([]byte(`
name: ${TEST_MISSING:-fallback}
http:
  port: ${TEST_PORT}
  debug: ${TEST_DEBUG}
  addr: ${TEST_EMPTY:-:7000}
db:
  dsn: postgres://${TEST_HOST}:5432/app?cost=$5
  password: ${TEST_PASSWORD}
tags:
  - ${TEST_HOST}
  - $${TEST_HOST}
  - $TEST_HOST
`), &cfg)
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct{ got, want any }{
		{cfg.Name, "fallback"},
		{cfg.HTTP.Port, 9100},
		{cfg.HTTP.Debug, true},
		{cfg.HTTP.Addr, ":7000"},
		{string(cfg.DB.DSN), "postgres://db.local:5432/app?cost=$5"},
		{string(cfg.DB.Password), "p@ss #word: {x}"},
		{cfg.Tags, []string{"db.local", "${TEST_HOST}", "$TEST_HOST"}},
	}
	for i, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("check %d: got %#v, want %#v", i, c.got, c.want)
		}
	}
}

func TestEnvExpansionKeepsStringStyle(t *testing.T) {
	t.Setenv("TEST_VERSION", "1.10")
	t.Setenv("TEST_PORT", "9100")
	var cfg struct {
		Plain  string `yaml:"plain"`
		Quoted string `yaml:"quoted"`
		Any    any    `yaml:"any"`
		Port   int    `yaml:"port"`
	}
	err := Parse([]byte(`
plain: ${TEST_VERSION}
quoted: "${TEST_VERSION}"
any: "${TEST_VERSION}"
`), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plain != "1.10" || cfg.Quoted != "1.10" || cfg.Any != "1.10" {
		t.Errorf("got %+v", cfg)
	}

	// Quoting marks the value as a string, exactly as with a literal "9100".
	if err := Parse([]byte(`port: "${TEST_PORT}"`), &cfg); err == nil {
		t.Error("quoted value should not decode into int")
	}
}

func TestEnvExpansionDoesNotTouchKeys(t *testing.T) {
	t.Setenv("TEST_KEY", "k")
	var cfg struct {
		Labels map[string]string `yaml:"labels"`
	}
	if err := Parse([]byte("labels:\n  ${TEST_KEY}: v\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Labels["${TEST_KEY}"]; !ok {
		t.Errorf("key was expanded: %v", cfg.Labels)
	}
}

func TestEnvExpansionErrors(t *testing.T) {
	os.Unsetenv("TEST_UNSET")
	cases := map[string]string{
		"unset":        "name: ${TEST_UNSET}",
		"unterminated": "name: ${TEST_UNSET",
		"empty name":   "name: ${}",
		"bad name":     "name: ${1ABC}",
		"bad syntax":   "name: ${A-B}",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := defaultTestConfig()
			err := Parse([]byte("\n"+doc), &cfg)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "line 2") {
				t.Errorf("error should report line 2: %v", err)
			}
		})
	}
}

func TestEnvUnsetAllowedEmpty(t *testing.T) {
	os.Unsetenv("TEST_UNSET")
	cfg := defaultTestConfig()
	if err := Parse([]byte(`name: "${TEST_UNSET:-}"`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "" {
		t.Errorf("got %q", cfg.Name)
	}
}

func TestEnvExpansionInAnchors(t *testing.T) {
	t.Setenv("TEST_HOST", "h")
	var cfg struct {
		A string   `yaml:"a"`
		B []string `yaml:"b"`
	}
	// $${...} inside an anchor must be unescaped once, not again via the alias.
	err := Parse([]byte("a: &x ${TEST_HOST}-$${TEST_HOST}\nb: [*x, *x]\n"), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := "h-${TEST_HOST}"
	if cfg.A != want || !reflect.DeepEqual(cfg.B, []string{want, want}) {
		t.Errorf("got %+v", cfg)
	}
}

type validatedConfig struct {
	Port int `yaml:"port"`
}

func (c *validatedConfig) Validate() error {
	if c.Port <= 0 {
		return errors.New("port must be positive")
	}
	return nil
}

var errSentinel = errors.New("sentinel")

type sentinelConfig struct{}

func (*sentinelConfig) Validate() error { return errSentinel }

func TestValidate(t *testing.T) {
	cfg := validatedConfig{Port: 80}
	if err := Parse([]byte("port: 8080"), &cfg); err != nil {
		t.Fatal(err)
	}
	err := Parse([]byte("port: 0"), &cfg)
	if err == nil || !strings.Contains(err.Error(), "port must be positive") {
		t.Errorf("want validation error, got %v", err)
	}
	if err := Parse(nil, &sentinelConfig{}); !errors.Is(err, errSentinel) {
		t.Errorf("validation error should be wrapped, got %v", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("name: from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := defaultTestConfig()
	if err := Load(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "from-file" || cfg.HTTP.Port != 8080 {
		t.Errorf("got %+v", cfg)
	}

	err := Load(filepath.Join(dir, "missing.yaml"), &cfg)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want ErrNotExist, got %v", err)
	}

	if err := os.WriteFile(path, []byte("nmae: typo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = Load(path, &cfg)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("error should include the path: %v", err)
	}
}

func TestSecretIsMasked(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.DB.DSN = "postgres://user:hunter2@db/app"

	yamlOut, err := yaml.Dump(cfg)
	if err != nil {
		t.Fatal(err)
	}
	jsonOut, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var logOut strings.Builder
	slog.New(slog.NewTextHandler(&logOut, nil)).Info("cfg", "dsn", cfg.DB.DSN, "cfg", cfg)
	var jsonLogOut strings.Builder
	slog.New(slog.NewJSONHandler(&jsonLogOut, nil)).Info("cfg", "dsn", cfg.DB.DSN, "cfg", cfg)

	outputs := map[string]string{
		"yaml":      string(yamlOut),
		"json":      string(jsonOut),
		"%v":        fmt.Sprintf("%v", cfg),
		"%+v":       fmt.Sprintf("%+v", cfg),
		"%#v":       fmt.Sprintf("%#v", cfg),
		"%s":        fmt.Sprintf("%s", cfg.DB.DSN),
		"slog text": logOut.String(),
		"slog json": jsonLogOut.String(),
	}
	for name, out := range outputs {
		if strings.Contains(out, "hunter2") {
			t.Errorf("%s leaks the secret: %s", name, out)
		}
		if !strings.Contains(out, secretMask) {
			t.Errorf("%s does not show the mask: %s", name, out)
		}
	}

	if Secret("").String() != "" {
		t.Error("empty secret should print as empty")
	}
}

func TestSecretDecodesAsString(t *testing.T) {
	cfg := defaultTestConfig()
	if err := Parse([]byte("db:\n  dsn: abc\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if string(cfg.DB.DSN) != "abc" {
		t.Errorf("got %q", string(cfg.DB.DSN))
	}
}
