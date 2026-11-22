package savedview_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/savedview"
)

type filterConfig struct {
	Status   []string `json:"status,omitempty"`
	Priority string   `json:"priority,omitempty"`
	Sort     string   `json:"sort,omitempty"`
}

func openDB(t *testing.T) *bun.DB {
	t.Helper()
	return dbtest.Open(t, savedview.Migrations())
}

func subject(tenant, id string, perms ...string) identity.Subject {
	return identity.Subject{Type: "member", ID: id, Tenant: tenant, Permissions: perms}
}

func TestCRUDAndVisibility(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openDB(t)
	dbtest.CheckSchema(t, db)
	dbtest.CheckComments(t, db)

	views := savedview.New[filterConfig](db, "conversation", savedview.Options{
		ManageShared: func(sub identity.Subject) bool { return sub.Can("views:manage") },
	})
	contacts := savedview.New[filterConfig](db, "contact", savedview.Options{
		ManageShared: func(sub identity.Subject) bool { return sub.Can("views:manage") },
	})

	admin := subject("t1", "u1", "views:manage")
	agent := subject("t1", "u2")
	otherTenant := subject("t2", "u1", "views:manage")

	// 管理员创建共享视图
	shared, err := views.Create(ctx, admin, savedview.CreateInput[filterConfig]{
		Scope:  savedview.ScopeShared,
		Name:   "  高优待回复  ",
		Config: filterConfig{Status: []string{"open"}, Priority: "high", Sort: "waiting"},
	})
	require.NoError(t, err)
	assert.Equal(t, "高优待回复", shared.Name)
	assert.Equal(t, savedview.ScopeShared, shared.Scope)
	assert.True(t, shared.Owner.IsZero())
	assert.Equal(t, admin.Ref(), shared.CreatedBy)
	assert.True(t, views.CanManage(admin, shared))
	assert.False(t, views.CanManage(agent, shared))

	// 普通成员无权创建共享视图，但可以创建自己的个人视图
	_, err = views.Create(ctx, agent, savedview.CreateInput[filterConfig]{
		Scope: savedview.ScopeShared,
		Name:  "越权共享",
	})
	require.ErrorIs(t, err, savedview.ErrForbidden)

	personal, err := views.Create(ctx, agent, savedview.CreateInput[filterConfig]{
		Scope:  savedview.ScopePersonal,
		Name:   "我的高优",
		Config: filterConfig{Priority: "high"},
	})
	require.NoError(t, err)
	assert.Equal(t, savedview.ScopePersonal, personal.Scope)
	assert.Equal(t, agent.Ref(), personal.Owner)
	assert.True(t, views.CanManage(agent, personal))
	assert.False(t, views.CanManage(admin, personal))

	// 不同 target 互相隔离，允许同名
	contactView, err := contacts.Create(ctx, admin, savedview.CreateInput[filterConfig]{
		Scope: savedview.ScopeShared,
		Name:  "高优待回复",
	})
	require.NoError(t, err)
	assert.Equal(t, "contact", contactView.Target)

	// 列表顺序：共享在前、个人在后；管理员看不到 agent 的个人视图
	adminList, err := views.List(ctx, admin, "")
	require.NoError(t, err)
	require.Len(t, adminList, 1)
	assert.Equal(t, shared.ID, adminList[0].ID)

	agentList, err := views.List(ctx, agent, "")
	require.NoError(t, err)
	require.Len(t, agentList, 2)
	assert.Equal(t, shared.ID, agentList[0].ID)
	assert.Equal(t, personal.ID, agentList[1].ID)

	// 跨租户隔离：t2 看不到 t1 的共享视图
	_, err = views.Get(ctx, otherTenant, shared.ID)
	require.ErrorIs(t, err, savedview.ErrNotFound)

	// 管理员无法 Get/Update/Delete 他人个人视图（返回 404 不暴露存在性）
	_, err = views.Get(ctx, admin, personal.ID)
	require.ErrorIs(t, err, savedview.ErrNotFound)
	_, err = views.Update(ctx, admin, personal.ID, savedview.UpdateInput[filterConfig]{Name: "hack"})
	require.ErrorIs(t, err, savedview.ErrNotFound)
	_, err = views.Delete(ctx, admin, personal.ID)
	require.ErrorIs(t, err, savedview.ErrNotFound)

	// 普通成员可见共享视图，但修改/删除返回 ErrForbidden
	gotShared, err := views.Get(ctx, agent, shared.ID)
	require.NoError(t, err)
	assert.Equal(t, shared.Config, gotShared.Config)
	_, err = views.Update(ctx, agent, shared.ID, savedview.UpdateInput[filterConfig]{Name: "改名"})
	require.ErrorIs(t, err, savedview.ErrForbidden)
	_, err = views.Delete(ctx, agent, shared.ID)
	require.ErrorIs(t, err, savedview.ErrForbidden)

	// 管理员修改共享视图成功
	updated, err := views.Update(ctx, admin, shared.ID, savedview.UpdateInput[filterConfig]{
		Name:   "高优会话",
		Config: filterConfig{Status: []string{"open", "snoozed"}, Priority: "high"},
	})
	require.NoError(t, err)
	assert.Equal(t, "高优会话", updated.Name)
	assert.Equal(t, []string{"open", "snoozed"}, updated.Config.Status)

	// agent 删除自己的个人视图成功
	deleted, err := views.Delete(ctx, agent, personal.ID)
	require.NoError(t, err)
	assert.Equal(t, personal.ID, deleted.ID)
	_, err = views.Get(ctx, agent, personal.ID)
	require.ErrorIs(t, err, savedview.ErrNotFound)
}

