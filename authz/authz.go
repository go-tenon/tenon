package authz

import (
	"cmp"
	"context"
	"database/sql"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/cache"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
)

// AllTenants 是分配角色时表示“所有租户”的域，例如让运营人员在每个租户中都有只读支持权限。
// 平台本身（""）另行处理。
const AllTenants = "*"

// roleNameConstraint 是“同一租户中的角色名唯一”的约束名，见 authz 的迁移。
const roleNameConstraint = "authz_roles_tenant_name_unique"

var (
	// ErrNotMember 表示主体在本次请求的租户中没有任何角色。Resolve 返回它，请求得到 404，不暴露租户是否存在。
	ErrNotMember = errorx.Define(errorx.NotFound, "authz.not_member")
	// ErrRoleNotFound 表示角色不存在，或在该租户中不可见。
	ErrRoleNotFound = errorx.Define(errorx.NotFound, "authz.role_not_found")
	// ErrRoleExists 表示同一租户中已有同名角色。
	ErrRoleExists = errorx.Define(errorx.Conflict, "authz.role_exists")
	// ErrInvalid 表示输入不合法，例如权限不在目录中、把租户自定义的角色分配到其他租户。
	ErrInvalid = errorx.Define(errorx.InvalidArgument, "authz.invalid")
)

// Permission 是权限目录中的一项。
type Permission = identity.Permission

// Catalog 是应用在代码中声明的全部权限。Store 只接受目录中的权限，以及能匹配目录中权限的通配符。
// 它也可以交给 rest.Options.Permissions，写入 OpenAPI，客户端据此生成权限常量。
type Catalog []Permission

// Role 是一组权限。
type Role struct {
	ID          string
	Tenant      string // "" 为全局角色，可在任何租户和平台中分配；否则是该租户自定义的角色，只能在该租户中分配
	Name        string
	Description string
	Permissions []string // 支持通配符，见 identity.Subject.Can
}

// Member 是一个主体和它在某个域中的角色。
type Member struct {
	Subject identity.Ref
	Roles   []Role
}

// Store 是基于 Bun 的角色与分配存储，Migrations 提供表结构。可以并发使用。
//
// 权限属于角色，角色在某个域中分配给主体。域是租户 ID、平台本身（""）或所有租户（AllTenants）。
// 主体在租户 t 中的权限，是它在 t 和 AllTenants 中全部角色的权限之和；在平台中的权限只来自 "" 中的角色。
type Store struct {
	db      bun.IDB
	catalog Catalog
	cache   *cache.Cache[grant] // nil 表示不缓存
	prefix  string              // 缓存的命名空间，见 Cache
}

// grant 是缓存的权限，即 Permissions 的结果。
type grant struct {
	Permissions []string `json:"p,omitempty"`
	Member      bool     `json:"m,omitempty"`
}

// Option 是 NewStore 的选项。
type Option func(*Store)

// Cache 把主体在每个域中的权限缓存在 Memcached 中，Resolve 和 Permissions 优先读取缓存。
// mc 是应用共用的 Memcached 客户端；prefix 是命名空间，例如 "blog:prod:"，共用一个 Memcached 的应用和环境各不相同，
// 否则会读到别的数据库中的权限；ttl 是缓存的过期时间，必须为正。
//
// 缓存不改变“权限变化立即生效”：Assign、Unassign、RemoveMember 使主体的缓存失效，UpdateRole、DeleteRole 使全部缓存失效，
// 方法返回之后的读取都得到新的权限，多个实例共用同一份缓存。失效在 Store 的事务提交之后执行；
// Store 建在 database.Tx 上时，改为在外层事务提交之后执行（Tx.AfterCommit），建在其他事务上时 NewStore panic。
// 失效失败时修改照常成功，记录 Error 日志，旧的权限最多保留 ttl。绕过 Store 直接修改表时同样最多保留 ttl。
//
// 修改角色和分配的每个 Store 都要使用同一个 mc 和 prefix 的 Cache，缓存才会随修改失效。
func Cache(mc cache.Client, prefix string, ttl time.Duration) Option {
	c := cache.New[grant](mc, prefix+"authz:v1:permissions:", ttl)
	return func(s *Store) { s.cache, s.prefix = c, prefix }
}

