package settings

import (
	"context"
	"database/sql"
	"embed"
	"encoding"
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"time"
	"unicode"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
)

// secretPrefix 标记已由 crypt.Crypt 加密保存的 settings:"secret" 字段值。
const secretPrefix = "enc:v1:"

// ErrInvalid 表示租户 ID 为空或包含空白。
var ErrInvalid = errorx.Define(errorx.InvalidArgument, "settings.invalid")

//go:embed migrations
var migrationFiles embed.FS

// Migrations 返回 settings 表的迁移，供应用发布。
func Migrations() fs.FS {
	f, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return f
}

// row 是 settings 表中的一行：一组设置在一个范围内的值。
type row struct {
	bun.BaseModel `bun:"table:settings,alias:s"`

	Name      string          `bun:"name,pk"`
	Tenant    string          `bun:"tenant,pk"`
	Payload   json.RawMessage `bun:"payload,type:jsonb,notnull"`
	UpdatedAt time.Time       `bun:"updated_at,notnull"`
}

// Group 是一组设置，整组保存为 settings 表中的一行 JSON。T 是一个结构体，字段用 json 标签命名。
// 带有 settings:"secret" 标签的 string 字段在保存时由 crypt.Crypt 加密、读取时自动解密。
// Group 可以并发使用；Tenant 和 WithDB 返回副本，原来的 Group 保持原样。
type Group[T any] struct {
	db        bun.IDB
	name      string
	defaults  json.RawMessage
	shape     shape
	crypt     *crypt.Crypt
	hasSecret bool
	tenant    string
	scoped    bool // 调用过 Tenant
}

// Option 是 New 的选项。
type Option func(*options)

// options 保存 New 的配置项。
type options struct {
	crypt *crypt.Crypt
}

// WithCrypt 为设置组提供加解密器，用于透明加密带有 settings:"secret" 标签的字段（例如第三方 API 密钥）。
func WithCrypt(c *crypt.Crypt) Option {
	return func(o *options) { o.crypt = c }
}

// New 创建名为 name 的一组设置，defaults 是没有保存过时的值，也用于补上保存之后新增的字段。
// db 必须已执行 settings 迁移。
//
// 以下情况 panic，启动时就能发现：db 为 nil；name 为空或包含空白；T 不是结构体；
// T 或嵌套的结构体中有字段带 omitempty 或 omitzero（零值会从 JSON 中省略，读取时又被默认值覆盖）；
// T 中有非 string 字段带 settings:"secret"，或包含 settings:"secret" 字段但未通过 WithCrypt 传入加密器。
func New[T any](db bun.IDB, name string, defaults T, opts ...Option) *Group[T] {
	if db == nil {
		panic("settings: nil database")
	}
	if !validKey(name) {
		panic(errors.Newf("settings: invalid group name %q", name))
	}
	var cfg options
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct {
		panic(errors.Newf("settings: group %q: %v is not a struct", name, t))
	}
	s := shapeOf(t, name)
	hasSecret := s.hasSecret()
	if hasSecret && cfg.crypt == nil {
		panic(errors.Newf("settings: group %q has secret fields and requires WithCrypt", name))
	}
	b, err := json.Marshal(defaults)
	if err != nil {
		panic(errors.Wrapf(err, "settings: group %q: encode defaults", name))
	}
	return &Group[T]{db: db, name: name, defaults: b, shape: s, crypt: cfg.crypt, hasSecret: hasSecret}
}

// Name 返回这组设置的名称。
func (g *Group[T]) Name() string { return g.name }

// WithDB 返回使用 db（通常是 database.RunInTx 的事务）的副本，让 Save、Update 与其他写入（例如操作日志）一起提交。db 为 nil 时 panic。
func (g *Group[T]) WithDB(db bun.IDB) *Group[T] {
	if db == nil {
		panic("settings: nil database")
	}
	cp := *g
	cp.db = db
	return &cp
}

// Tenant 返回读写租户 id 的设置的副本。租户的设置与全局设置各自独立：租户没有保存过时使用默认值。
// id 为空或包含空白时，副本的每个方法都返回 ErrInvalid。
func (g *Group[T]) Tenant(id string) *Group[T] {
	cp := *g
	cp.tenant, cp.scoped = id, true
	return &cp
}

