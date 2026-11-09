// Package role 是角色与权限，类似 spatie/laravel-permission：
//
//   - 权限是代码中声明的字符串（Declare），与 authz 的能力同名，例如 "audit.view"、"order.refund"；
//   - 角色是数据：管理员在后台创建角色、勾选权限，再把角色分配给用户；拥有 "*" 权限的角色拥有全部权限。
//
// 与授权连接后，用户的角色拥有某个权限时，就具有同名的能力，不需要为它写规则：
//
//	roles := role.New(a.DB)
//	roles.Declare("audit.view", "setting.manage", "order.refund")
//	roles.Attach(a.Gate)
//	a.Router.Use(roles.Middleware) // 一个请求内只查一次当前用户的权限
//
// 需要看具体对象的能力（“只能改自己的订单”）仍用 authz.DefineFor 写规则，规则中可以用 HasPermission。
// 权限的显示名是多语言消息 permission.<权限>，由声明它的模块或产品提供。
//
// 暂不支持团队或租户内的角色。迁移：把 role.Migrations 加入 app.Options.Publish。
package role

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"github.com/ryan053857/go-kit/authz"
	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/i18n"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations 是本模块的迁移。
var Migrations = database.MigrationSource{Name: "role", FS: mustSub(migrationsFS, "migrations")}

//go:embed locales/*.yaml
var locales embed.FS

func init() { i18n.MustLoad(locales) }

// 权限。
const (
	All    = "*"           // 全部权限（超级管理员）
	Manage = "role.manage" // 管理角色与权限，本模块的接口需要它
)

var (
	ErrNotFound          = httpx.DefineError(http.StatusNotFound, "role.not_found")
	ErrNameTaken         = httpx.DefineError(http.StatusConflict, "role.name_taken")
	ErrUnknownPermission = httpx.DefineError(http.StatusUnprocessableEntity, "role.unknown_permission")
)

// Role 是一个角色。
type Role struct {
	bun.BaseModel `bun:"table:roles,alias:r"`
	database.Model

	Name        string   `bun:"name,notnull"`
	Description string   `bun:"description,notnull"`
	Permissions []string `bun:"permissions,array,notnull"`
}

type userRole struct {
	bun.BaseModel `bun:"table:user_roles,alias:ur"`

	UserID    uuid.UUID `bun:"user_id,pk,type:uuid"`
	RoleID    uuid.UUID `bun:"role_id,pk,type:uuid"`
	CreatedAt time.Time `bun:"created_at,notnull,default:current_timestamp"`
}

// Service 管理角色、权限与用户的角色。
type Service struct {
	db          *bun.DB
	permissions []string
}

// New 返回 Service，已声明本模块的 role.manage 权限。
func New(db *bun.DB) *Service {
	s := &Service{db: db}
	s.Declare(Manage)
	return s
}

// Declare 声明权限，供角色勾选。应在启动时调用；重复声明会被忽略。
func (s *Service) Declare(permissions ...string) {
	for _, p := range permissions {
		if p != "" && p != All && !slices.Contains(s.permissions, p) {
			s.permissions = append(s.permissions, p)
		}
	}
}

// Permissions 返回已声明的权限（不含 "*"），按声明顺序。
func (s *Service) Permissions() []string { return slices.Clone(s.permissions) }

func (s *Service) checkPermissions(perms []string) error {
	for _, p := range perms {
		if p != All && !slices.Contains(s.permissions, p) {
			return httpx.WithData(ErrUnknownPermission, map[string]any{"Permission": p})
		}
	}
	return nil
}

// Roles 返回全部角色，按创建时间排序。
func (s *Service) Roles(ctx context.Context) ([]Role, error) {
	list := []Role{}
	err := s.db.NewSelect().Model(&list).Order("id").Scan(ctx)
	return list, errors.Wrap(err, "role: list")
}

// Role 返回角色，不存在时返回 ErrNotFound。
func (s *Service) Role(ctx context.Context, id uuid.UUID) (*Role, error) {
	r := new(Role)
	err := s.db.NewSelect().Model(r).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, errors.Wrap(err, "role: get")
}

// Save 创建（r.ID 为零值）或更新角色。权限必须已声明（或为 "*"），否则返回 ErrUnknownPermission；
// 名称重复时返回 ErrNameTaken；更新的角色不存在时返回 ErrNotFound。
func (s *Service) Save(ctx context.Context, r *Role) error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Permissions == nil {
		r.Permissions = []string{}
	}
	if err := s.checkPermissions(r.Permissions); err != nil {
		return err
	}
	var err error
	if r.ID == uuid.Nil() {
		_, err = s.db.NewInsert().Model(r).Exec(ctx)
	} else {
		var res sql.Result
		res, err = s.db.NewUpdate().Model(r).Column("name", "description", "permissions", "updated_at").WherePK().Exec(ctx)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrNotFound
			}
		}
	}
	if database.IsUniqueViolation(err, "roles_name_key") {
		return ErrNameTaken
	}
	return errors.Wrap(err, "role: save")
}