// roleRow 是 authz_roles 表中的一行。
type roleRow struct {
	bun.BaseModel `bun:"table:authz_roles,alias:r"`

	ID          uuid.UUID `bun:"id,pk,type:uuid"`
	Tenant      string    `bun:"tenant,notnull"`
	Name        string    `bun:"name,notnull"`
	Description string    `bun:"description,notnull"`
	CreatedAt   time.Time `bun:"created_at,notnull"`
	UpdatedAt   time.Time `bun:"updated_at,notnull"`
}

// permissionRow 是 authz_role_permissions 表中的一行：角色的一项权限。
type permissionRow struct {
	bun.BaseModel `bun:"table:authz_role_permissions,alias:p"`

	RoleID     uuid.UUID `bun:"role_id,pk,type:uuid"`
	Permission string    `bun:"permission,pk"`
}

// assignmentRow 是 authz_assignments 表中的一行：主体在租户中的一个角色。
type assignmentRow struct {
	bun.BaseModel `bun:"table:authz_assignments,alias:a"`

	SubjectType string    `bun:"subject_type,pk"`
	SubjectID   string    `bun:"subject_id,pk"`
	Tenant      string    `bun:"tenant,pk"`
	RoleID      uuid.UUID `bun:"role_id,pk,type:uuid"`
	CreatedAt   time.Time `bun:"created_at,notnull"`
}

// NewStore 创建存储。db 必须已执行 authz 迁移。catalog 是应用的权限目录，为空时 panic。
func NewStore(db bun.IDB, catalog Catalog, opts ...Option) *Store {
	if db == nil {
		panic("authz: nil database")
	}
	if len(catalog) == 0 {
		panic("authz: empty permission catalog")
	}
	for _, p := range catalog {
		if err := checkName(p.Name, "permission"); err != nil || strings.Contains(p.Name, "*") {
			panic(errors.Newf("authz: invalid catalog permission %q", p.Name))
		}
	}
	s := &Store{db: db, catalog: slices.Clone(catalog)}
	for _, opt := range opts {
		opt(s)
	}
	if s.cache != nil {
		switch db.(type) {
		case database.Tx:
		case bun.Tx, *bun.Tx:
			panic("authz: Cache needs a Store on a database or a database.Tx: invalidation must run after commit")
		}
	}
	return s
}

// Catalog 返回权限目录，供角色编辑界面列出可选的权限。
func (s *Store) Catalog() Catalog { return slices.Clone(s.catalog) }

// Resolve 是一个 identity.Resolver，可以直接作为 Options.Accounts 中一套账号的 Resolver：按 identity.TenantFromContext 加载 sub
// 在本次请求的租户中的权限，写入 sub.Permissions 和 sub.Tenant。
// sub 在租户中没有任何角色时返回 ErrNotMember（404）；在平台中没有角色是正常的，权限为空。
func (s *Store) Resolve(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
	tenant := identity.TenantFromContext(ctx)
	perms, member, err := s.Permissions(ctx, sub.Ref(), tenant)
	if err != nil {
		return identity.Subject{}, err
	}
	if !member && tenant != "" {
		return identity.Subject{}, ErrNotMember.New()
	}
	sub.Tenant, sub.Permissions = tenant, perms
	return sub, nil
}

// Permissions 返回主体 ref 在 tenant 中的全部权限（去重、排序），以及它在其中是否有任何角色。
// 使用 Cache 时先读缓存。
func (s *Store) Permissions(ctx context.Context, ref identity.Ref, tenant string) ([]string, bool, error) {
	if s.cache == nil {
		return s.permissions(ctx, ref, tenant)
	}
	g, err := s.cache.Remember(ctx, lengthPrefixed(string(ref.Type), ref.ID, tenant), func(ctx context.Context) (grant, error) {
		// 缓存在修改提交后失效，接着的读取要在主库：读库上的旧数据写进缓存后，要等它过期才会更正。
		perms, member, err := s.permissions(database.Primary(ctx), ref, tenant)
		return grant{Permissions: perms, Member: member}, err
	}, s.subjectTag(ref), s.rolesTag())
	return g.Permissions, g.Member, err
}