// Get 返回保存的设置；没有保存过时返回默认值。保存之后新增的字段使用默认值，嵌套的结构体逐个字段补上，
// 其他类型（切片、map、指针等）整体使用保存的值。已删除的字段被忽略，下次保存时去掉。
// 保存的值与 T 的类型不符时（例如字段从字符串改成了数字）返回错误，此时应当写迁移转换保存的 JSON。
func (g *Group[T]) Get(ctx context.Context) (T, error) {
	var v T
	if err := g.check(); err != nil {
		return v, err
	}
	var r row
	err := g.db.NewSelect().Model(&r).Column("payload").
		Where("name = ? AND tenant = ?", g.name, g.tenant).Scan(ctx)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return v, g.decode(nil, &v)
	case err != nil:
		return v, errors.Wrapf(err, "settings: get %q", g.name)
	}
	return v, g.decode(r.Payload, &v)
}

// Save 用 v 覆盖保存的设置。两个人同时编辑同一组设置时，后保存的覆盖先保存的；只修改部分字段时用 Update。
func (g *Group[T]) Save(ctx context.Context, v T) error {
	if err := g.check(); err != nil {
		return err
	}
	return g.save(ctx, g.db, v)
}

// Update 在事务中读出设置，交给 fn 修改后保存。并发的 Update 依次执行，每次修改都会保留。
// fn 返回错误时放弃保存，Update 返回这个错误。g 使用的数据库是 database.RunInTx 的事务时以保存点执行。
func (g *Group[T]) Update(ctx context.Context, fn func(*T) error) error {
	if err := g.check(); err != nil {
		return err
	}
	return database.RunInTx(ctx, g.db, nil, func(ctx context.Context, tx database.Tx) error {
		// 先写入默认值，保证有一行可以锁住；否则两个并发的 Update 都读到默认值，后写的覆盖先写的。
		_, err := tx.NewInsert().Model(&row{Name: g.name, Tenant: g.tenant, Payload: g.defaults, UpdatedAt: time.Now().UTC()}).
			On("CONFLICT (name, tenant) DO NOTHING").Exec(ctx)
		if err != nil {
			return errors.Wrapf(err, "settings: update %q", g.name)
		}
		var r row
		err = tx.NewSelect().Model(&r).Column("payload").
			Where("name = ? AND tenant = ?", g.name, g.tenant).For("UPDATE").Scan(ctx)
		if err != nil {
			return errors.Wrapf(err, "settings: update %q", g.name)
		}
		var v T
		if err := g.decode(r.Payload, &v); err != nil {
			return err
		}
		if err := fn(&v); err != nil {
			return err
		}
		return g.save(ctx, tx, v)
	})
}

// Reset 删除保存的设置，之后 Get 返回默认值。没有保存过时直接返回。
func (g *Group[T]) Reset(ctx context.Context) error {
	if err := g.check(); err != nil {
		return err
	}
	_, err := g.db.NewDelete().Model((*row)(nil)).
		Where("name = ? AND tenant = ?", g.name, g.tenant).Exec(ctx)
	return errors.Wrapf(err, "settings: reset %q", g.name)
}

// check 检查租户范围的设置组有合法的租户。
func (g *Group[T]) check() error {
	if g.scoped && !validKey(g.tenant) {
		return ErrInvalid.New(errorx.Public("field", "tenant"))
	}
	return nil
}

// save 把 v 编码为 JSON 保存；包含 settings:"secret" 的非空字符串字段自动加密后再落库。
func (g *Group[T]) save(ctx context.Context, db bun.IDB, v T) error {
	b, err := json.Marshal(v)
	if err != nil {
		return errors.Wrapf(err, "settings: encode %q", g.name)
	}
	if g.hasSecret {
		if b, err = transformSecrets(g.shape, b, g.encryptSecret); err != nil {
			return errors.Wrapf(err, "settings: encrypt %q", g.name)
		}
	}
	r := &row{Name: g.name, Tenant: g.tenant, Payload: b, UpdatedAt: time.Now().UTC()}
	_, err = db.NewInsert().Model(r).On("CONFLICT (name, tenant) DO UPDATE").
		Set("payload = EXCLUDED.payload").Set("updated_at = EXCLUDED.updated_at").Exec(ctx)
	return errors.Wrapf(err, "settings: save %q", g.name)
}

// decode 把保存的 JSON 解密（若含 secret 字段）并合并到默认值上，解码到 v。payload 为 nil 时只解码默认值。
func (g *Group[T]) decode(payload json.RawMessage, v *T) error {
	data := g.defaults
	if payload != nil {
		stored := payload
		if g.hasSecret {
			var err error
			if stored, err = transformSecrets(g.shape, stored, g.decryptSecret); err != nil {
				return errors.Wrapf(err, "settings: decrypt %q", g.name)
			}
		}
		var err error
		if data, err = merge(g.shape, g.defaults, stored); err != nil {
			return errors.Wrapf(err, "settings: decode %q", g.name)
		}
	}
	return errors.Wrapf(json.Unmarshal(data, v), "settings: decode %q", g.name)
}

