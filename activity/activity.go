package activity

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/page"
)

// DefaultPerPage 是 List 在 Query.Page.PerPage 不大于 0 时每页返回的条数。
const DefaultPerPage = 20

// Activity 是一条操作记录，也是 activity_log 表的 Bun 模型，List 之外的查询可以直接用 Bun 编写。
type Activity struct {
	bun.BaseModel `bun:"table:activity_log,alias:al"`

	ID uuid.UUID `bun:"id,pk,type:uuid"` // UUIDv7，按时间递增
	database.Timestamps
	Tenant           string               `bun:"tenant,notnull"` // 操作所在的租户，"" 为平台本身
	ActorType        identity.SubjectType `bun:"actor_type,notnull"`
	ActorID          string               `bun:"actor_id,notnull"` // 操作人，见 Actor；可以为空
	ImpersonatorType identity.SubjectType `bun:"impersonator_type,notnull"`
	ImpersonatorID   string               `bun:"impersonator_id,notnull"` // 代为登录时实际操作的人，见 Impersonator；可以为空
	Event            string               `bun:"event,notnull"`           // 发生了什么，例如 article.published
	TargetType       string               `bun:"target_type,notnull"`
	TargetID         string               `bun:"target_id,notnull"`  // 被操作的对象，例如 article 和文章 ID；可以为空
	Properties       map[string]any       `bun:"properties,notnull"` // 展示这条记录需要的其他数据，保存为 JSON
	RequestID        string               `bun:"request_id,notnull"` // 产生这条记录的请求，队列任务中是发布任务的请求
}

// Actor 返回操作人。
func (a Activity) Actor() identity.Ref { return identity.Ref{Type: a.ActorType, ID: a.ActorID} }

// Impersonator 返回代为登录时实际操作的人，见 identity.Subject.Impersonator；零值表示操作人本人。
func (a Activity) Impersonator() identity.Ref {
	return identity.Ref{Type: a.ImpersonatorType, ID: a.ImpersonatorID}
}

// Record 在 db 中写入一条操作记录。db 通常是业务修改所在的事务，记录与修改一起提交或回滚。
//
// a 只需要填写 Event、TargetType、TargetID 和 Properties，其余字段由 Record 填写：
// 操作人和租户来自 actor（它的 ID、Type、Impersonator 和 Tenant），队列和定时任务传入代表系统的主体；
// RequestID 来自 logging.RequestID(ctx)，HTTP 请求和由它发布的队列任务都带有请求 ID。
func Record(ctx context.Context, db bun.IDB, actor identity.Subject, a Activity) error {
	id, err := uuid.NewV7()
	if err != nil {
		return errors.Wrap(err, "activity: new id")
	}
	a.ID, a.Timestamps = id, database.Timestamps{}
	a.Tenant, a.ActorType, a.ActorID = actor.Tenant, actor.Type, actor.ID
	a.ImpersonatorType, a.ImpersonatorID = actor.Impersonator.Type, actor.Impersonator.ID
	a.RequestID = logging.RequestID(ctx)
	if a.Properties == nil {
		a.Properties = map[string]any{}
	}
	if err := check(a); err != nil {
		return err
	}
	_, err = db.NewInsert().Model(&a).Exec(ctx)
	return errors.Wrap(err, "activity: record")
}

// check 校验记录的必填字段，返回第一个不合法之处。
func check(a Activity) error {
	if a.Event == "" {
		return errors.New("activity: empty event")
	}
	if a.TargetID != "" && a.TargetType == "" {
		return errors.Newf("activity: target id %q without target type", a.TargetID)
	}
	return nil
}

// Query 是 List 的条件。Tenant 总是生效，其余条件为空时不限制。
type Query struct {
	Tenant     string // 只查询这个租户中的记录，"" 为平台本身
	TargetType string
	TargetID   string       // 需要同时设置 TargetType
	Actor      identity.Ref // 操作人，零值不限制
	Event      string
	Page       page.OffsetParams // 分页参数，零值为第 1 页、每页 DefaultPerPage 条
}

