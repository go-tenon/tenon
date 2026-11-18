package config

import (
	"bytes"
	"io"
	"os"
	"slices"

	"github.com/cockroachdb/errors"
	"go.yaml.in/yaml/v4"
)

// Load 依次读取 files 中的 YAML 配置文件并返回 T，T 通常是应用的根配置结构体。
// 后面的文件覆盖前面的文件：没写的字段保留，写了的字段覆盖，map 按键合并，slice 整体替换。
//
// 流程：SetDefaults 填充默认值 → 逐个文件展开 ${VAR} 并解码（未知字段报错）→
// 全部文件合并后 Validate 校验一次。${VAR} 引用的环境变量没有设置、也没有默认值时，只要后面的文件设置了
// 这一项就不算错误，例如 app.yaml 中的 key: ${APP_KEY} 由本机的 local.yaml 给出；否则返回错误。
func Load[T any](files ...string) (T, error) {
	var cfg T
	if len(files) == 0 {
		return cfg, errors.New("config: no configuration file")
	}
	setDefaults(&cfg)
	type pending struct {
		file string
		ref  unsetRef
		at   int // 所在文件的序号
	}
	var unset []pending
	paths := make([]map[string]yaml.Kind, len(files))
	for i, path := range files {
		var refs []unsetRef
		p, err := decodeFile(path, &cfg, &refs)
		if err != nil {
			var zero T
			return zero, errors.Wrapf(err, "config: %s", path)
		}
		paths[i] = p
		for _, r := range refs {
			unset = append(unset, pending{file: path, ref: r, at: i})
		}
	}
	for _, u := range unset {
		if !slices.ContainsFunc(paths[u.at+1:], func(p map[string]yaml.Kind) bool { return setsKey(p, u.ref.path) }) {
			var zero T
			return zero, errors.Wrapf(errors.Wrapf(&unsetError{name: u.ref.name}, "line %d", u.ref.line), "config: %s", u.file)
		}
	}
	if err := validate(&cfg); err != nil {
		var zero T
		return zero, errors.Wrap(err, "config")
	}
	return cfg, nil
}

// Default 返回只填充了默认值的 T：与 Load 读取一个空文件得到的值相同，跳过 Validate。
// 用于离线时需要一份可用配置的场合，例如 openapi、routes 命令构造应用的路由：
//
//	httpcmd.Commands(func() chi.Routes { return routes(config.Default[Config](), nil) })
func Default[T any]() T {
	var cfg T
	setDefaults(&cfg)
	return cfg
}

// decodeFile 展开 path 中的环境变量引用，并把内容解码到 dst 上。引用了未设置的变量的项不解码，记入 unset；
// 返回文件中出现的各项的键路径。
func decodeFile(path string, dst any, unset *[]unsetRef) (map[string]yaml.Kind, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	loader, err := yaml.NewLoader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var doc yaml.Node // 空文件或只有注释时 Load 返回 io.EOF，doc 保持零值
	if err := loader.Load(&doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	paths := map[string]yaml.Kind{}
	if doc.Kind == 0 {
		return paths, nil
	}
	if _, err := expandNode(&doc, "", os.LookupEnv, unset); err != nil {
		return nil, err
	}
	keyPaths(&doc, "", paths)
	return paths, doc.Load(dst, yaml.WithKnownFields())
}
