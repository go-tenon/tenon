// Package config loads a single YAML file into a Go struct.
//
// The model follows the convention used by most Go server products
// (Prometheus, Loki, ...): defaults live in Go code, one YAML file overrides
// them, unknown keys are rejected, and environment variables are referenced
// explicitly from the YAML file with ${VAR} or ${VAR:-default}.
//
//	cfg := DefaultConfig()
//	if err := config.Load(path, &cfg); err != nil {
//		log.Fatal(err)
//	}
//
// Decoding is done on top of the value dst points to, so fields that the
// file does not mention keep their defaults. Mappings are merged into
// existing maps; sequences replace existing slices.
//
// Environment references are expanded inside scalar values after the YAML
// has been parsed, so the content of a variable can never change the
// document structure (a password containing '#' or ': ' stays intact).
// Expansion rules:
//
//	${VAR}          value of VAR; an error if VAR is unset
//	${VAR:-default} value of VAR, or default if VAR is unset or empty
//	$${VAR}         the literal text ${VAR}
//
// A bare $VAR is left untouched. Mapping keys are never expanded.
//
// If dst implements [Validator], Validate is called after decoding.
package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"

	"go.yaml.in/yaml/v4"
)

// Validator is implemented by configuration types that check themselves
// after loading.
type Validator interface {
	Validate() error
}

// Load reads the YAML file at path and decodes it into dst, which must be a
// non-nil pointer. See the package documentation for the full semantics.
func Load(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := parse(data, dst); err != nil {
		return fmt.Errorf("config: %s: %w", path, err)
	}
	return nil
}

// Parse is like [Load] but takes the YAML document directly. It is useful
// for tests and for configuration embedded with go:embed.
func Parse(data []byte, dst any) error {
	if err := parse(data, dst); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

func parse(data []byte, dst any) error {
	if v := reflect.ValueOf(dst); v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("destination must be a non-nil pointer, got %T", dst)
	}

	root, err := parseDocument(data)
	if err != nil {
		return err
	}
	// An empty file has no document: keep the defaults as they are.
	if root != nil {
		if err := expandNode(root); err != nil {
			return err
		}
		if err := root.Load(dst, yaml.WithKnownFields()); err != nil {
			return err
		}
	}

	if v, ok := dst.(Validator); ok {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("invalid configuration: %w", err)
		}
	}
	return nil
}

// parseDocument parses data into a node tree. It returns nil for an empty
// stream (no content or only comments) and an error for more than one
// document.
func parseDocument(data []byte) (*yaml.Node, error) {
	l, err := yaml.NewLoader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := l.Load(&root); err == io.EOF {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := l.Load(&extra); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("line %d: expected a single YAML document, found more", extra.Line)
	}
	return &root, nil
}