// permissions 从数据库读取主体 ref 在 tenant 中的权限。
func (s *Store) permissions(ctx context.Context, ref identity.Ref, tenant string) ([]string, bool, error) {
	var rows []struct {
		RoleID     uuid.UUID      `bun:"role_id"`
		Permission sql.NullString `bun:"permission"`
	}
	err := s.db.NewSelect().TableExpr("authz_assignments AS a").
		Join("LEFT JOIN authz_role_permissions AS p ON p.role_id = a.role_id").
		ColumnExpr("a.role_id, p.permission").
		Where("a.subject_type = ? AND a.subject_id = ?", ref.Type, ref.ID).Where("a.tenant IN (?)", bun.List(domains(tenant))).
		Scan(ctx, &rows)
	if err != nil {
		return nil, false, errors.Wrap(err, "authz: permissions")
	}
	var perms []string
	for _, r := range rows {
		if r.Permission.Valid {
			perms = append(perms, r.Permission.String)
		}
	}
	slices.Sort(perms)
	return slices.Compact(perms), len(rows) > 0, nil
}

// domains 返回租户的权限来自哪些域。
func domains(tenant string) []string {
	if tenant == "" {
		return []string{""}
	}
	return []string{tenant, AllTenants}
}

// CreateRole 创建角色，返回带 ID 的角色。权限必须在目录中，同一租户中的角色名唯一。
func (s *Store) CreateRole(ctx context.Context, r Role) (Role, error) {
	if err := s.checkRole(r); err != nil {
		return Role{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Role{}, errors.Wrap(err, "authz: new id")
	}
	now := time.Now().UTC()
	row := &roleRow{ID: id, Tenant: r.Tenant, Name: r.Name, Description: r.Description, CreatedAt: now, UpdatedAt: now}
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(row).Exec(ctx); database.IsUniqueViolation(err, roleNameConstraint) {
			return ErrRoleExists.New()
		} else if err != nil {
			return errors.Wrap(err, "authz: insert role")
		}
		return setPermissions(ctx, tx, id, r.Permissions)
	})
	if err != nil {
		return Role{}, err
	}
	r.ID, r.Permissions = id.String(), normalize(r.Permissions)
	return r, nil
}

// UpdateRole 修改角色的名称、说明和权限；r.ID 指定角色，角色所属的租户保持不变，r.Tenant 不起作用。
// tenant 不为空时只能修改该租户自定义的角色，全局角色和其他租户的角色视为不存在，返回 ErrRoleNotFound，
// 供租户管理员的界面使用；tenant 为空时不限制，供平台管理员使用。修改立即影响所有拥有这个角色的主体。
func (s *Store) UpdateRole(ctx context.Context, r Role, tenant string) (Role, error) {
	id, err := uuid.Parse(r.ID)
	if err != nil {
		return Role{}, ErrRoleNotFound.New()
	}
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var row roleRow
		q := tx.NewSelect().Model(&row).Where("id = ?", id)
		if tenant != "" {
			q = q.Where("tenant = ?", tenant)
		}
		if err := q.Scan(ctx); errors.Is(err, sql.ErrNoRows) {
			return ErrRoleNotFound.New()
		} else if err != nil {
			return errors.Wrap(err, "authz: load role")
		}
		r.Tenant = row.Tenant
		if err := s.checkRole(r); err != nil {
			return err
		}
		_, err := tx.NewUpdate().Model((*roleRow)(nil)).
			Set("name = ?", r.Name).Set("description = ?", r.Description).Set("updated_at = ?", time.Now().UTC()).
			Where("id = ?", id).Exec(ctx)
		if database.IsUniqueViolation(err, roleNameConstraint) {
			return ErrRoleExists.New()
		} else if err != nil {
			return errors.Wrap(err, "authz: update role")
		}
		if _, err := tx.NewDelete().Model((*permissionRow)(nil)).Where("role_id = ?", id).Exec(ctx); err != nil {
			return errors.Wrap(err, "authz: clear permissions")
		}
		return setPermissions(ctx, tx, id, r.Permissions)
	})
	if err != nil {
		return Role{}, err
	}
	s.invalidate(ctx, s.rolesTag())
	r.Permissions = normalize(r.Permissions)
	return r, nil
}