// List 按时间从新到旧返回符合 q 的一页记录。
func List(ctx context.Context, db bun.IDB, q Query) (page.Offset[Activity], error) {
	if q.TargetID != "" && q.TargetType == "" {
		return page.Offset[Activity]{}, errors.New("activity: query target id without target type")
	}
	p := q.Page
	if p.Page <= 0 {
		p.Page = 1
	}
	if p.PerPage <= 0 {
		p.PerPage = DefaultPerPage
	}
	activities := []Activity{}
	sel := db.NewSelect().Model(&activities).Where("tenant = ?", q.Tenant)
	for _, f := range [...]struct{ column, value string }{
		{"target_type", q.TargetType}, {"target_id", q.TargetID}, {"event", q.Event},
	} {
		if f.value != "" {
			sel = sel.Where("? = ?", bun.Ident(f.column), f.value)
		}
	}
	if !q.Actor.IsZero() {
		sel = sel.Where("actor_type = ? AND actor_id = ?", q.Actor.Type, q.Actor.ID)
	}
	total, err := sel.OrderExpr("id DESC").Limit(p.Limit()).Offset(p.Offset()).ScanAndCount(ctx)
	if err != nil {
		return page.Offset[Activity]{}, errors.Wrap(err, "activity: list")
	}
	return page.NewOffset(activities, p, int64(total)), nil
}

// Export 返回主体 ref 在所有租户中作为操作人的记录，按时间从旧到新，用于导出个人数据；签名与 privacy.Source 的 Export 相同。
// 每条记录是一个对象：tenant、event、target_type、target_id、properties、impersonated（是否由别人代为登录操作）、created_at，
// 空的字段省略。注销账号时操作日志不删除，由 Cleanup 按保留期删除。
func Export(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error) {
	if err := ref.Validate(); err != nil {
		return nil, errors.Wrap(err, "activity")
	}
	var rows []Activity
	err := db.NewSelect().Model(&rows).
		Where("actor_type = ? AND actor_id = ?", ref.Type, ref.ID).OrderExpr("id").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "activity: export")
	}
	type exported struct {
		Tenant       string         `json:"tenant"`
		Event        string         `json:"event"`
		TargetType   string         `json:"target_type,omitempty"`
		TargetID     string         `json:"target_id,omitempty"`
		Properties   map[string]any `json:"properties,omitempty"`
		Impersonated bool           `json:"impersonated,omitempty"`
		CreatedAt    time.Time      `json:"created_at"`
	}
	out := make([]exported, len(rows))
	for i, a := range rows {
		out[i] = exported{
			Tenant: a.Tenant, Event: a.Event, TargetType: a.TargetType, TargetID: a.TargetID,
			Properties: a.Properties, Impersonated: a.ImpersonatorID != "", CreatedAt: a.CreatedAt,
		}
	}
	return out, nil
}

// Cleanup 返回删除 keep 以前的记录的清理函数，每次调用最多删除 limit 条，交给 schedule.Cleanup：
//
//	sched.Add("activity:cleanup", "0 4 * * *", schedule.Cleanup(activity.Cleanup(db, 180*24*time.Hour), 1000))
func Cleanup(db bun.IDB, keep time.Duration) func(ctx context.Context, limit int) (int, error) {
	if db == nil {
		panic("activity: nil database")
	}
	if keep <= 0 {
		panic("activity: keep must be positive")
	}
	return func(ctx context.Context, limit int) (int, error) {
		if limit <= 0 {
			return 0, errors.New("activity: cleanup limit must be positive")
		}
		old := db.NewSelect().Model((*Activity)(nil)).Column("id").
			Where("created_at < ?", time.Now().Add(-keep).UTC()).OrderExpr("created_at").Limit(int64(limit))
		res, err := db.NewDelete().Model((*Activity)(nil)).
			Where("id IN (?)", old).Exec(ctx)
		if err != nil {
			return 0, errors.Wrap(err, "activity: cleanup")
		}
		n, err := res.RowsAffected()
		return int(n), errors.Wrap(err, "activity: cleanup")
	}
}
