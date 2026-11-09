package i18n_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/ryan053857/mygoravel/i18n"
)

func TestBundleIsolationAndOverride(t *testing.T) {
	productA := fstest.MapFS{"zh-CN.yaml": &fstest.MapFile{Data: []byte("greet: 你好A\n")}}
	productB := fstest.MapFS{"zh-CN.yaml": &fstest.MapFile{Data: []byte("greet: 你好B\n")}}

	a, err := i18n.New(i18n.Options{
		Layers: []i18n.Layer{
			i18n.FrameworkLayer(),
			{Priority: i18n.PriorityProduct, FS: productA, Name: "a"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := i18n.New(i18n.Options{
		Layers: []i18n.Layer{
			i18n.FrameworkLayer(),
			{Priority: i18n.PriorityProduct, FS: productB, Name: "b"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	msgA, err := a.Localizer("zh-CN").Localize(&goi18n.LocalizeConfig{MessageID: "greet"})
	if err != nil || msgA != "你好A" {
		t.Fatalf("a=%q err=%v", msgA, err)
	}
	msgB, err := b.Localizer("zh-CN").Localize(&goi18n.LocalizeConfig{MessageID: "greet"})
	if err != nil || msgB != "你好B" {
		t.Fatalf("b=%q err=%v", msgB, err)
	}
	ok, err := a.Localizer("en").Localize(&goi18n.LocalizeConfig{MessageID: "mygoravel.ok"})
	if err != nil || ok != "OK" {
		t.Fatalf("framework en=%q err=%v", ok, err)
	}
}

func TestDuplicateSamePriority(t *testing.T) {
	one := fstest.MapFS{"zh-CN.yaml": &fstest.MapFile{Data: []byte("dup: one\n")}}
	two := fstest.MapFS{"zh-CN.yaml": &fstest.MapFile{Data: []byte("dup: two\n")}}
	_, err := i18n.New(i18n.Options{Layers: []i18n.Layer{
		{Priority: i18n.PriorityModule, FS: one, Name: "one"},
		{Priority: i18n.PriorityModule, FS: two, Name: "two"},
	}})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("got %v", err)
	}
}

func TestRequiredIDs(t *testing.T) {
	_, err := i18n.New(i18n.Options{
		Layers:      []i18n.Layer{i18n.FrameworkLayer()},
		RequiredIDs: []string{"mygoravel.ok", "missing.id"},
	})
	if err == nil || !strings.Contains(err.Error(), "missing.id") {
		t.Fatalf("got %v", err)
	}
}

func TestAcceptLanguageAndContext(t *testing.T) {
	b, err := i18n.New(i18n.Options{Layers: []i18n.Layer{i18n.FrameworkLayer()}})
	if err != nil {
		t.Fatal(err)
	}
	if got := b.MatchAcceptLanguage("en-US,en;q=0.9"); got != "en" && !strings.HasPrefix(got, "en") {
		t.Fatalf("got %q", got)
	}
	ctx := i18n.WithLocale(context.Background(), "en")
	if i18n.Locale(ctx) != "en" {
		t.Fatalf("locale=%q", i18n.Locale(ctx))
	}
	if i18n.Locale(context.Background()) != "" {
		t.Fatal("empty context should have empty locale")
	}
}

func TestNoGlobalCatalog(t *testing.T) {
	// 两个 Bundle 互不影响即证明没有可变全局目录。
	fs := fstest.MapFS{"zh-CN.yaml": &fstest.MapFile{Data: []byte("only: x\n")}}
	first, err := i18n.New(i18n.Options{Layers: []i18n.Layer{{Priority: i18n.PriorityProduct, FS: fs}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := i18n.New(i18n.Options{Layers: []i18n.Layer{i18n.FrameworkLayer()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Localizer("zh-CN").Localize(&goi18n.LocalizeConfig{MessageID: "only"}); err == nil {
		t.Fatal("second bundle should not see first bundle messages")
	}
	if _, err := first.Localizer("zh-CN").Localize(&goi18n.LocalizeConfig{MessageID: "mygoravel.ok"}); err == nil {
		t.Fatal("first bundle should not see framework messages it did not load")
	}
}