// DeleteRole 删除角色及其全部分配。tenant 的含义与 UpdateRole 相同：不为空时只能删除该租户自定义的角色。
// 角色不存在时返回 ErrRoleNotFound。
func (s *Store) DeleteRole(ctx context.Context, id, tenant string) error {
	rid, err := uuid.Parse(id)
	if err != nil {
		return ErrRoleNotFound.New()
	}
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		q := tx.NewDelete().Model((*roleRow)(nil)).Where("id = ?", rid)
		if tenant != "" {
			q = q.Where("tenant = ?", tenant)
		}
		res, err := q.Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "authz: delete role")
		}
		if n, err := res.RowsAffected(); err != nil {
			return errors.Wrap(err, "authz: delete role")
		} else if n == 0 {
			return ErrRoleNotFound.New()
		}
		for _, model := range []any{(*permissionRow)(nil), (*assignmentRow)(nil)} {
			if _, err := tx.NewDelete().Model(model).Where("role_id = ?", rid).Exec(ctx); err != nil {
				return errors.Wrap(err, "authz: delete role")
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.invalidate(ctx, s.rolesTag())
	return nil
}

// Role 返回角色。tenant 不为空时，只返回全局角色或该租户的角色，其他租户的角色视为不存在，
// 供租户管理员的界面使用；tenant 为空时不限制。
func (s *Store) Role(ctx context.Context, id, tenant string) (Role, error) {
	rid, err := uuid.Parse(id)
	if err != nil {
		return Role{}, ErrRoleNotFound.New()
	}
	q := s.db.NewSelect().Model((*roleRow)(nil)).Where("id = ?", rid)
	if tenant != "" {
		q = q.Where("tenant IN (?)", bun.List([]string{"", tenant}))
	}
	roles, err := s.loadRoles(ctx, q)
	if err != nil {
		return Role{}, err
	}
	if len(roles) == 0 {
		return Role{}, ErrRoleNotFound.New()
	}
	return roles[0], nil
}

// RoleByName 返回 tenant 中名为 name 的角色：tenant 为 "" 时是全局角色，否则是该租户自定义的角色（不包括同名的全局角色）。
// 不存在时返回 ErrRoleNotFound。预置的角色按名称引用，例如 roles.RoleByName(ctx, "owner", "")。
func (s *Store) RoleByName(ctx context.Context, name, tenant string) (Role, error) {
	q := s.db.NewSelect().Model((*roleRow)(nil)).Where("tenant = ?", tenant).Where("name = ?", name)
	roles, err := s.loadRoles(ctx, q)
	if err != nil {
		return Role{}, err
	}
	if len(roles) == 0 {
		return Role{}, ErrRoleNotFound.New()
	}
	return roles[0], nil
}

// EnsureRole 让 r.Tenant 中名为 r.Name 的角色与 r 一致：不存在时创建，存在时把说明和权限改成 r 的；返回带 ID 的角色。
// 用于在代码中声明的预置角色（例如平台管理员、租户的所有者），应用启动时为每个预置角色调用一次：重复调用、多个实例同时调用
// 都是安全的，代码中改了权限，下一次启动时生效。r.ID 不起作用。在界面中修改这样的角色，下一次调用时会被改回代码中的样子。
func (s *Store) EnsureRole(ctx context.Context, r Role) (Role, error) {
	if err := s.checkRole(r); err != nil {
		return Role{}, err
	}
	newID, err := uuid.NewV7()
	if err != nil {
		return Role{}, errors.Wrap(err, "authz: new id")
	}
	perms := normalize(r.Permissions)
	var id uuid.UUID
	changed := false
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 同时调用时只有一个插入成功，其余的等它提交后读到同一行。
		now := time.Now().UTC()
		row := &roleRow{ID: newID, Tenant: r.Tenant, Name: r.Name, Description: r.Description, CreatedAt: now, UpdatedAt: now}
		res, err := tx.NewInsert().Model(row).On("CONFLICT ON CONSTRAINT " + roleNameConstraint + " DO NOTHING").Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "authz: insert role")
		}
		if n, err := res.RowsAffected(); err != nil {
			return errors.Wrap(err, "authz: insert role")
		} else if n == 1 {
			id = newID
			return setPermissions(ctx, tx, id, perms)
		}
		var cur roleRow
		err = tx.NewSelect().Model(&cur).Where("tenant = ?", r.Tenant).Where("name = ?", r.Name).For("UPDATE").Scan(ctx)
		if err != nil {
			return errors.Wrap(err, "authz: load role")
		}
		id = cur.ID
		var have []string
		err = tx.NewSelect().Model((*permissionRow)(nil)).Column("permission").Where("role_id = ?", id).OrderExpr("permission").Scan(ctx, &have)
		if err != nil {
			return errors.Wrap(err, "authz: load permissions")
		}
		if cur.Description == r.Description && slices.Equal(have, perms) {
			return nil
		}
		changed = true
		_, err = tx.NewUpdate().Model((*roleRow)(nil)).
			Set("description = ?", r.Description).Set("updated_at = ?", now).Where("id = ?", id).Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "authz: update role")
		}
		if _, err := tx.NewDelete().Model((*permissionRow)(nil)).Where("role_id = ?", id).Exec(ctx); err != nil {
			return errors.Wrap(err, "authz: clear permissions")
		}
		return setPermissions(ctx, tx, id, perms)
	})
	if err != nil {
		return Role{}, err
	}
	if changed {
		s.invalidate(ctx, s.rolesTag())
	}
	r.ID, r.Permissions = id.String(), perms
	return r, nil
}

