package feature

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

// ErrInvalid 表示设置不合法，例如开关不在目录中、给租户或主体设置比例、比例不在 0 到 100 之间。
var ErrInvalid = errorx.Define(errorx.InvalidArgument, "feature.invalid")

//go:embed migrations
var migrationFiles embed.FS

// Migrations 返回 Store 使用的 feature_settings 表的迁移，供应用发布。
func Migrations() fs.FS {
	f, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return f
}

// Flag 是在代码中声明的一个开关。
type Flag struct {
	Name        string                                               // 开关名，例如 articles.new-editor，保存在设置中。
	Description string                                               // 在后台页面中显示。
	ByTenant    bool                                                 // ByTenant 为 true 时按租户放量，同一租户的人结果一致；否则按主体放量。
	Default     func(ctx context.Context, sub identity.Subject) bool // 在没有任何设置命中时决定结果，nil 表示关闭。它可能在每次检查时调用，应当足够快。
}

// Catalog 是应用声明的全部开关。Set 只接受目录中的开关，Prune 删除不在目录中的开关的设置。
type Catalog []Flag

// Scope 是一条设置的范围：全员（Everyone）、某个租户（Tenant）或某个主体（Subject）。
type Scope struct {
	kind   scopeKind
	tenant string
	ref    identity.Ref
}

// scopeKind 是开关设置的作用范围的种类。
type scopeKind uint8

const (
	everyone scopeKind = iota
	tenantScope
	subjectScope
)

// Everyone 是全员的设置。
var Everyone = Scope{}

// Tenant 是租户 id 的设置。id 为空时 Set 和 Clear 返回 ErrInvalid。
func Tenant(id string) Scope { return Scope{kind: tenantScope, tenant: id} }

// Subject 是主体 ref 的设置，在主体所属的每个租户中都生效。ref 不合法（见 identity.Ref.Validate）时
// Set 和 Clear 返回 ErrInvalid。identity.System 主体不适用主体的设置。
func Subject(ref identity.Ref) Scope { return Scope{kind: subjectScope, ref: ref} }

// Tenant 返回租户的 ID；不是租户的设置时返回 ""。
func (s Scope) Tenant() string { return s.tenant }

// Subject 返回主体；不是主体的设置时返回零值。
func (s Scope) Subject() identity.Ref { return s.ref }

// Mode 是设置的类型。
type Mode string

// 设置的类型：开启、关闭、强制开启、强制关闭、按比例开启。
const (
	ModeOn       Mode = "on"
	ModeOff      Mode = "off"
	ModeForceOn  Mode = "force_on"  // 只用于全员，覆盖租户和主体的单独设置
	ModeForceOff Mode = "force_off" // 只用于全员，覆盖租户和主体的单独设置（紧急关闭）
	ModePercent  Mode = "percent"   // 只用于全员
)

// State 是一条设置的值。
type State struct {
	Mode    Mode
	Percent int // Mode 为 ModePercent 时有效，0 到 100
}

var (
	// On 开启。设给全员时作为默认状态，租户和主体的单独设置优先于它。
	On = State{Mode: ModeOn}
	// Off 关闭。设给全员时作为默认状态，租户和主体的单独设置优先于它。
	Off = State{Mode: ModeOff}
	// ForceOn 强制全员开启，覆盖租户和主体的单独设置，只能设给全员。
	ForceOn = State{Mode: ModeForceOn}
	// ForceOff 强制全员关闭，覆盖租户和主体的单独设置，用作紧急关闭，只能设给全员。
	ForceOff = State{Mode: ModeForceOff}
)

// Percent 让 n% 的主体或租户开启，只能设给全员。租户和主体的单独设置优先于它。
// Percent(100) 包括没有主体或租户的请求，例如未登录的访客，以及 identity.System；小于 100 时它们不在比例内。
func Percent(n int) State { return State{Mode: ModePercent, Percent: n} }

// Setting 是一条设置。
type Setting struct {
	Scope     Scope
	State     State
	UpdatedAt time.Time
}

// row 是 feature_settings 表中的一行：一个开关在一个范围内的设置。
type row struct {
	bun.BaseModel `bun:"table:feature_settings,alias:fs"`

	Name        string    `bun:"name,pk"`
	Tenant      string    `bun:"tenant,pk"`
	SubjectType string    `bun:"subject_type,pk"`
	SubjectID   string    `bun:"subject_id,pk"`
	Mode        Mode      `bun:"mode,notnull"`
	Percent     int       `bun:"percent,notnull"`
	UpdatedAt   time.Time `bun:"updated_at,notnull"`
}

// Store 是基于 Bun 的开关设置存储，可以并发使用。
type Store struct {
	db      bun.IDB
	catalog Catalog
	names   map[string]bool
}

