package savedview

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

const (
	defaultMaxViews   = 50
	defaultMaxNameLen = 50
)

var (
	// ErrNotFound 表示视图不存在、属于其他租户或业务对象，或属于其他主体的个人视图。
	ErrNotFound = errorx.Define(errorx.NotFound, "savedview.not_found")
	// ErrForbidden 表示调用者没有权限管理该视图。
	ErrForbidden = errorx.Define(errorx.PermissionDenied, "savedview.forbidden")
	// ErrNameTaken 表示同一租户、对象和作用域下已存在同名视图（忽略大小写）。
	ErrNameTaken = errorx.Define(errorx.Conflict, "savedview.name_taken")
	// ErrInvalidName 表示视图名称为空或超出允许的最大字符数。
	ErrInvalidName = errorx.Define(errorx.InvalidArgument, "savedview.invalid_name")
	// ErrInvalidScope 表示视图作用域取值不合法。
	ErrInvalidScope = errorx.Define(errorx.InvalidArgument, "savedview.invalid_scope")
	// ErrLimitExceeded 表示该作用域下的视图数量已达到上限。
	ErrLimitExceeded = errorx.Define(errorx.Conflict, "savedview.limit_exceeded")
)

//go:embed migrations
var migrationFiles embed.FS

// Migrations 返回 saved_views 表的迁移，供应用发布。
func Migrations() fs.FS {
	f, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return f
}

// Scope 是视图的作用域：共享或个人。
type Scope string

const (
	// ScopeShared 表示租户（或单租户应用）内全体成员可见的共享视图。
	ScopeShared Scope = "shared"
	// ScopePersonal 表示仅归属主体本人可见与管理的个人视图。
	ScopePersonal Scope = "personal"
)

// Scopes 是视图的全部作用域，显示文字来自翻译文件中的 savedview.scope.<作用域>。
var Scopes = enum.New("savedview.scope", enum.Item(ScopeShared), enum.Item(ScopePersonal))

// Schema 让 OpenAPI 中的 Scope 只接受 Scopes 中的取值，并附带显示文字。
func (Scope) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Scopes) }

// View 是一条已保存的视图记录，Config 为业务包声明的配置结构体 T。
type View[T any] struct {
	ID        string       // 视图 ID（UUIDv7）
	Tenant    string       // 所属租户 ID，单租户应用为空字符串
	Target    string       // 业务对象标识
	Scope     Scope        // 作用域：shared 或 personal
	Owner     identity.Ref // 个人视图归属主体；共享视图为零值
	Name      string       // 视图名称
	Config    T            // 视图保存的配置
	Position  int          // 同作用域下的展示顺序，值越小越靠前
	CreatedBy identity.Ref // 创建者主体；系统主体创建时为零值
	CreatedAt time.Time    // 创建时间
	UpdatedAt time.Time    // 最近修改时间
}

// Options 是 Group 的可选配置。
type Options struct {
	MaxShared    int                         // 每个租户下该对象最多创建的共享视图数；0 表示默认 50，负数表示不限
	MaxPersonal  int                         // 每个主体在每个租户下最多创建的个人视图数；0 表示默认 50，负数表示不限
	MaxNameLen   int                         // 视图名称允许的最大字符数（按 rune 计）；0 表示默认 50
	ManageShared func(identity.Subject) bool // 报告主体是否有权管理共享视图；nil 时仅 identity.System 可管理
}

// CreateInput 是新建视图的输入。
type CreateInput[T any] struct {
	Scope    Scope  // 作用域：ScopeShared 或 ScopePersonal
	Name     string // 视图名称，自动去除首尾空白
	Config   T      // 视图保存的配置
	Position *int   // 可选展示顺序；为空时排在当前作用域末尾
}

// UpdateInput 是修改视图的输入（作用域创建后不可修改）。
type UpdateInput[T any] struct {
	Name     string // 新的视图名称，自动去除首尾空白
	Config   T      // 新的视图配置
	Position *int   // 可选新的展示顺序；为空时保持原值
}

// row 是 saved_views 表中的一行。
type row struct {
	bun.BaseModel `bun:"table:saved_views,alias:sv"`

	ID string `bun:"id,pk"`
	database.Timestamps
	Tenant        string          `bun:"tenant,notnull"`
	Target        string          `bun:"target,notnull"`
	OwnerType     string          `bun:"owner_type,notnull"`
	OwnerID       string          `bun:"owner_id,notnull"`
	Name          string          `bun:"name,notnull"`
	Config        json.RawMessage `bun:"config,type:jsonb,notnull"`
	Position      int             `bun:"position,notnull"`
	CreatedByType string          `bun:"created_by_type,notnull"`
	CreatedByID   string          `bun:"created_by_id,notnull"`
}

