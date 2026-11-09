package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ryan053857/mygoravel/config"
)

func TestRejectMalformedStructure(t *testing.T) {
	for _, src := range []string{"hello", "- http", "http: {}\nhttp: {}", "http:\n  port: 1\n  port: 2", "http:\n  1: x"} {
		t.Run(src, func(t *testing.T) {
			if _, err := config.Parse([]byte(src), config.WithSections("http")); err == nil {
				t.Fatal("accepted malformed structure")
			}
		})
	}
}
func TestSecretEveryFormattingVerb(t *testing.T) {
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X"} {
		got := fmt.Sprintf(verb, config.Secret("regression-secret"))
		if strings.Contains(got, "regression-secret") || strings.Contains(got, "72656772657373696f6e") {
			t.Fatalf("%s leaked: %s", verb, got)
		}
	}
}

func TestInvalidValuesDoNotLeakAndPathsRemain(t *testing.T) {
	type section struct {
		Port int `yaml:"port"`
	}
	doc, err := config.Parse([]byte("http:\n  port: private-secret-value\n"), config.WithSections("http"))
	if err != nil {
		t.Fatal(err)
	}
	err = doc.Decode("http", &section{})
	if err == nil || strings.Contains(fmt.Sprintf("%+v", err), "private-secret-value") || !strings.Contains(err.Error(), "http.port") {
		t.Fatal(err)
	}
	for _, input := range []string{"${broken-private-secret", "${bad name:-private-secret}"} {
		doc, err := config.Parse([]byte("http:\n  port: "+input), config.WithSections("http"), config.WithLookup(func(string) (string, bool) { return "", false }))
		if err != nil {
			t.Fatal(err)
		}
		if err = doc.Decode("http", &section{}); err == nil || strings.Contains(fmt.Sprintf("%+v", err), "private-secret") {
			t.Fatal(err)
		}
	}
}
func TestAliasesAreDecodedIndependently(t *testing.T) {
	doc, err := config.Parse([]byte("one: &shared\n  port: ${PORT}\ntwo: *shared\n"), config.WithSections("one", "two"), config.WithLookup(func(string) (string, bool) { return "123", true }))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"two", "one"} {
		var s struct {
			Port int `yaml:"port"`
		}
		if err := doc.Decode(name, &s); err != nil || s.Port != 123 {
			t.Fatal(s, err)
		}
	}
}
