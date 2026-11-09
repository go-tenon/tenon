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

	msgA, err := goi18n.NewLocalizer(a, "zh-CN").Localize(&goi18n.LocalizeConfig{MessageID: "greet"})
	if err != nil || msgA != "你好A" {
		t.Fatalf("a=%q err=%v", msgA, err)
	}
	msgB, err := goi18n.NewLocalizer(b, "zh-CN").Localize(&goi18n.LocalizeConfig{MessageID: "greet"})
	if err != nil || msgB != "你好B" {
		t.Fatalf("b=%q err=%v", msgB, err)
	}
	ok, err := goi18n.NewLocalizer(a, "en").Localize(&goi18n.LocalizeConfig{MessageID: "mygoravel.ok"})
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
	if got := i18n.MatchAcceptLanguage(b, "en-US,en;q=0.9"); got != "en" {
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
	fs := fstest.MapFS{"zh-CN.yaml": &fstest.MapFile{Data: []byte("only: x\n")}}
	first, err := i18n.New(i18n.Options{Layers: []i18n.Layer{{Priority: i18n.PriorityProduct, FS: fs}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := i18n.New(i18n.Options{Layers: []i18n.Layer{i18n.FrameworkLayer()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := goi18n.NewLocalizer(second, "zh-CN").Localize(&goi18n.LocalizeConfig{MessageID: "only"}); err == nil {
		t.Fatal("second bundle should not see first bundle messages")
	}
	if _, err := goi18n.NewLocalizer(first, "zh-CN").Localize(&goi18n.LocalizeConfig{MessageID: "mygoravel.ok"}); err == nil {
		t.Fatal("first bundle should not see framework messages it did not load")
	}
}

func TestRequiredTemplateAndUnsupportedLanguage(t *testing.T) {
	files := fstest.MapFS{"en.yaml": &fstest.MapFile{Data: []byte("greet: Hello {{.Name}}\n")}, "zh-CN.yaml": &fstest.MapFile{Data: []byte("greet: 你好 {{.Name}}\n")}}
	b, err := i18n.New(i18n.Options{Default: "en", RequiredIDs: []string{"greet"}, Layers: []i18n.Layer{{FS: files}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := i18n.MatchAcceptLanguage(b, "fr"); got != "en" {
		t.Fatal(got)
	}
	message, err := goi18n.NewLocalizer(b, "en").Localize(&goi18n.LocalizeConfig{MessageID: "greet", TemplateData: map[string]any{"Name": "Ada"}})
	if err != nil || message != "Hello Ada" {
		t.Fatal(message, err)
	}
}

func TestConfiguredLayerRequiresFilesystem(t *testing.T) {
	_, err := i18n.New(i18n.Options{Layers: []i18n.Layer{{Name: "product"}}})
	if err == nil || !strings.Contains(err.Error(), "product") {
		t.Fatalf("expected layer configuration error, got %v", err)
	}
}

func TestNegotiatedLanguageBelongsToBundle(t *testing.T) {
	bundle, err := i18n.New(i18n.Options{Layers: []i18n.Layer{i18n.FrameworkLayer()}})
	if err != nil {
		t.Fatal(err)
	}
	for header, want := range map[string]string{
		"en-US": "en", "en-GB": "en", "zh-Hans": "zh-CN",
		"fr": "zh-CN", "": "zh-CN", "???": "zh-CN",
	} {
		t.Run(header, func(t *testing.T) {
			if got := i18n.MatchAcceptLanguage(bundle, header); got != want {
				t.Fatalf("got %q want %q", got, want)
			}
		})
	}
}