// Group 管理某一个业务对象（target）的已保存视图。T 为该对象保存的配置结构体，通过 JSON 读写。
// Group 可以并发使用；WithDB 返回使用指定数据库或事务的新副本，原 Group 保持不变。
type Group[T any] struct {
	db           bun.IDB
	target       string
	maxShared    int
	maxPersonal  int
	maxNameLen   int
	manageShared func(identity.Subject) bool
}

// New 创建业务对象 target 的视图存储。db 必须已执行 savedview 迁移。
// db 为 nil 或 target 为空、包含空白时 panic，启动时即可暴露配置错误。
func New[T any](db bun.IDB, target string, opts ...Options) *Group[T] {
	if db == nil {
		panic("savedview: nil database")
	}
	if !validKey(target) {
		panic(errors.Newf("savedview: invalid target %q", target))
	}
	var opt Options
	if len(opts) > 0 {
		opt = opts[0]
	}
	maxShared := opt.MaxShared
	if maxShared == 0 {
		maxShared = defaultMaxViews
	}
	maxPersonal := opt.MaxPersonal
	if maxPersonal == 0 {
		maxPersonal = defaultMaxViews
	}
	maxNameLen := opt.MaxNameLen
	if maxNameLen <= 0 {
		maxNameLen = defaultMaxNameLen
	}
	return &Group[T]{
		db:           db,
		target:       target,
		maxShared:    maxShared,
		maxPersonal:  maxPersonal,
		maxNameLen:   maxNameLen,
		manageShared: opt.ManageShared,
	}
}

// Target 返回该视图组对应的业务对象标识。
func (g *Group[T]) Target() string { return g.target }

// WithDB 返回使用 db（通常是 database.RunInTx 的事务）的副本，用于让视图写入与操作日志等业务操作在同一事务中提交。
func (g *Group[T]) WithDB(db bun.IDB) *Group[T] {
	cp := *g
	cp.db = db
	return &cp
}

// CanManageShared 报告主体 sub 是否有权创建、修改、删除或排序共享视图。
// identity.System 始终拥有管理权限。
func (g *Group[T]) CanManageShared(sub identity.Subject) bool {
	if sub.Type == identity.System {
		return true
	}
	return g.manageShared != nil && g.manageShared(sub)
}

// CanManage 报告主体 sub 是否有权修改或删除视图 v。
func (g *Group[T]) CanManage(sub identity.Subject, v View[T]) bool {
	if v.Scope == ScopeShared {
		return g.CanManageShared(sub)
	}
	ref := ownerOf(sub)
	return !ref.IsZero() && v.Owner == ref
}

