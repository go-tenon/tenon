package config

// LookupFunc 查找环境变量；第二个返回值表示是否已设置（空字符串仍算已设置）。
type LookupFunc func(key string) (string, bool)

// Option 配置 Document 的解析选项。
type Option func(*options)

type options struct {
	sections map[string]struct{}
	lookup   LookupFunc
}

func applyOptions(opts []Option) options {
	o := options{
		sections: make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithSections 声明允许的顶层配置段；文档中出现未声明段时解析失败。
// 至少声明一个段；空文档始终合法。
func WithSections(names ...string) Option {
	return func(o *options) {
		for _, n := range names {
			o.sections[n] = struct{}{}
		}
	}
}

// WithLookup 注入环境变量查找；Decode 展开 ${VAR} 时使用。
// 展开真实变量引用时需要此选项；转义的字面量直接保留。
func WithLookup(fn LookupFunc) Option {
	return func(o *options) {
		o.lookup = fn
	}
}