// NewStore 创建存储。db 必须已执行 feature 迁移。
// 目录中的开关名为空、重复或包含空白时 panic。
func NewStore(db bun.IDB, catalog Catalog) *Store {
	if db == nil {
		panic("feature: nil database")
	}
	names := make(map[string]bool, len(catalog))
	for _, f := range catalog {
		if !validKey(f.Name) || names[f.Name] {
			panic(errors.Newf("feature: invalid or duplicate flag name %q", f.Name))
		}
		names[f.Name] = true
	}
	return &Store{db: db, catalog: slices.Clone(catalog), names: names}
}

// WithDB 返回使用 db（通常是业务事务）的副本，用于让 Set、Clear 与其他写入（例如操作日志）一起提交。
func (s *Store) WithDB(db bun.IDB) *Store {
	cp := *s
	cp.db = db
	return &cp
}

// Catalog 返回开关目录，供后台页面列出全部开关。
func (s *Store) Catalog() Catalog { return slices.Clone(s.catalog) }

// Active 报告 f 对 sub 是否开启，判断顺序见包文档。读取设置出错时记录 Error 日志，返回 f.Default 的结果。
func (s *Store) Active(ctx context.Context, sub identity.Subject, f Flag) bool {
	rows, err := s.load(ctx, sub, f.Name)
	if err != nil {
		slog.ErrorContext(ctx, "feature: load settings, using default", "flag", f.Name, "error", err)
	}
	return evaluate(ctx, sub, f, rows)
}

// For 一次读出 sub 相关的全部设置，用于在一个请求中检查多个开关。读取出错时记录 Error 日志，
// 返回的 Flags 只使用各开关的 Default。Flags 在调用 Default 时使用 ctx，只在本次请求中使用。
func (s *Store) For(ctx context.Context, sub identity.Subject) Flags {
	rows, err := s.load(ctx, sub, "")
	if err != nil {
		slog.ErrorContext(ctx, "feature: load settings, using defaults", "error", err)
	}
	byName := make(map[string][]row)
	for _, r := range rows {
		byName[r.Name] = append(byName[r.Name], r)
	}
	return Flags{ctx: ctx, sub: sub, rows: byName}
}

// Flags 是 For 读出的一个主体的设置。
type Flags struct {
	ctx  context.Context
	sub  identity.Subject
	rows map[string][]row
}

// Active 报告 f 是否开启，结果与 Store.Active 相同。
func (f Flags) Active(flag Flag) bool { return evaluate(f.ctx, f.sub, flag, f.rows[flag.Name]) }

// load 读取 sub 能命中的设置：全员、sub 的租户和 sub 本人；name 不为空时只读这个开关。
func (s *Store) load(ctx context.Context, sub identity.Subject, name string) ([]row, error) {
	var rows []row
	q := s.db.NewSelect().Model(&rows)
	if name != "" {
		q = q.Where("name = ?", name)
	}
	q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
		q = q.WhereOr("tenant = '' AND subject_id = ''")
		if sub.Tenant != "" {
			q = q.WhereOr("tenant = ? AND subject_id = ''", sub.Tenant)
		}
		if ref := subjectOf(sub); !ref.IsZero() {
			q = q.WhereOr("tenant = '' AND subject_type = ? AND subject_id = ?", ref.Type, ref.ID)
		}
		return q
	})
	return rows, errors.Wrap(q.Scan(ctx), "feature: load settings")
}

// subjectOf 返回用于主体设置和按主体放量的主体：identity.System（例如后台任务）不适用，返回零值。
func subjectOf(sub identity.Subject) identity.Ref {
	if sub.Type == identity.System {
		return identity.Ref{}
	}
	return sub.Ref()
}

// evaluate 按全员强制覆盖、主体、租户、全员默认/比例的顺序判断开关 f 对 sub 是否开启；都没有设置时使用 f.Default。
func evaluate(ctx context.Context, sub identity.Subject, f Flag, rows []row) bool {
	var everyone, tenant, subject *row
	for i := range rows {
		r := &rows[i]
		switch {
		case r.Tenant == "" && r.SubjectID == "":
			everyone = r
		case r.SubjectID == "":
			tenant = r
		default:
			subject = r
		}
	}
	switch {
	case everyone != nil && (everyone.Mode == ModeForceOn || everyone.Mode == ModeForceOff):
		return everyone.Mode == ModeForceOn
	case subject != nil:
		return subject.Mode == ModeOn
	case tenant != nil:
		return tenant.Mode == ModeOn
	case everyone != nil:
		switch everyone.Mode {
		case ModeOn:
			return true
		case ModeOff:
			return false
		default:
			unit := subjectOf(sub).String()
			if f.ByTenant {
				unit = sub.Tenant
			}
			return inRollout(f.Name, unit, everyone.Percent)
		}
	case f.Default != nil:
		return f.Default(ctx, sub)
	}
	return false
}

// inRollout 报告 unit 是否落在 name 的前 percent% 中。哈希包含开关名，不同开关选中的人不同。
func inRollout(name, unit string, percent int) bool {
	if percent >= 100 {
		return true
	}
	if unit == "" || percent <= 0 {
		return false
	}
	h := sha256.Sum256([]byte(name + "\x00" + unit))
	return int(binary.BigEndian.Uint32(h[:4])%100) < percent
}