// Roles 返回 tenant 中可以分配的角色：全局角色，以及 tenant 自定义的角色（tenant 为空时只有全局角色），
// 全局角色在前，各自按名称排序。
func (s *Store) Roles(ctx context.Context, tenant string) ([]Role, error) {
	q := s.db.NewSelect().Model((*roleRow)(nil)).Where("tenant IN (?)", bun.List([]string{"", tenant}))
	return s.loadRoles(ctx, q)
}

// loadRoles 执行选择 authz_roles 的查询 q，并加载每个角色的权限。
func (s *Store) loadRoles(ctx context.Context, q *bun.SelectQuery) ([]Role, error) {
	var rows []roleRow
	if err := q.OrderExpr("tenant, name").Scan(ctx, &rows); err != nil {
		return nil, errors.Wrap(err, "authz: load roles")
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	var perms []permissionRow
	err := s.db.NewSelect().Model(&perms).Where("role_id IN (?)", bun.List(ids)).OrderExpr("permission").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "authz: load permissions")
	}
	byRole := map[uuid.UUID][]string{}
	for _, p := range perms {
		byRole[p.RoleID] = append(byRole[p.RoleID], p.Permission)
	}
	roles := make([]Role, len(rows))
	for i, r := range rows {
		roles[i] = Role{ID: r.ID.String(), Tenant: r.Tenant, Name: r.Name, Description: r.Description, Permissions: byRole[r.ID]}
	}
	return roles, nil
}