// List 返回 sub 在其所在租户下可见的全部视图。scope 为空时返回共享与个人视图（共享在前，个人在后），
// 传 ScopeShared 或 ScopePersonal 时只返回对应作用域；同作用域按 position、created_at、id 升序排列。
func (g *Group[T]) List(ctx context.Context, sub identity.Subject, scope Scope) ([]View[T], error) {
	q := g.db.NewSelect().Model((*row)(nil)).
		Where("sv.tenant = ? AND sv.target = ?", sub.Tenant, g.target)
	ref := ownerOf(sub)
	switch scope {
	case "":
		if !ref.IsZero() {
			q = q.Where("(sv.owner_id = '' OR (sv.owner_type = ? AND sv.owner_id = ?))", ref.Type, ref.ID)
		} else {
			q = q.Where("sv.owner_id = ''")
		}
	case ScopeShared:
		q = q.Where("sv.owner_id = ''")
	case ScopePersonal:
		if ref.IsZero() {
			return []View[T]{}, nil
		}
		q = q.Where("sv.owner_type = ? AND sv.owner_id = ?", ref.Type, ref.ID)
	default:
		return nil, ErrInvalidScope.New(errorx.Field("scope"))
	}
	var rows []row
	err := q.Model(&rows).
		OrderExpr("(CASE WHEN sv.owner_id = '' THEN 0 ELSE 1 END) ASC, sv.position ASC, sv.created_at ASC, sv.id ASC").
		Scan(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "savedview: list %q", g.target)
	}
	out := make([]View[T], 0, len(rows))
	for i := range rows {
		v, err := g.decode(&rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Get 读取 sub 在当前租户下可见的视图 id；id 不是合法 UUID、不存在、属于其他租户或对象，
// 或属于其他主体的个人视图时均返回 ErrNotFound。
func (g *Group[T]) Get(ctx context.Context, sub identity.Subject, id string) (View[T], error) {
	r, err := g.loadVisible(ctx, g.db, sub, id, false)
	if err != nil {
		return View[T]{}, err
	}
	return g.decode(r)
}

// Create 为 sub 在当前租户下新建一条共享或个人视图。
// 共享视图校验 CanManageShared，个人视图要求非系统主体；名称去首尾空白并校验长度、上限和同作用域唯一性。
func (g *Group[T]) Create(ctx context.Context, sub identity.Subject, in CreateInput[T]) (View[T], error) {
	var owner identity.Ref
	limit := g.maxShared
	switch in.Scope {
	case ScopeShared:
		if !g.CanManageShared(sub) {
			return View[T]{}, ErrForbidden.New()
		}
	case ScopePersonal:
		owner = ownerOf(sub)
		if owner.IsZero() {
			return View[T]{}, ErrForbidden.New()
		}
		limit = g.maxPersonal
	default:
		return View[T]{}, ErrInvalidScope.New(errorx.Field("scope"))
	}

	name, err := g.validateName(in.Name)
	if err != nil {
		return View[T]{}, err
	}
	raw, err := json.Marshal(in.Config)
	if err != nil {
		return View[T]{}, errors.Wrapf(err, "savedview: encode config for %q", g.target)
	}

	creator := ownerOf(sub)
	var inserted *row
	err = database.RunInTx(ctx, g.db, nil, func(ctx context.Context, tx database.Tx) error {
		// 检查同作用域数量上限并计算默认末尾位置。
		var stats struct {
			Count int  `bun:"cnt"`
			Max   *int `bun:"max_pos"`
		}
		sq := tx.NewSelect().TableExpr("saved_views AS sv").
			ColumnExpr("count(*) AS cnt").
			ColumnExpr("max(sv.position) AS max_pos").
			Where("sv.tenant = ? AND sv.target = ?", sub.Tenant, g.target)
		if owner.IsZero() {
			sq = sq.Where("sv.owner_id = ''")
		} else {
			sq = sq.Where("sv.owner_type = ? AND sv.owner_id = ?", owner.Type, owner.ID)
		}
		if err := sq.Scan(ctx, &stats); err != nil {
			return errors.Wrapf(err, "savedview: count %q", g.target)
		}
		if limit > 0 && stats.Count >= limit {
			return ErrLimitExceeded.New(errorx.Public("limit", limit))
		}
		pos := 0
		if in.Position != nil {
			pos = *in.Position
		} else if stats.Max != nil {
			pos = *stats.Max + 1
		}

		r := &row{
			ID:            uuid.Must(uuid.NewV7()).String(),
			Tenant:        sub.Tenant,
			Target:        g.target,
			OwnerType:     string(owner.Type),
			OwnerID:       owner.ID,
			Name:          name,
			Config:        raw,
			Position:      pos,
			CreatedByType: string(creator.Type),
			CreatedByID:   creator.ID,
		}
		if _, err := tx.NewInsert().Model(r).Exec(ctx); err != nil {
			if isNameConflict(err) {
				return ErrNameTaken.New(errorx.Field("name"), errorx.Public("name", name))
			}
			return errors.Wrapf(err, "savedview: insert %q", g.target)
		}
		inserted = r
		return nil
	})
	if err != nil {
		return View[T]{}, err
	}
	return g.decode(inserted)
}

// Update 修改视图 id 的名称、配置和可选顺序；作用域创建后不可修改。
// 视图不存在或不可见时返回 ErrNotFound，无权修改时返回 ErrForbidden，重名时返回 ErrNameTaken。
func (g *Group[T]) Update(ctx context.Context, sub identity.Subject, id string, in UpdateInput[T]) (View[T], error) {
	name, err := g.validateName(in.Name)
	if err != nil {
		return View[T]{}, err
	}
	raw, err := json.Marshal(in.Config)
	if err != nil {
		return View[T]{}, errors.Wrapf(err, "savedview: encode config for %q", g.target)
	}

	var updated *row
	err = database.RunInTx(ctx, g.db, nil, func(ctx context.Context, tx database.Tx) error {
		r, err := g.loadVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		if !g.canManageRow(sub, r) {
			return ErrForbidden.New()
		}
		r.Name = name
		r.Config = raw
		if in.Position != nil {
			r.Position = *in.Position
		}
		if _, err := tx.NewUpdate().Model(r).Column("name", "config", "position", "updated_at").WherePK().Exec(ctx); err != nil {
			if isNameConflict(err) {
				return ErrNameTaken.New(errorx.Field("name"), errorx.Public("name", name))
			}
			return errors.Wrapf(err, "savedview: update %q", g.target)
		}
		updated = r
		return nil
	})
	if err != nil {
		return View[T]{}, err
	}
	return g.decode(updated)
}

// Delete 删除视图 id 并返回被删除的视图。不存在或不可见时返回 ErrNotFound，无权删除时返回 ErrForbidden。
func (g *Group[T]) Delete(ctx context.Context, sub identity.Subject, id string) (View[T], error) {
	var deleted View[T]
	err := database.RunInTx(ctx, g.db, nil, func(ctx context.Context, tx database.Tx) error {
		r, err := g.loadVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		if !g.canManageRow(sub, r) {
			return ErrForbidden.New()
		}
		v, err := g.decode(r)
		if err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model(r).WherePK().Exec(ctx); err != nil {
			return errors.Wrapf(err, "savedview: delete %q", g.target)
		}
		deleted = v
		return nil
	})
	return deleted, err
}

// Reorder 按 ids 的顺序更新 scope 下视图的 position（从 0 递增）；不在 ids 中但在该作用域下的其余视图
// 按原有顺序排在后面。ids 中不属于当前租户、对象和作用域的 ID 会返回 ErrNotFound。
func (g *Group[T]) Reorder(ctx context.Context, sub identity.Subject, scope Scope, ids []string) error {
	var owner identity.Ref
	switch scope {
	case ScopeShared:
		if !g.CanManageShared(sub) {
			return ErrForbidden.New()
		}
	case ScopePersonal:
		owner = ownerOf(sub)
		if owner.IsZero() {
			return ErrForbidden.New()
		}
	default:
		return ErrInvalidScope.New(errorx.Field("scope"))
	}
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return ErrNotFound.New()
		}
	}

	return database.RunInTx(ctx, g.db, nil, func(ctx context.Context, tx database.Tx) error {
		var rows []row
		q := tx.NewSelect().Model(&rows).
			Where("sv.tenant = ? AND sv.target = ?", sub.Tenant, g.target).
			OrderExpr("sv.position ASC, sv.created_at ASC, sv.id ASC").
			For("UPDATE")
		if owner.IsZero() {
			q = q.Where("sv.owner_id = ''")
		} else {
			q = q.Where("sv.owner_type = ? AND sv.owner_id = ?", owner.Type, owner.ID)
		}
		if err := q.Scan(ctx); err != nil {
			return errors.Wrapf(err, "savedview: load for reorder %q", g.target)
		}
		existing := make(map[string]bool, len(rows))
		for i := range rows {
			existing[rows[i].ID] = true
		}
		ordered := make([]string, 0, len(rows))
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if !existing[id] {
				return ErrNotFound.New()
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			ordered = append(ordered, id)
		}
		for i := range rows {
			if !seen[rows[i].ID] {
				ordered = append(ordered, rows[i].ID)
			}
		}

		now := time.Now().UTC()
		for pos, id := range ordered {
			if _, err := tx.NewUpdate().TableExpr("saved_views").
				Set("position = ?", pos).
				Set("updated_at = ?", now).
				Where("id = ?", id).Exec(ctx); err != nil {
				return errors.Wrapf(err, "savedview: reorder %q", g.target)
			}
		}
		return nil
	})
}