// Set 保存 f 在 scope 上的设置，已有设置时覆盖。f 不在目录中、租户 ID 为空、主体不合法、
// 给租户或主体设置比例、比例不在 0 到 100 之间时返回 ErrInvalid。
func (s *Store) Set(ctx context.Context, f Flag, scope Scope, state State) error {
	if !s.names[f.Name] {
		return ErrInvalid.New(errorx.Public("field", "flag"))
	}
	if err := checkScope(scope); err != nil {
		return err
	}
	switch {
	case state.Mode == ModeOn || state.Mode == ModeOff:
		state.Percent = 0
	case (state.Mode == ModeForceOn || state.Mode == ModeForceOff) && scope == Everyone:
		state.Percent = 0
	case state.Mode == ModePercent && state.Percent >= 0 && state.Percent <= 100 && scope == Everyone:
	default:
		return ErrInvalid.New(errorx.Public("field", "state"))
	}
	r := &row{
		Name: f.Name, Tenant: scope.tenant, SubjectType: string(scope.ref.Type), SubjectID: scope.ref.ID,
		Mode: state.Mode, Percent: state.Percent, UpdatedAt: time.Now().UTC(),
	}
	_, err := s.db.NewInsert().Model(r).On("CONFLICT (name, tenant, subject_type, subject_id) DO UPDATE").Set("mode = ?", r.Mode).Set("percent = ?", r.Percent).Set("updated_at = ?", r.UpdatedAt).Exec(ctx)
	return errors.Wrap(err, "feature: set")
}

// Clear 删除 f 在 scope 上的设置，恢复按后面的规则判断。没有这条设置时直接返回。
func (s *Store) Clear(ctx context.Context, f Flag, scope Scope) error {
	if err := checkScope(scope); err != nil {
		return err
	}
	_, err := s.db.NewDelete().Model((*row)(nil)).
		Where("name = ?", f.Name).Where("tenant = ?", scope.tenant).
		Where("subject_type = ? AND subject_id = ?", scope.ref.Type, scope.ref.ID).Exec(ctx)
	return errors.Wrap(err, "feature: clear")
}

// Erase 在 db 中删除为主体 ref 单独做的全部开关设置（Subject 作用域），用于注销账号。
// db 通常是注销所在的事务，签名与 privacy.Source 的 Erase 相同。
func Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error {
	if err := ref.Validate(); err != nil {
		return errors.Wrap(err, "feature")
	}
	_, err := db.NewDelete().Model((*row)(nil)).
		Where("subject_type = ? AND subject_id = ?", ref.Type, ref.ID).Exec(ctx)
	return errors.Wrap(err, "feature: erase")
}

// Settings 返回 f 的全部设置：全员在前，然后是各租户、各主体；租户按 ID 排序，主体按类型和 ID 排序。
func (s *Store) Settings(ctx context.Context, f Flag) ([]Setting, error) {
	var rows []row
	err := s.db.NewSelect().Model(&rows).Where("name = ?", f.Name).
		OrderExpr("subject_type ASC, subject_id ASC, tenant ASC").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "feature: list settings")
	}
	out := make([]Setting, len(rows))
	for i, r := range rows {
		out[i] = Setting{
			Scope:     scopeOf(r),
			State:     State{Mode: r.Mode, Percent: r.Percent},
			UpdatedAt: r.UpdatedAt,
		}
	}
	return out, nil
}

// Prune 删除不在目录中的开关的全部设置，返回删除的条数。开关从代码中删除之后调用。
func (s *Store) Prune(ctx context.Context) (int, error) {
	q := s.db.NewDelete().Model((*row)(nil))
	if len(s.names) == 0 {
		q = q.Where("1 = 1")
	} else {
		names := make([]string, 0, len(s.names))
		for n := range s.names {
			names = append(names, n)
		}
		q = q.Where("name NOT IN (?)", bun.List(names))
	}
	res, err := q.Exec(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "feature: prune")
	}
	n, err := res.RowsAffected()
	return int(n), errors.Wrap(err, "feature: prune")
}

// checkScope 校验范围 scope 的租户或主体。
func checkScope(scope Scope) error {
	switch {
	case scope.kind == tenantScope && !validKey(scope.tenant),
		scope.kind == subjectScope && scope.ref.Validate() != nil:
		return ErrInvalid.New(errorx.Public("field", "scope"))
	}
	return nil
}

// scopeOf 返回行 r 的作用范围。
func scopeOf(r row) Scope {
	switch {
	case r.SubjectID != "":
		return Subject(identity.Ref{Type: identity.SubjectType(r.SubjectType), ID: r.SubjectID})
	case r.Tenant != "":
		return Tenant(r.Tenant)
	}
	return Everyone
}

// validKey 报告名称非空且不含空白。
func validKey(s string) bool {
	return s != "" && !strings.ContainsFunc(s, unicode.IsSpace)
}