// Assign 在 tenant 中把角色分配给主体 ref；已经分配时直接返回。
// tenant 为 "" 表示平台，AllTenants 表示所有租户。租户自定义的角色只能在该租户中分配，
// AllTenants 中只能分配全局角色，否则返回 ErrInvalid；角色不存在时返回 ErrRoleNotFound。
func (s *Store) Assign(ctx context.Context, ref identity.Ref, tenant, roleID string) error {
	if ref.Validate() != nil {
		return ErrInvalid.New(errorx.Public("field", "subject"))
	}
	rid, err := uuid.Parse(roleID)
	if err != nil {
		return ErrRoleNotFound.New()
	}
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var role roleRow
		if err := tx.NewSelect().Model(&role).Column("tenant").Where("id = ?", rid).Scan(ctx); errors.Is(err, sql.ErrNoRows) {
			return ErrRoleNotFound.New()
		} else if err != nil {
			return errors.Wrap(err, "authz: load role")
		}
		if role.Tenant != "" && role.Tenant != tenant {
			return ErrInvalid.New(errorx.Public("field", "tenant"))
		}
		row := &assignmentRow{SubjectType: string(ref.Type), SubjectID: ref.ID, Tenant: tenant, RoleID: rid, CreatedAt: time.Now().UTC()}
		_, err := tx.NewInsert().Model(row).On("CONFLICT DO NOTHING").Exec(ctx)
		return errors.Wrap(err, "authz: assign")
	})
	if err != nil {
		return err
	}
	s.invalidate(ctx, s.subjectTag(ref))
	return nil
}

// Unassign 撤销主体 ref 在 tenant 中的角色；没有分配时直接返回，roleID 不是合法的角色 ID 时返回 ErrRoleNotFound。
func (s *Store) Unassign(ctx context.Context, ref identity.Ref, tenant, roleID string) error {
	rid, err := uuid.Parse(roleID)
	if err != nil {
		return ErrRoleNotFound.New()
	}
	_, err = s.db.NewDelete().Model((*assignmentRow)(nil)).
		Where("subject_type = ? AND subject_id = ?", ref.Type, ref.ID).Where("tenant = ?", tenant).Where("role_id = ?", rid).Exec(ctx)
	if err != nil {
		return errors.Wrap(err, "authz: unassign")
	}
	s.invalidate(ctx, s.subjectTag(ref))
	return nil
}

// RemoveMember 撤销主体 ref 在 tenant 中的全部角色，返回撤销的个数，例如把成员移出租户。
func (s *Store) RemoveMember(ctx context.Context, ref identity.Ref, tenant string) (int, error) {
	res, err := s.db.NewDelete().Model((*assignmentRow)(nil)).
		Where("subject_type = ? AND subject_id = ?", ref.Type, ref.ID).Where("tenant = ?", tenant).Exec(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "authz: remove member")
	}
	s.invalidate(ctx, s.subjectTag(ref))
	n, err := res.RowsAffected()
	return int(n), errors.Wrap(err, "authz: remove member")
}

// Erase 在 db 中撤销主体 ref 在所有域中的全部角色，用于注销账号。db 通常是注销所在的事务，
// 签名与 privacy.Source 的 Erase 相同。
//
// 有缓存时，db 是 database.Tx 则在外层事务提交之后使缓存失效，是其他事务时返回错误：
// 提交之前删除的缓存可能被并发的读取用旧数据重新填上。
func (s *Store) Erase(ctx context.Context, db bun.IDB, ref identity.Ref) error {
	if err := ref.Validate(); err != nil {
		return errors.Wrap(err, "authz")
	}
	tx, isTx := db.(database.Tx)
	if s.cache != nil && !isTx {
		switch db.(type) {
		case bun.Tx, *bun.Tx:
			return errors.New("authz: erase with Cache needs a database.Tx: invalidation must run after commit")
		}
	}
	_, err := db.NewDelete().Model((*assignmentRow)(nil)).
		Where("subject_type = ? AND subject_id = ?", ref.Type, ref.ID).Exec(ctx)
	if err != nil {
		return errors.Wrap(err, "authz: erase")
	}
	if s.cache == nil {
		return nil
	}
	tag := s.subjectTag(ref)
	if isTx {
		tx.AfterCommit(func(ctx context.Context) error { return s.cache.Invalidate(ctx, tag) })
		return nil
	}
	if err := s.cache.Invalidate(ctx, tag); err != nil {
		slog.ErrorContext(ctx, "invalidate cached permissions failed", "error", err, logging.AttrComponent, "authz")
	}
	return nil
}

