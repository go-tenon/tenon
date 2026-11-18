package config

import (
	"bytes"
	"io"
	"os"
	"strings"

	"github.com/cockroachdb/errors"
	"go.yaml.in/yaml/v4"
)

// Value 返回依次叠加 files 之后 key 的值：key 是以点分隔的键路径，例如 http.addr；值中的 ${VAR} 按 Load 的规则展开，
// 后面的文件覆盖前面的。它只读取这一项，不需要知道整个配置的结构，用于开发工具，例如 tenon dev 从应用的配置中
// 推出应用的地址。文件中都没有这一项时 ok 为 false；最后给出它的文件引用了未设置的环境变量时返回错误。
func Value(key string, files ...string) (value string, ok bool, err error) {
	var unset error
	for _, path := range files {
		n, err := lookupKey(path, key)
		if err != nil {
			return "", false, errors.Wrapf(err, "config: %s", path)
		}
		if n == nil {
			continue
		}
		if n.Kind != yaml.ScalarNode {
			return "", false, errors.Newf("config: %s: line %d: %s is not a single value", path, n.Line, key)
		}
		v, err := expand(n.Value, os.LookupEnv)
		var ue *unsetError
		switch {
		case errors.As(err, &ue):
			unset = errors.Wrapf(errors.Wrapf(err, "line %d", n.Line), "config: %s", path)
		case err != nil:
			return "", false, errors.Wrapf(errors.Wrapf(err, "line %d", n.Line), "config: %s", path)
		default:
			value, ok, unset = v, true, nil
		}
	}
	if unset != nil {
		return "", false, unset
	}
	return value, ok, nil
}

// lookupKey 返回文件 path 中键路径 key 的节点，没有这一项时返回 nil。
func lookupKey(path, key string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	loader, err := yaml.NewLoader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := loader.Load(&doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, nil
	}
	n := doc.Content[0]
	for seg := range strings.SplitSeq(key, ".") {
		if n.Kind != yaml.MappingNode {
			return nil, nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == seg {
				next = n.Content[i+1]
			}
		}
		if next == nil {
			return nil, nil
		}
		n = next
	}
	return n, nil
}