func TestValidationConflictsAndLimits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openDB(t)

	views := savedview.New[filterConfig](db, "conversation", savedview.Options{
		MaxShared:    2,
		MaxPersonal:  2,
		MaxNameLen:   10,
		ManageShared: func(sub identity.Subject) bool { return sub.Can("views:manage") },
	})
	admin := subject("t1", "u1", "views:manage")

	// 名称为空或过长
	_, err := views.Create(ctx, admin, savedview.CreateInput[filterConfig]{Scope: savedview.ScopeShared, Name: "   "})
	require.ErrorIs(t, err, savedview.ErrInvalidName)
	_, err = views.Create(ctx, admin, savedview.CreateInput[filterConfig]{Scope: savedview.ScopeShared, Name: "一二三四五六七八九十超"})
	require.ErrorIs(t, err, savedview.ErrInvalidName)

	// 非法作用域
	_, err = views.Create(ctx, admin, savedview.CreateInput[filterConfig]{Scope: "unknown", Name: "ok"})
	require.ErrorIs(t, err, savedview.ErrInvalidScope)
	_, err = views.List(ctx, admin, "unknown")
	require.ErrorIs(t, err, savedview.ErrInvalidScope)

	// 同作用域忽略大小写重名冲突；不同作用域允许同名
	_, err = views.Create(ctx, admin, savedview.CreateInput[filterConfig]{Scope: savedview.ScopeShared, Name: "VIP"})
	require.NoError(t, err)
	_, err = views.Create(ctx, admin, savedview.CreateInput[filterConfig]{Scope: savedview.ScopeShared, Name: "vip"})
	require.ErrorIs(t, err, savedview.ErrNameTaken)
	_, err = views.Create(ctx, admin, savedview.CreateInput[filterConfig]{Scope: savedview.ScopePersonal, Name: "vip"})
	require.NoError(t, err)

	// 达到共享数量上限
	_, err = views.Create(ctx, admin, savedview.CreateInput[filterConfig]{Scope: savedview.ScopeShared, Name: "第二条"})
	require.NoError(t, err)
	_, err = views.Create(ctx, admin, savedview.CreateInput[filterConfig]{Scope: savedview.ScopeShared, Name: "第三条"})
	require.ErrorIs(t, err, savedview.ErrLimitExceeded)
}