// Export 在 db 中读取主体 ref 在各个域中的角色，用于导出个人数据；签名与 privacy.Source 的 Export 相同。
// 每个分配是一个对象：tenant（"" 为平台本身，* 为所有租户）、role（角色名称）、assigned_at。
func (s *Store) Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error) {
	if err := ref.Validate(); err != nil {
		return nil, errors.Wrap(err, "authz")
	}
	type exported struct {
		Tenant     string    `bun:"tenant"      json:"tenant"`
		Role       string    `bun:"role"        json:"role"`
		AssignedAt time.Time `bun:"assigned_at" json:"assigned_at"`
	}
	out := []exported{}
	err := db.NewSelect().TableExpr("authz_assignments AS a").
		Join("JOIN authz_roles AS r ON r.id = a.role_id").
		ColumnExpr("a.tenant, r.name AS role, a.created_at AS assigned_at").
		Where("a.subject_type = ? AND a.subject_id = ?", ref.Type, ref.ID).
		OrderExpr("a.tenant, r.name").Scan(ctx, &out)
	if err != nil {
		return nil, errors.Wrap(err, "authz: export")
	}
	return out, nil
}

// subjectTag 是主体 ref 在所有域中的权限缓存的 tag，分配变化时失效。
func (s *Store) subjectTag(ref identity.Ref) string {
	return s.prefix + "authz:subject:" + lengthPrefixed(string(ref.Type), ref.ID)
}

// rolesTag 是全部权限缓存的 tag，角色的权限变化时失效。
func (s *Store) rolesTag() string {
	return s.prefix + "authz:roles"
}

// invalidate 在修改提交之后使 tags 的缓存失效。Store 建在 database.Tx 上时登记到外层事务提交之后。
// 修改已经生效，所以失效失败只记录日志，旧的权限在缓存过期后消失。
func (s *Store) invalidate(ctx context.Context, tags ...string) {
	if s.cache == nil {
		return
	}
	if tx, ok := s.db.(database.Tx); ok {
		tx.AfterCommit(func(ctx context.Context) error { return s.cache.Invalidate(ctx, tags...) })
		return
	}
	if err := s.cache.Invalidate(ctx, tags...); err != nil {
		slog.ErrorContext(ctx, "invalidate cached permissions failed", "error", err, logging.AttrComponent, "authz")
	}
}

// lengthPrefixed 把各部分连接成没有歧义的缓存键：每部分前面加上它的长度。
func lengthPrefixed(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(strconv.Itoa(len(p)))
		b.WriteByte(':')
		b.WriteString(p)
	}
	return b.String()
}

// RolesOf 返回主体 ref 在 tenant 这个域中直接分配的角色，不包括 AllTenants 中的角色。
func (s *Store) RolesOf(ctx context.Context, ref identity.Ref, tenant string) ([]Role, error) {
	if ref.IsZero() {
		return nil, nil
	}
	members, err := s.members(ctx, tenant, ref)
	if err != nil || len(members) == 0 {
		return nil, err
	}
	return members[0].Roles, nil
}

// Members 返回在 tenant 这个域中有角色的全部主体及其角色，按主体的类型和 ID 排序，供成员管理界面使用。
func (s *Store) Members(ctx context.Context, tenant string) ([]Member, error) {
	return s.members(ctx, tenant, identity.Ref{})
}

