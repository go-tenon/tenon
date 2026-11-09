package config

import (
	"bytes"
	"io"
	"os"
	"reflect"

	"github.com/cockroachdb/errors"
	"go.yaml.in/yaml/v4"
)

// Validator 由配置类型在解码后自检。
type Validator interface {
	Validate() error
}

// Document 是已解析、尚未按段展开环境变量的配置文档。
type Document struct {
	root     *yaml.Node // DocumentNode，或 nil 表示空流
	sections map[string]struct{}
	lookup   LookupFunc
	path     string // 仅用于错误前缀，可为空
}

// ReadFile 读取 path 并解析为 Document，不展开环境变量。
func ReadFile(path string, opts ...Option) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "config")
	}
	doc, err := Parse(data, opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "config: %s", path)
	}
	doc.path = path
	return doc, nil
}

// Parse 从 YAML 字节解析 Document，不展开环境变量。
func Parse(data []byte, opts ...Option) (*Document, error) {
	o := applyOptions(opts)
	if len(o.sections) == 0 {
		return nil, errors.New("config: at least one section must be declared with WithSections")
	}
	root, err := parseDocument(data)
	if err != nil {
		return nil, errors.Wrap(err, "config")
	}
	if root != nil {
		if err := rejectNulls(root); err != nil {
			return nil, errors.Wrap(err, "config")
		}
		if err := checkAllowedSections(root, o.sections); err != nil {
			return nil, errors.Wrap(err, "config")
		}
	}
	return &Document{root: root, sections: o.sections, lookup: o.lookup}, nil
}

// Decode 将名为 section 的顶层段解码到 dst（必须是非 nil 指针）。
// 仅在该段内展开环境变量；段缺失时保留 dst 中的默认值。
// 若 dst 实现 Validator，解码成功后调用 Validate。
func (d *Document) Decode(section string, dst any) error {
	if d == nil {
		return errors.New("config: nil document")
	}
	if _, ok := d.sections[section]; !ok {
		return errors.Newf("config: section %q is not declared", section)
	}
	if v := reflect.ValueOf(dst); v.Kind() != reflect.Pointer || v.IsNil() {
		return errors.Newf("config: destination must be a non-nil pointer, got %T", dst)
	}

	node := d.sectionNode(section)
	if node != nil {
		cloned, err := cloneNode(node)
		if err != nil {
			return err
		}
		if err := expandNode(cloned, d.lookup); err != nil {
			return wrapPath(d.path, err)
		}
		if err := cloned.Load(dst, yaml.WithKnownFields()); err != nil {
			return wrapPath(d.path, err)
		}
	}

	if v, ok := dst.(Validator); ok {
		if err := v.Validate(); err != nil {
			return wrapPath(d.path, errors.Wrap(err, "invalid configuration"))
		}
	}
	return nil
}

// HasSection 报告文档是否包含该顶层段（不论是否已声明）。
func (d *Document) HasSection(section string) bool {
	return d != nil && d.sectionNode(section) != nil
}

func wrapPath(path string, err error) error {
	if err == nil {
		return nil
	}
	if path == "" {
		return errors.Wrap(err, "config")
	}
	return errors.Wrapf(err, "config: %s", path)
}

func (d *Document) sectionNode(section string) *yaml.Node {
	body := mappingBody(d.root)
	if body == nil {
		return nil
	}
	for i := 0; i+1 < len(body.Content); i += 2 {
		k := body.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == section {
			return body.Content[i+1]
		}
	}
	return nil
}

func mappingBody(root *yaml.Node) *yaml.Node {
	if root == nil {
		return nil
	}
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

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
		return nil, errors.Newf("line %d: expected a single YAML document, found more", extra.Line)
	}
	return &root, nil
}

func checkAllowedSections(root *yaml.Node, allowed map[string]struct{}) error {
	body := mappingBody(root)
	if body == nil {
		// 空文档、纯标量等：无顶层段可声明违规。
		if root.Kind == yaml.DocumentNode && len(root.Content) == 0 {
			return nil
		}
		if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
			n := root.Content[0]
			if n.Kind == yaml.ScalarNode && (n.Value == "" || n.Tag == "!!null") {
				return nil
			}
			if n.Kind == yaml.MappingNode {
				body = n
			} else if n.Kind != yaml.DocumentNode {
				// 非 mapping 根：没有段名，视为无未知段。
				return nil
			}
		}
		if body == nil {
			return nil
		}
	}
	for i := 0; i+1 < len(body.Content); i += 2 {
		k := body.Content[i]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		if _, ok := allowed[k.Value]; !ok {
			return errors.Newf("line %d: unknown top-level section %q", k.Line, k.Value)
		}
	}
	return nil
}

func rejectNulls(n *yaml.Node) error {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null" {
		return errors.Newf("line %d: explicit null is not allowed (use {} or [] for empty collections, or omit the field to keep defaults)", n.Line)
	}
	for _, c := range n.Content {
		if err := rejectNulls(c); err != nil {
			return err
		}
	}
	return nil
}

func cloneNode(n *yaml.Node) (*yaml.Node, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	if err := enc.Encode(n); err != nil {
		return nil, errors.Wrap(err, "config: clone section")
	}
	if err := enc.Close(); err != nil {
		return nil, errors.Wrap(err, "config: clone section")
	}
	var out yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&out); err != nil {
		return nil, errors.Wrap(err, "config: clone section")
	}
	// Encode 包了一层 DocumentNode；Decode 到 section 值时需要内容节点。
	if out.Kind == yaml.DocumentNode && len(out.Content) == 1 {
		return out.Content[0], nil
	}
	return &out, nil
}