// DeleteByOwner 删除主体 owner 在租户 tenant 下全部对象的个人视图（共享视图保留）。
// 适合在成员被移出租户的同一事务中调用。
func DeleteByOwner(ctx context.Context, db bun.IDB, tenant string, owner identity.Ref) error {
	if err := owner.Validate(); err != nil {
		return errors.Wrap(err, "savedview")
	}
	_, err := db.NewDelete().Model((*row)(nil)).
		Where("tenant = ? AND owner_type = ? AND owner_id = ?", tenant, owner.Type, owner.ID).Exec(ctx)
	return errors.Wrap(err, "savedview: delete by owner")
}

// Erase 删除主体 ref 在全部租户与对象下的个人视图，签名与 privacy.Source.Erase 一致。
func Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error {
	if err := ref.Validate(); err != nil {
		return errors.Wrap(err, "savedview")
	}
	_, err := db.NewDelete().Model((*row)(nil)).
		Where("owner_type = ? AND owner_id = ?", ref.Type, ref.ID).Exec(ctx)
	return errors.Wrap(err, "savedview: erase")
}

// exportedView 是隐私导出中的一条个人视图记录。
type exportedView struct {
	ID        string          `json:"id"`
	Tenant    string          `json:"tenant"`
	Target    string          `json:"target"`
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"config"`
	Position  int             `json:"position"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Export 导出主体 ref 拥有的全部个人视图，签名与 privacy.Source.Export 一致；没有个人视图时返回 nil。
func Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error) {
	if err := ref.Validate(); err != nil {
		return nil, errors.Wrap(err, "savedview")
	}
	var rows []row
	err := db.NewSelect().Model(&rows).
		Where("sv.owner_type = ? AND sv.owner_id = ?", ref.Type, ref.ID).
		OrderExpr("sv.tenant ASC, sv.target ASC, sv.position ASC, sv.created_at ASC, sv.id ASC").
		Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "savedview: export")
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]exportedView, len(rows))
	for i, r := range rows {
		out[i] = exportedView{
			ID:        r.ID,
			Tenant:    r.Tenant,
			Target:    r.Target,
			Name:      r.Name,
			Config:    slices.Clone(r.Config),
			Position:  r.Position,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		}
	}
	return out, nil
}

