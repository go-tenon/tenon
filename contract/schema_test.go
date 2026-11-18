package contract_test

import (
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/contract"
)

// Source 与 log/slog.Source 同名。
type Source struct {
	Path string `json:"path" minLength:"1"`
}

type sourcesArgs struct {
	Log  slog.Source `json:"log"`
	File Source      `json:"file"`
}

// 不同包的同名类型在 $defs 中按 SchemaName 各有自己的名字，与 OpenAPI 文档相同。
func TestNewSchemaSameNameDifferentPackages(t *testing.T) {
	t.Parallel()
	s := contract.NewSchema(reflect.TypeFor[sourcesArgs](), huma.ModeWriteToServer)
	data, err := json.Marshal(s.JSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"log":{"$ref":"#/$defs/SlogSource"}`,
		`"file":{"$ref":"#/$defs/Source"}`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("schema missing %s:\n%s", want, data)
		}
	}
	var args sourcesArgs
	details, err := s.Validate([]byte(`{"log":{"function":"main","file":"main.go","line":1},"file":{"path":""}}`), &args, "")
	if err != nil || len(details) != 1 || details[0].Location != "file.path" {
		t.Fatalf("validate: %v %v", details, err)
	}
}
