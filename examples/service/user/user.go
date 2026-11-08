// Package user 定义认证得到的 Principal，以及 Session 与 Bearer 两种方式的加载与校验函数。
//
// 示例中只有一个来自配置文件的演示账号；实际产品从数据库读取用户，
// API token 的格式与校验方式（JWT、数据库查询……）由产品决定。
package user

import (
	"context"
	"crypto/subtle"

	"github.com/shellphy-kit/tools/httpx/auth"
	"github.com/shellphy-kit/tools/support/password"
)

// User 是本服务的 Principal。
type User struct {
	ID   string
	Name string
}

// UserID 实现 auth.Principal。
func (u *User) UserID() string { return u.ID }

// Store 保存演示账号。
type Store struct {
	user         *User
	passwordHash string
	apiToken     string
}

// NewStore 用配置中的演示账号创建 Store。密码在启动时哈希，内存中不保留明文。
func NewStore(name, plainPassword, apiToken string) *Store {
	return &Store{
		user:         &User{ID: "u-1", Name: name},
		passwordHash: password.Hash(plainPassword),
		apiToken:     apiToken,
	}
}

// Authenticate 校验用户名与密码，失败时返回 auth.ErrUnauthenticated。
func (s *Store) Authenticate(_ context.Context, name, plainPassword string) (*User, error) {
	ok, err := password.Verify(plainPassword, s.passwordHash)
	if err != nil {
		return nil, err
	}
	if name != s.user.Name || !ok {
		return nil, auth.ErrUnauthenticated
	}
	return s.user, nil
}

// Load 按会话中的用户 ID 加载 User，供 auth.LoadSession 使用。
func (s *Store) Load(_ context.Context, id string) (*User, error) {
	if id != s.user.ID {
		return nil, auth.ErrUnauthenticated // 用户已删除：按未登录处理
	}
	return s.user, nil
}

// VerifyToken 校验 Bearer token，供 rest.Authorize 使用。
func (s *Store) VerifyToken(_ context.Context, token string) (*User, error) {
	if s.apiToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.apiToken)) != 1 {
		return nil, auth.ErrUnauthenticated
	}
	return s.user, nil
}
