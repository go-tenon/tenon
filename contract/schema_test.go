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

// 超过 float64 精确整数范围（2^53）的 int64 在无默认值和补填默认值时都保持精度不丢失。
func TestValidatePreservesInt64Precision(t *testing.T) {
	t.Parallel()
	type payload struct {
		ID   int64  `json:"id"`
		Mode string `json:"mode,omitempty" default:"fast"`
	}
	s := contract.NewSchema(reflect.TypeFor[payload](), huma.ModeWriteToServer)
	const want int64 = 9007199254740993 // 2^53 + 1

	var a payload
	details, err := s.Validate([]byte(`{"id":9007199254740993,"mode":"strong"}`), &a, "")
	if err != nil || len(details) > 0 || a.ID != want || a.Mode != "strong" {
		t.Fatalf("without default: a=%+v details=%v err=%v", a, details, err)
	}

	var b payload
	details, err = s.Validate([]byte(`{"id":9007199254740993}`), &b, "")
	if err != nil || len(details) > 0 || b.ID != want || b.Mode != "fast" {
		t.Fatalf("with default: b=%+v details=%v err=%v", b, details, err)
	}
}