// Ensure 确保存在名为 name 的角色并拥有 permissions（不存在时创建，存在时更新权限），返回角色。
// 用于启动时初始化，例如 roles.Ensure(ctx, "超级管理员", role.All)。
func (s *Service) Ensure(ctx context.Context, name string, permissions ...string) (*Role, error) {
	if err := s.checkPermissions(permissions); err != nil {
		return nil, err
	}
	r := &Role{Name: name, Permissions: permissions}
	if r.Permissions == nil {
		r.Permissions = []string{}
	}
	_, err := s.db.NewInsert().Model(r).
		On("CONFLICT (name) DO UPDATE").Set("permissions = EXCLUDED.permissions, updated_at = EXCLUDED.updated_at").
		Returning("*").Exec(ctx)
	return r, errors.Wrap(err, "role: ensure")
}

// Delete 删除角色（拥有它的用户随之失去它），不存在时返回 ErrNotFound。
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.NewDelete().Model((*Role)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return errors.Wrap(err, "role: delete")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UserRoles 返回用户的角色。
func (s *Service) UserRoles(ctx context.Context, userID uuid.UUID) ([]Role, error) {
	list := []Role{}
	err := s.db.NewSelect().Model(&list).
		Where("id IN (SELECT role_id FROM user_roles WHERE user_id = ?)", userID).Order("id").Scan(ctx)
	return list, errors.Wrap(err, "role: user roles")
}

// SetUserRoles 把用户的角色设置为 roleIDs（替换原有的）。任一角色不存在时返回 ErrNotFound。
func (s *Service) SetUserRoles(ctx context.Context, userID uuid.UUID, roleIDs ...uuid.UUID) error {
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*userRole)(nil)).Where("user_id = ?", userID).Exec(ctx); err != nil {
			return err
		}
		if len(roleIDs) == 0 {
			return nil
		}
		n, err := tx.NewSelect().Model((*Role)(nil)).Where("id IN (?)", bun.List(roleIDs)).Count(ctx)
		if err != nil {
			return err
		}
		if int(n) != len(roleIDs) {
			return ErrNotFound
		}
		rows := make([]userRole, len(roleIDs))
		for i, id := range roleIDs {
			rows[i] = userRole{UserID: userID, RoleID: id}
		}
		_, err = tx.NewInsert().Model(&rows).On("CONFLICT DO NOTHING").Exec(ctx)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return err
	}
	return errors.Wrap(err, "role: set user roles")
}

// AddUserRole 给用户加上一个角色（已有时什么也不做）。
func (s *Service) AddUserRole(ctx context.Context, userID, roleID uuid.UUID) error {
	_, err := s.db.NewInsert().Model(&userRole{UserID: userID, RoleID: roleID}).On("CONFLICT DO NOTHING").Exec(ctx)
	return errors.Wrap(err, "role: add user role")
}

// UserPermissions 返回用户通过角色拥有的全部权限（去重），可能包含 "*"。
func (s *Service) UserPermissions(ctx context.Context, userID uuid.UUID) ([]string, error) {
	perms := []string{}
	err := s.db.NewRaw(`SELECT COALESCE(array_agg(DISTINCT p), '{}')
		FROM roles AS r JOIN user_roles AS ur ON ur.role_id = r.id, unnest(r.permissions) AS p
		WHERE ur.user_id = ?`, userID).Scan(ctx, pgdialect.Array(&perms))
	return perms, errors.Wrap(err, "role: user permissions")
}

// HasPermission 报告用户是否拥有权限 perm（或 "*"）。在挂载了 Middleware 的请求中只查一次数据库。
func (s *Service) HasPermission(ctx context.Context, userID uuid.UUID, perm string) (bool, error) {
	perms, err := s.cachedPermissions(ctx, userID)
	if err != nil {
		return false, err
	}
	return slices.Contains(perms, All) || slices.Contains(perms, perm), nil
}

// Attach 把角色权限接入授权：用户的角色拥有与能力同名的权限（或 "*"）时允许，否则交给其他规则。
func (s *Service) Attach(g *authz.Gate) {
	g.Before(func(ctx context.Context, user uuid.UUID, ability string) (allowed, decided bool) {
		ok, err := s.HasPermission(ctx, user, ability)
		if err != nil || !ok {
			return false, false
		}
		return true, true
	})
}

type cacheKey struct{}

type cache struct {
	mu    sync.Mutex
	perms map[uuid.UUID][]string
}

// Middleware 在请求的 context 中放入权限缓存，一个请求内每个用户的权限只查一次。
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), cacheKey{}, &cache{perms: map[uuid.UUID][]string{}})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Service) cachedPermissions(ctx context.Context, userID uuid.UUID) ([]string, error) {
	c, ok := ctx.Value(cacheKey{}).(*cache)
	if !ok {
		return s.UserPermissions(ctx, userID)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if perms, ok := c.perms[userID]; ok {
		return perms, nil
	}
	perms, err := s.UserPermissions(ctx, userID)
	if err != nil {
		return nil, err
	}
	c.perms[userID] = perms
	return perms, nil
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