func TestReorderAndTransactions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openDB(t)

	views := savedview.New[filterConfig](db, "conversation", savedview.Options{
		ManageShared: func(sub identity.Subject) bool { return sub.Can("views:manage") },
	})
	admin := subject("t1", "u1", "views:manage")

	v1, err := views.Create(ctx, admin, savedview.CreateInput[filterConfig]{Scope: savedview.ScopeShared, Name: "A"})
	require.NoError(t, err)
	v2, err := views.Create(ctx, admin, savedview.CreateInput[filterConfig]{Scope: savedview.ScopeShared, Name: "B"})
	require.NoError(t, err)
	v3, err := views.Create(ctx, admin, savedview.CreateInput[filterConfig]{Scope: savedview.ScopeShared, Name: "C"})
	require.NoError(t, err)

	// 传入部分顺序：C, A 排前，B 自动顺延到最后
	require.NoError(t, views.Reorder(ctx, admin, savedview.ScopeShared, []string{v3.ID, v1.ID}))
	list, err := views.List(ctx, admin, savedview.ScopeShared)
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.Equal(t, []string{v3.ID, v1.ID, v2.ID}, []string{list[0].ID, list[1].ID, list[2].ID})

	// WithDB 事务回滚时视图不会落库
	rollback := errorx.Define(errorx.Internal, "test.rollback")
	err = database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		_, err := views.WithDB(tx).Create(ctx, admin, savedview.CreateInput[filterConfig]{
			Scope: savedview.ScopePersonal,
			Name:  "会回滚",
		})
		require.NoError(t, err)
		return rollback.New()
	})
	require.ErrorIs(t, err, rollback)
	personal, err := views.List(ctx, admin, savedview.ScopePersonal)
	require.NoError(t, err)
	assert.Empty(t, personal)
}

func TestDeleteByOwnerEraseAndExport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openDB(t)

	views := savedview.New[filterConfig](db, "conversation", savedview.Options{
		ManageShared: func(sub identity.Subject) bool { return true },
	})
	uT1 := subject("t1", "u1")
	uT2 := subject("t2", "u1")

	_, err := views.Create(ctx, uT1, savedview.CreateInput[filterConfig]{Scope: savedview.ScopeShared, Name: "租户1共享"})
	require.NoError(t, err)
	_, err = views.Create(ctx, uT1, savedview.CreateInput[filterConfig]{Scope: savedview.ScopePersonal, Name: "租户1个人"})
	require.NoError(t, err)
	_, err = views.Create(ctx, uT2, savedview.CreateInput[filterConfig]{Scope: savedview.ScopePersonal, Name: "租户2个人"})
	require.NoError(t, err)

	// Export 导出两个租户的个人视图，不包含共享视图
	exported, err := savedview.Export(ctx, db, uT1.Ref())
	require.NoError(t, err)
	require.NotNil(t, exported)

	// DeleteByOwner 只删除 t1 下的个人视图，保留 t1 的共享视图和 t2 的个人视图
	require.NoError(t, savedview.DeleteByOwner(ctx, db, "t1", uT1.Ref()))
	t1Shared, err := views.List(ctx, uT1, savedview.ScopeShared)
	require.NoError(t, err)
	assert.Len(t, t1Shared, 1)
	t1Personal, err := views.List(ctx, uT1, savedview.ScopePersonal)
	require.NoError(t, err)
	assert.Empty(t, t1Personal)
	t2Personal, err := views.List(ctx, uT2, savedview.ScopePersonal)
	require.NoError(t, err)
	assert.Len(t, t2Personal, 1)

	// Erase 删除所有租户下的个人视图
	require.NoError(t, savedview.Erase(ctx, db, uT1.Ref()))
	t2Personal, err = views.List(ctx, uT2, savedview.ScopePersonal)
	require.NoError(t, err)
	assert.Empty(t, t2Personal)
	exported, err = savedview.Export(ctx, db, uT1.Ref())
	require.NoError(t, err)
	assert.Nil(t, exported)
}
