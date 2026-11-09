package role

import (
	"context"
	"net/http"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/i18n"
)

// 以下函数返回角色管理的 REST 接口，都需要 role.manage 能力：
//
//	rest.Get(admin, "/permissions", role.PermissionsEndpoint(roles))
//	rest.Get(admin, "/roles", role.RolesEndpoint(roles))
//	rest.Post(admin, "/roles", role.CreateRoleEndpoint(roles))
//	rest.Put(admin, "/roles/{id}", role.UpdateRoleEndpoint(roles))
//	rest.Delete(admin, "/roles/{id}", role.DeleteRoleEndpoint(roles))
//	rest.Get(admin, "/users/{id}/roles", role.UserRolesEndpoint(roles))
//	rest.Put(admin, "/users/{id}/roles", role.SetUserRolesEndpoint(roles))

var tags = []string{"role"}

func security() []map[string][]string { return rest.RequireAuth(Manage) }

// PermissionDTO 是一个权限及其显示名。
type PermissionDTO struct {
	Name  string `json:"name" example:"audit.view"`
	Label string `json:"label" doc:"显示名（按请求的语言）"`
}

// RoleDTO 是一个角色。
type RoleDTO struct {
	ID          string    `json:"id" format:"uuid"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Permissions []string  `json:"permissions" doc:"* 表示全部权限"`
	CreatedAt   time.Time `json:"created_at"`
}

// RoleDTO 返回 r 的 RoleDTO。
func (s *Service) RoleDTO(r *Role) RoleDTO {
	return RoleDTO{ID: r.ID.String(), Name: r.Name, Description: r.Description, Permissions: r.Permissions, CreatedAt: r.CreatedAt}
}

func (s *Service) roleDTOs(list []Role) []RoleDTO {
	out := make([]RoleDTO, 0, len(list))
	for i := range list {
		out = append(out, s.RoleDTO(&list[i]))
	}
	return out
}

type permissionsOutput struct{ Body []PermissionDTO }

// PermissionsEndpoint 列出可以分配给角色的全部权限（含 "*"）。
func PermissionsEndpoint(s *Service) rest.Endpoint[struct{}, permissionsOutput] {
	return rest.Endpoint[struct{}, permissionsOutput]{
		Operation: huma.Operation{OperationID: "role-permissions", Summary: "全部权限", Tags: tags, Security: security()},
		Handler: func(ctx context.Context, _ *struct{}) (*permissionsOutput, error) {
			out := &permissionsOutput{Body: []PermissionDTO{{Name: All, Label: i18n.T(ctx, "permission."+All)}}}
			for _, p := range s.Permissions() {
				out.Body = append(out.Body, PermissionDTO{Name: p, Label: i18n.T(ctx, "permission."+p)})
			}
			return out, nil
		},
	}
}

type rolesOutput struct{ Body []RoleDTO }

// RolesEndpoint 列出全部角色。
func RolesEndpoint(s *Service) rest.Endpoint[struct{}, rolesOutput] {
	return rest.Endpoint[struct{}, rolesOutput]{
		Operation: huma.Operation{OperationID: "role-list", Summary: "角色列表", Tags: tags, Security: security()},
		Handler: func(ctx context.Context, _ *struct{}) (*rolesOutput, error) {
			list, err := s.Roles(ctx)
			if err != nil {
				return nil, err
			}
			return &rolesOutput{Body: s.roleDTOs(list)}, nil
		},
	}
}

type roleBody struct {
	Name        string   `json:"name" minLength:"1" maxLength:"50"`
	Description string   `json:"description,omitempty" maxLength:"200"`
	Permissions []string `json:"permissions" maxItems:"500"`
}

type createRoleInput struct{ Body roleBody }

type roleOutput struct{ Body RoleDTO }

// CreateRoleEndpoint 创建角色。
func CreateRoleEndpoint(s *Service) rest.Endpoint[createRoleInput, roleOutput] {
	return rest.Endpoint[createRoleInput, roleOutput]{
		Operation: huma.Operation{OperationID: "role-create", Summary: "创建角色", Tags: tags,
			DefaultStatus: http.StatusCreated, Security: security()},
		Handler: func(ctx context.Context, in *createRoleInput) (*roleOutput, error) {
			r := &Role{Name: in.Body.Name, Description: in.Body.Description, Permissions: in.Body.Permissions}
			if err := s.Save(ctx, r); err != nil {
				return nil, err
			}
			return &roleOutput{Body: s.RoleDTO(r)}, nil
		},
	}
}

type updateRoleInput struct {
	ID   string `path:"id" format:"uuid"`
	Body roleBody
}

// UpdateRoleEndpoint 修改角色。
func UpdateRoleEndpoint(s *Service) rest.Endpoint[updateRoleInput, roleOutput] {
	return rest.Endpoint[updateRoleInput, roleOutput]{
		Operation: huma.Operation{OperationID: "role-update", Summary: "修改角色", Tags: tags, Security: security()},
		Handler: func(ctx context.Context, in *updateRoleInput) (*roleOutput, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			r, err := s.Role(ctx, id)
			if err != nil {
				return nil, err
			}
			r.Name, r.Description, r.Permissions = in.Body.Name, in.Body.Description, in.Body.Permissions
			if err := s.Save(ctx, r); err != nil {
				return nil, err
			}
			return &roleOutput{Body: s.RoleDTO(r)}, nil
		},
	}
}

type idInput struct {
	ID string `path:"id" format:"uuid"`
}

func parseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return id, ErrNotFound
	}
	return id, nil
}

// DeleteRoleEndpoint 删除角色。
func DeleteRoleEndpoint(s *Service) rest.Endpoint[idInput, struct{}] {
	return rest.Endpoint[idInput, struct{}]{
		Operation: huma.Operation{OperationID: "role-delete", Summary: "删除角色", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: security()},
		Handler: func(ctx context.Context, in *idInput) (*struct{}, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			return nil, s.Delete(ctx, id)
		},
	}
}

// UserRolesEndpoint 返回用户的角色，路径参数 id 为用户 ID。
func UserRolesEndpoint(s *Service) rest.Endpoint[idInput, rolesOutput] {
	return rest.Endpoint[idInput, rolesOutput]{
		Operation: huma.Operation{OperationID: "role-user-roles", Summary: "用户的角色", Tags: tags, Security: security()},
		Handler: func(ctx context.Context, in *idInput) (*rolesOutput, error) {
			uid, err := uuid.Parse(in.ID)
			if err != nil {
				return &rolesOutput{Body: []RoleDTO{}}, nil
			}
			list, err := s.UserRoles(ctx, uid)
			if err != nil {
				return nil, err
			}
			return &rolesOutput{Body: s.roleDTOs(list)}, nil
		},
	}
}

type setUserRolesInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		RoleIDs []string `json:"role_ids" maxItems:"100"`
	}
}

// SetUserRolesEndpoint 设置用户的角色（替换原有的），路径参数 id 为用户 ID。
func SetUserRolesEndpoint(s *Service) rest.Endpoint[setUserRolesInput, rolesOutput] {
	return rest.Endpoint[setUserRolesInput, rolesOutput]{
		Operation: huma.Operation{OperationID: "role-set-user-roles", Summary: "设置用户的角色", Tags: tags, Security: security()},
		Handler: func(ctx context.Context, in *setUserRolesInput) (*rolesOutput, error) {
			uid, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, ErrNotFound
			}
			ids := make([]uuid.UUID, 0, len(in.Body.RoleIDs))
			for _, s := range in.Body.RoleIDs {
				id, err := parseID(s)
				if err != nil {
					return nil, err
				}
				ids = append(ids, id)
			}
			if err := s.SetUserRoles(ctx, uid, ids...); err != nil {
				return nil, err
			}
			list, err := s.UserRoles(ctx, uid)
			if err != nil {
				return nil, err
			}
			return &rolesOutput{Body: s.roleDTOs(list)}, nil
		},
	}
}