// loadVisible 按 id 读取 sub 在当前租户与 target 下可见的视图行；lock 为 true 时加行锁。
func (g *Group[T]) loadVisible(ctx context.Context, db bun.IDB, sub identity.Subject, id string, lock bool) (*row, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound.New()
	}
	r := new(row)
	q := db.NewSelect().Model(r).Where("sv.id = ? AND sv.tenant = ? AND sv.target = ?", id, sub.Tenant, g.target)
	if ref := ownerOf(sub); !ref.IsZero() {
		q = q.Where("(sv.owner_id = '' OR (sv.owner_type = ? AND sv.owner_id = ?))", ref.Type, ref.ID)
	} else {
		q = q.Where("sv.owner_id = ''")
	}
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return r, errors.Wrapf(err, "savedview: load %q", g.target)
}

// canManageRow 报告 sub 是否有权修改或删除行 r。
func (g *Group[T]) canManageRow(sub identity.Subject, r *row) bool {
	if r.OwnerID == "" {
		return g.CanManageShared(sub)
	}
	ref := ownerOf(sub)
	return !ref.IsZero() && r.OwnerType == string(ref.Type) && r.OwnerID == ref.ID
}

// validateName 规范化并校验视图名称长度。
func (g *Group[T]) validateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(name)
	if n == 0 || n > g.maxNameLen {
		return "", ErrInvalidName.New(errorx.Field("name"), errorx.Public("max", g.maxNameLen))
	}
	return name, nil
}

// decode 将数据库行 r 解码为泛型 View[T]。
func (g *Group[T]) decode(r *row) (View[T], error) {
	var cfg T
	if len(r.Config) > 0 {
		if err := json.Unmarshal(r.Config, &cfg); err != nil {
			return View[T]{}, errors.Wrapf(err, "savedview: decode config for %q", g.target)
		}
	}
	scope := ScopeShared
	var owner identity.Ref
	if r.OwnerID != "" {
		scope = ScopePersonal
		owner = identity.Ref{Type: identity.SubjectType(r.OwnerType), ID: r.OwnerID}
	}
	var creator identity.Ref
	if r.CreatedByID != "" {
		creator = identity.Ref{Type: identity.SubjectType(r.CreatedByType), ID: r.CreatedByID}
	}
	return View[T]{
		ID:        r.ID,
		Tenant:    r.Tenant,
		Target:    r.Target,
		Scope:     scope,
		Owner:     owner,
		Name:      r.Name,
		Config:    cfg,
		Position:  r.Position,
		CreatedBy: creator,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}, nil
}

// ownerOf 返回可用于个人视图归属的主体引用；identity.System 或未认证主体返回零值。
func ownerOf(sub identity.Subject) identity.Ref {
	if sub.Type == identity.System || sub.Type == "" || sub.ID == "" {
		return identity.Ref{}
	}
	return sub.Ref()
}

// isNameConflict 报告数据库错误是否为同作用域重名唯一约束冲突。
func isNameConflict(err error) bool {
	return database.IsUniqueViolation(err, "uq_saved_views_shared_name") ||
		database.IsUniqueViolation(err, "uq_saved_views_personal_name")
}

// validKey 报告标识非空且不含空白。
func validKey(s string) bool {
	return s != "" && !strings.ContainsFunc(s, unicode.IsSpace)
}
