package config

import (
	"os"
	"strings"

	"github.com/cockroachdb/errors"
	"go.yaml.in/yaml/v4"
)

// expandNode expands ${...} references in every scalar value below n.
// Mapping keys are left alone. Alias nodes are skipped because the node they
// point to is expanded where it is defined.
func expandNode(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		return expandScalar(n)
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			if err := expandNode(n.Content[i]); err != nil {
				return err
			}
		}
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if err := expandNode(c); err != nil {
				return err
			}
		}
	}
	return nil
}

func expandScalar(n *yaml.Node) error {
	if !strings.Contains(n.Value, "${") {
		return nil
	}
	v, err := expand(n.Value)
	if err != nil {
		return errors.Wrapf(err, "line %d", n.Line)
	}
	n.Value = v
	// The resolved tag was computed from the unexpanded text (always !!str).
	// Drop it so a plain scalar is resolved again from its new value, e.g.
	// "port: ${PORT}" decodes into an int. Quoted scalars stay strings, and
	// an explicit tag written by the author is kept.
	if n.Style&yaml.TaggedStyle == 0 {
		n.Tag = ""
	}
	return nil
}

// expand replaces ${VAR} and ${VAR:-default} in s. "$${" produces a literal
// "${". Any other '$' is copied unchanged.
func expand(s string) (string, error) {
	var b strings.Builder
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			b.WriteString(s)
			return b.String(), nil
		}
		if i > 0 && s[i-1] == '$' {
			// "$${" escape: drop one '$' and keep "${" literally.
			b.WriteString(s[:i-1])
			b.WriteString("${")
			s = s[i+2:]
			continue
		}
		b.WriteString(s[:i])

		end := strings.IndexByte(s[i+2:], '}')
		if end < 0 {
			return "", errors.Newf("unterminated reference in %q", s[i:])
		}
		ref := s[i+2 : i+2+end]
		s = s[i+2+end+1:]

		name, def, hasDef := strings.Cut(ref, ":-")
		if !validName(name) {
			return "", errors.Newf("invalid environment variable reference ${%s}", ref)
		}
		val, ok := os.LookupEnv(name)
		switch {
		case hasDef && val == "":
			val = def
		case !ok:
			return "", errors.Newf("environment variable %s is not set (use ${%s:-} to allow empty)", name, name)
		}
		b.WriteString(val)
	}
}

func validName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		switch {
		case c == '_', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