// members 返回 tenant 中的成员；ref 不为零值时只返回这个主体。
func (s *Store) members(ctx context.Context, tenant string, ref identity.Ref) ([]Member, error) {
	q := s.db.NewSelect().Model((*assignmentRow)(nil)).Column("subject_type", "subject_id", "role_id").Where("tenant = ?", tenant)
	if !ref.IsZero() {
		q = q.Where("subject_type = ? AND subject_id = ?", ref.Type, ref.ID)
	}
	var rows []assignmentRow
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, errors.Wrap(err, "authz: members")
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.RoleID
	}
	roles, err := s.loadRoles(ctx, s.db.NewSelect().Model((*roleRow)(nil)).Where("id IN (?)", bun.List(ids)))
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Role, len(roles))
	for _, r := range roles {
		byID[r.ID] = r
	}
	bySubject := map[identity.Ref][]Role{}
	for _, r := range rows {
		ref := identity.Ref{Type: identity.SubjectType(r.SubjectType), ID: r.SubjectID}
		bySubject[ref] = append(bySubject[ref], byID[r.RoleID.String()])
	}
	members := make([]Member, 0, len(bySubject))
	for ref, roles := range bySubject {
		slices.SortFunc(roles, func(a, b Role) int { return cmp.Or(cmp.Compare(a.Tenant, b.Tenant), cmp.Compare(a.Name, b.Name)) })
		members = append(members, Member{Subject: ref, Roles: roles})
	}
	slices.SortFunc(members, func(a, b Member) int {
		return cmp.Or(cmp.Compare(a.Subject.Type, b.Subject.Type), cmp.Compare(a.Subject.ID, b.Subject.ID))
	})
	return members, nil
}

// Tenants 返回主体 ref 有角色的全部租户，按 ID 排序，不包括平台和 AllTenants，供租户切换界面使用。
func (s *Store) Tenants(ctx context.Context, ref identity.Ref) ([]string, error) {
	var tenants []string
	err := s.db.NewSelect().Model((*assignmentRow)(nil)).Distinct().Column("tenant").
		Where("subject_type = ? AND subject_id = ?", ref.Type, ref.ID).Where("tenant NOT IN (?)", bun.List([]string{"", AllTenants})).
		OrderExpr("tenant").Scan(ctx, &tenants)
	return tenants, errors.Wrap(err, "authz: tenants")
}

// checkRole 检查角色的租户、名称和权限。
func (s *Store) checkRole(r Role) error {
	if r.Tenant == AllTenants {
		return ErrInvalid.New(errorx.Public("field", "tenant"))
	}
	if err := checkName(r.Name, "name"); err != nil {
		return err
	}
	for _, p := range r.Permissions {
		if !s.known(p) {
			return ErrInvalid.New(errorx.Public("field", "permissions"), errorx.Public("permission", p))
		}
	}
	return nil
}

// known 报告 perm 是目录中的权限，或是能匹配目录中权限的通配符。
func (s *Store) known(perm string) bool {
	if perm == "*" {
		return true
	}
	if checkName(perm, "permission") != nil {
		return false
	}
	for _, p := range s.catalog {
		if identity.Grants([]string{perm}, p.Name) {
			return true
		}
	}
	return false
}

// checkName 检查名称非空且不含空白。
func checkName(name, field string) error {
	if name == "" || strings.ContainsFunc(name, unicode.IsSpace) {
		return ErrInvalid.New(errorx.Public("field", field))
	}
	return nil
}

// setPermissions 在事务 tx 中把角色 id 的权限替换为 perms。
func setPermissions(ctx context.Context, tx bun.Tx, id uuid.UUID, perms []string) error {
	perms = normalize(perms)
	if len(perms) == 0 {
		return nil
	}
	rows := make([]permissionRow, len(perms))
	for i, p := range perms {
		rows[i] = permissionRow{RoleID: id, Permission: p}
	}
	_, err := tx.NewInsert().Model(&rows).Exec(ctx)
	return errors.Wrap(err, "authz: set permissions")
}

// normalize 返回去重并排序的权限。
func normalize(perms []string) []string {
	if len(perms) == 0 {
		return nil
	}
	out := slices.Clone(perms)
	slices.Sort(out)
	return slices.Compact(out)
}
