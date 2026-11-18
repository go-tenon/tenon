package config

import (
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
	"go.yaml.in/yaml/v4"
)

// lookupFunc 查找环境变量；第二个返回值表示是否已设置（空字符串仍算已设置）。
type lookupFunc func(key string) (string, bool)

// unsetRef 是引用了未设置的环境变量的一项：它在文档中的键路径和行号。
type unsetRef struct {
	path string // 键路径，例如 crypt.key、tags[0]；文档的根是空字符串
	name string // 环境变量名
	line int
}

// unsetError 表示 ${NAME} 引用的环境变量没有设置，也没有默认值。
type unsetError struct{ name string }

// Error 返回错误信息，其中给出允许空值的写法。
func (e *unsetError) Error() string {
	return "environment variable " + e.name + " is not set (use ${" + e.name + ":-} to allow empty, or set it in a later config file)"
}

// expandNode 展开 YAML 节点 n 及其子节点中的 ${NAME} 环境变量引用，path 是 n 的键路径。
// 引用了未设置的变量的项从文档中去掉，不参与解码，记入 unset；drop 表示 n 本身要去掉。
// 后面的配置文件可以设置这一项，Load 在全部文件叠加之后才把仍未设置的项作为错误返回。
func expandNode(n *yaml.Node, path string, lookup lookupFunc, unset *[]unsetRef) (drop bool, err error) {
	switch n.Kind {
	case yaml.ScalarNode:
		return expandScalar(n, path, lookup, unset)
	case yaml.MappingNode:
		kept := n.Content[:0]
		for i := 0; i+1 < len(n.Content); i += 2 {
			drop, err := expandNode(n.Content[i+1], joinKey(path, n.Content[i].Value), lookup, unset)
			if err != nil {
				return false, err
			}
			if !drop {
				kept = append(kept, n.Content[i], n.Content[i+1])
			}
		}
		n.Content = kept
	case yaml.SequenceNode:
		kept := n.Content[:0]
		for i, c := range n.Content {
			drop, err := expandNode(c, path+"["+strconv.Itoa(i)+"]", lookup, unset)
			if err != nil {
				return false, err
			}
			if !drop {
				kept = append(kept, c)
			}
		}
		n.Content = kept
	case yaml.DocumentNode:
		for _, c := range n.Content {
			if _, err := expandNode(c, path, lookup, unset); err != nil {
				return false, err
			}
		}
	}
	return false, nil
}

// joinKey 返回 path 下键 key 的路径。
func joinKey(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// expandScalar 展开标量节点中的环境变量引用。引用的变量没有设置时记入 unset，返回 drop。
func expandScalar(n *yaml.Node, path string, lookup lookupFunc, unset *[]unsetRef) (drop bool, err error) {
	if !strings.Contains(n.Value, "${") {
		return false, nil
	}
	v, err := expand(n.Value, lookup)
	var ue *unsetError
	if errors.As(err, &ue) {
		*unset = append(*unset, unsetRef{path: path, name: ue.name, line: n.Line})
		return true, nil
	}
	if err != nil {
		return false, errors.Wrapf(err, "line %d", n.Line)
	}
	n.Value = v
	if n.Style&yaml.TaggedStyle == 0 {
		n.Tag = ""
	}
	return false, nil
}

// keyPaths 返回文档 n 中出现的每一项的键路径和节点类型，用来判断后面的文件是否设置了某一项。
func keyPaths(n *yaml.Node, path string, out map[string]yaml.Kind) {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			keyPaths(c, path, out)
		}
	case yaml.MappingNode:
		out[path] = n.Kind
		for i := 0; i+1 < len(n.Content); i += 2 {
			keyPaths(n.Content[i+1], joinKey(path, n.Content[i].Value), out)
		}
	case yaml.SequenceNode:
		out[path] = n.Kind
		for i, c := range n.Content {
			keyPaths(c, path+"["+strconv.Itoa(i)+"]", out)
		}
	default:
		out[path] = n.Kind
	}
}

// setsKey 报告 paths（后面一个文件的 keyPaths）是否设置了 path：设置了这一项本身，
// 或者设置了包含它的列表（列表整体替换）。
func setsKey(paths map[string]yaml.Kind, path string) bool {
	if kind, ok := paths[path]; ok && kind != yaml.MappingNode {
		return true
	}
	for p := path; ; {
		i := strings.LastIndexAny(p, ".[")
		if i < 0 {
			return false
		}
		p = p[:i]
		if paths[p] == yaml.SequenceNode {
			return true
		}
	}
}

// expand 展开 s 中的 ${NAME} 和 ${NAME:-默认值}，变量由 lookup 查找。
func expand(s string, lookup lookupFunc) (string, error) {
	var b strings.Builder
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			b.WriteString(s)
			return b.String(), nil
		}
		if i > 0 && s[i-1] == '$' {
			b.WriteString(s[:i-1])
			b.WriteString("${")
			s = s[i+2:]
			continue
		}
		b.WriteString(s[:i])

		end := strings.IndexByte(s[i+2:], '}')
		if end < 0 {
			return "", errors.New("unterminated environment reference")
		}
		ref := s[i+2 : i+2+end]
		s = s[i+2+end+1:]

		name, def, hasDef := strings.Cut(ref, ":-")
		if !validName(name) {
			return "", errors.Newf("invalid environment variable reference ${%s}", ref)
		}
		val, ok := lookup(name)
		switch {
		case hasDef && val == "":
			val = def
		case !ok:
			return "", &unsetError{name: name}
		}
		b.WriteString(val)
	}
}

// validName 报告 name 是否是合法的环境变量名。
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
