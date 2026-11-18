package contract_test

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/contract"
)

// payload 覆盖编码规则关心的几种字段：nil 的切片和 map、omitempty、指针、带 MarshalJSON 方法的类型、需要转义的字符。
type payload struct {
	Tags     []string          `json:"tags"`
	Extra    map[string]string `json:"extra"`
	Optional []string          `json:"optional,omitempty"`
	Note     *string           `json:"note"`
	At       time.Time         `json:"at"`
	HTML     string            `json:"html"`
}

func TestMarshalJSON(t *testing.T) {
	t.Parallel()
	v := payload{At: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), HTML: "<a>&"}
	got, err := contract.MarshalJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"tags":[],"extra":{},"note":null,"at":"2026-01-02T03:04:05Z","html":"\u003ca\u003e\u0026"}`
	if string(got) != want {
		t.Fatalf("MarshalJSON:\n got %s\nwant %s", got, want)
	}

	// EncodeJSON 与 Huma 默认的格式相同：不转义 HTML 字符，末尾有换行
	var buf bytes.Buffer
	if err := contract.EncodeJSON(&buf, v); err != nil {
		t.Fatal(err)
	}
	want = `{"tags":[],"extra":{},"note":null,"at":"2026-01-02T03:04:05Z","html":"<a>&"}` + "\n"
	if buf.String() != want {
		t.Fatalf("EncodeJSON:\n got %q\nwant %q", buf.String(), want)
	}
}

func TestArraysNotNullable(t *testing.T) {
	t.Parallel()
	type body struct {
		Tags []string `json:"tags"`
		Ids  []string `json:"ids" nullable:"true"`
	}
	s := contract.NewSchema(reflect.TypeOf(body{}), huma.ModeWriteToServer)
	var dst body
	problems, err := s.Validate([]byte(`{"tags":null,"ids":null}`), &dst, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Location != "tags" {
		t.Fatalf("数组默认不可为空，nullable:\"true\" 的可以：%+v", problems)
	}
}