// encryptSecret 加密非空的敏感字符串，空字符串原样保存。
func (g *Group[T]) encryptSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	return secretPrefix + g.crypt.Encrypt(plain), nil
}

// decryptSecret 解密保存的敏感字符串，空字符串原样返回；非空但未带密文前缀的值视为错误。
func (g *Group[T]) decryptSecret(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	ct, ok := strings.CutPrefix(stored, secretPrefix)
	if !ok {
		return "", errors.New("unencrypted secret value")
	}
	return g.crypt.Decrypt(ct)
}

// shape 是 T 中逐个字段合并和加解密的结构描述。
// 没有自定义解码的结构体逐个字段合并（fields 非 nil）；secret 为 true 表示带 settings:"secret" 的字符串字段。
type shape struct {
	fields map[string]shape
	secret bool
}

// hasSecret 报告 s 或其嵌套字段中是否包含敏感字段。
func (s shape) hasSecret() bool {
	if s.secret {
		return true
	}
	for _, child := range s.fields {
		if child.hasSecret() {
			return true
		}
	}
	return false
}

var (
	jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// shapeOf 返回结构体类型 t 的字段结构，用于合并保存的值和默认值。
func shapeOf(t reflect.Type, path string) shape {
	if t.Kind() != reflect.Struct {
		return shape{}
	}
	if p := reflect.PointerTo(t); p.Implements(jsonUnmarshaler) || p.Implements(textUnmarshaler) {
		return shape{} // 例如 time.Time
	}
	s := shape{fields: map[string]shape{}}
	addFields(s.fields, t, path)
	return s
}

// addFields 把结构体 t 的字段按 JSON 路径加入 fields。
func addFields(fields map[string]shape, t reflect.Type, path string) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			addFields(fields, f.Type, path) // 嵌入的结构体，字段提升到外层对象
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		for o := range strings.SplitSeq(opts, ",") {
			if o == "omitempty" || o == "omitzero" {
				panic(errors.Newf("settings: %s.%s: %s is not allowed, the zero value would read back as the default", path, name, o))
			}
		}
		secret := hasSecretTag(f.Tag.Get("settings"))
		if secret && f.Type.Kind() != reflect.String {
			panic(errors.Newf("settings: %s.%s: settings:\"secret\" is only allowed on string fields", path, name))
		}
		if _, ok := fields[name]; !ok {
			if secret {
				fields[name] = shape{secret: true}
			} else {
				fields[name] = shapeOf(f.Type, path+"."+name)
			}
		}
	}
}

// hasSecretTag 报告 settings 结构体标签中是否包含 secret。
func hasSecretTag(tag string) bool {
	for item := range strings.SplitSeq(tag, ",") {
		if strings.TrimSpace(item) == "secret" {
			return true
		}
	}
	return false
}

// transformSecrets 按 s 递归转换 raw 中所有敏感字段的字符串值。
func transformSecrets(s shape, raw json.RawMessage, fn func(string) (string, error)) (json.RawMessage, error) {
	if s.secret {
		var val string
		if err := json.Unmarshal(raw, &val); err != nil {
			return nil, err
		}
		next, err := fn(val)
		if err != nil {
			return nil, err
		}
		return json.Marshal(next)
	}
	if s.fields == nil || !s.hasSecret() {
		return raw, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return raw, nil
	}
	for k, child := range s.fields {
		if !child.hasSecret() {
			continue
		}
		v, ok := obj[k]
		if !ok || string(v) == "null" {
			continue
		}
		next, err := transformSecrets(child, v, fn)
		if err != nil {
			return nil, errors.Wrapf(err, "field %s", k)
		}
		obj[k] = next
	}
	return json.Marshal(obj)
}

// merge 返回 stored 合并到 def 上的 JSON：s.fields 不为 nil 且两边都是对象时逐个字段合并，否则使用 stored。
func merge(s shape, def, stored json.RawMessage) (json.RawMessage, error) {
	if s.fields == nil {
		return stored, nil
	}
	var d, st map[string]json.RawMessage
	if json.Unmarshal(def, &d) != nil || json.Unmarshal(stored, &st) != nil || d == nil || st == nil {
		return stored, nil // 不是对象，交给解码报告类型错误
	}
	for k, v := range st {
		if dv, ok := d[k]; ok {
			m, err := merge(s.fields[k], dv, v)
			if err != nil {
				return nil, err
			}
			v = m
		}
		d[k] = v
	}
	return json.Marshal(d)
}

// validKey 报告名称非空且不含空白。
func validKey(s string) bool {
	return s != "" && !strings.ContainsFunc(s, unicode.IsSpace)
}
