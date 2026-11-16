package privacy

import (
	"context"
	"database/sql"
	"time"
	"unicode/utf8"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

var (
	// ErrPending 表示主体已经申请了注销，还在冷静期中。
	ErrPending = errorx.Define(errorx.Conflict, "privacy.erasure_pending")
	// ErrNotFound 表示主体没有待执行的注销：没有申请过、已经撤销，或者已经删除完成。
	ErrNotFound = errorx.Define(errorx.NotFound, "privacy.erasure_not_found")
)

// retryDelay 是 EraseDue 执行失败之后推迟重试的时间。
var retryDelay = 10 * time.Minute

// clock 返回当前时间，截到 PostgreSQL timestamptz 的精度（微秒），返回的 Erasure 与读回的相同。
func clock() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// maxError 是 last_error 保存的最大字符数。
const maxError = 1000

// Source 是保存了主体数据的一个地方，例如框架的 login、push，或者应用自己的用户表。
type Source struct {
	// Name 区分各项，出现在错误和日志中，例如 login、notifications。
	Name string
	// Check 报告 ref 现在能否注销，不能时返回业务错误（errorx），例如“你是团队唯一的所有者”。
	// Request 和 Erase 在开始时依次调用全部 Check；冷静期结束、EraseDue 执行时不再检查。可以为 nil。
	Check func(ctx context.Context, ref identity.Ref) error
	// Erase 在 db 中删除 ref 的数据。全部 Source 的 Erase 按登记的顺序在同一个事务中执行，db 是这个 database.Tx：
	// 任何一项返回错误，整个事务回滚，稍后重试。要删除外部系统中的数据（对象存储、向量库、支付平台）时，
	// 在这个事务中用 queue 发布任务，与删除一起提交。可以为 nil。
	//
	// Erase 可能执行多次（失败后重试），必须是幂等的：数据已经不存在时直接返回 nil。
	Erase func(ctx context.Context, db bun.IDB, ref identity.Ref) error
}

// Options 是 New 的选项。
type Options struct {
	// Grace 是冷静期：Request 之后经过这么久，EraseDue 才删除数据；期间用户可以 Cancel。
	// 0 表示没有冷静期，下一次 EraseDue 就删除。不能为负数。
	Grace time.Duration
	// Sources 是保存了主体数据的地方，至少一项。Name 不能为空或重复，Check 和 Erase 至少有一个。
	Sources []Source
}

// Erasure 是一次注销，也是 privacy_erasures 表的 Bun 模型，本包函数之外的查询（例如后台列表）可以直接用 Bun 编写。
type Erasure struct {
	bun.BaseModel `bun:"table:privacy_erasures,alias:pe"`

	ID          uuid.UUID            `bun:"id,pk,type:uuid"` // UUIDv7，按时间递增
	SubjectType identity.SubjectType `bun:"subject_type,notnull"`
	SubjectID   string               `bun:"subject_id,notnull"`
	RequestedAt time.Time            `bun:"requested_at,notnull"`
	DueAt       time.Time            `bun:"due_at,notnull"`     // 冷静期结束的时间；失败后是下一次重试的时间
	ErasedAt    time.Time            `bun:"erased_at,nullzero"` // 删除完成的时间，零值表示还没有删除
	Attempts    int                  `bun:"attempts,notnull"`   // 已经执行过的次数，包括失败的
	LastError   string               `bun:"last_error,notnull"` // 最近一次失败的原因，成功后为空
}

// Subject 返回注销的主体。
func (e Erasure) Subject() identity.Ref { return identity.Ref{Type: e.SubjectType, ID: e.SubjectID} }

// Erased 报告数据是否已经删除。
func (e Erasure) Erased() bool { return !e.ErasedAt.IsZero() }

// Service 申请、撤销和执行注销，可以并发使用，多个实例同时执行 EraseDue 也是安全的。
type Service struct {
	db      *bun.DB
	grace   time.Duration
	sources []Source
}

// New 创建服务。db 必须已执行 Migrations 的迁移。选项不合法时 panic。
func New(db *bun.DB, opts Options) *Service {
	if db == nil {
		panic("privacy: nil database")
	}
	if opts.Grace < 0 {
		panic("privacy: negative grace period")
	}
	if len(opts.Sources) == 0 {
		panic("privacy: no sources")
	}
	seen := make(map[string]bool, len(opts.Sources))
	for _, src := range opts.Sources {
		switch {
		case src.Name == "":
			panic("privacy: source without a name")
		case seen[src.Name]:
			panic(errors.Newf("privacy: duplicate source %q", src.Name))
		case src.Check == nil && src.Erase == nil:
			panic(errors.Newf("privacy: source %q has neither Check nor Erase", src.Name))
		}
		seen[src.Name] = true
	}
	return &Service{db: db, grace: opts.Grace, sources: append([]Source(nil), opts.Sources...)}
}

// Check 依次调用各项的 Check，返回第一个错误，用于在申请页面上提前告诉用户能否注销。
func (s *Service) Check(ctx context.Context, ref identity.Ref) error {
	if err := ref.Validate(); err != nil {
		return errors.Wrap(err, "privacy")
	}
	for _, src := range s.sources {
		if src.Check == nil {
			continue
		}
		if err := src.Check(ctx, ref); err != nil {
			return errors.Wrapf(err, "privacy: check %s", src.Name)
		}
	}
	return nil
}

// Request 申请注销 ref：执行 Check，然后在 tx 中登记，冷静期结束后由 EraseDue 删除。
// tx 通常是申请所在的业务事务，例如同时记录操作日志、发布通知邮件的任务。
// 已经申请过、还没有删除时返回 ErrPending。
//
// Request 不撤销登录：冷静期内用户仍然可以登录，看到“注销中”，用 Cancel 撤销。
func (s *Service) Request(ctx context.Context, tx bun.IDB, ref identity.Ref) (*Erasure, error) {
	if err := s.Check(ctx, ref); err != nil {
		return nil, err
	}
	now := clock()
	e, err := newErasure(ref, now, now.Add(s.grace))
	if err != nil {
		return nil, err
	}
	res, err := tx.NewInsert().Model(e).
		On("CONFLICT (subject_type, subject_id) WHERE erased_at IS NULL DO NOTHING").Exec(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "privacy: request")
	}
	if n, err := res.RowsAffected(); err != nil {
		return nil, errors.Wrap(err, "privacy: request")
	} else if n == 0 {
		return nil, ErrPending.New()
	}
	return e, nil
}

// Pending 返回 ref 待执行的注销，没有时返回 ErrNotFound。登录后用它提示“账号将于某日注销”。
func (s *Service) Pending(ctx context.Context, ref identity.Ref) (*Erasure, error) {
	e := new(Erasure)
	err := s.db.NewSelect().Model(e).
		Where("subject_type = ? AND subject_id = ?", ref.Type, ref.ID).Where("erased_at IS NULL").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	if err != nil {
		return nil, errors.Wrap(err, "privacy: pending")
	}
	return e, nil
}

// Cancel 在 tx 中撤销 ref 待执行的注销，没有时返回 ErrNotFound。
// 正在执行删除时，Cancel 等它结束，然后返回 ErrNotFound。
func (s *Service) Cancel(ctx context.Context, tx bun.IDB, ref identity.Ref) error {
	res, err := tx.NewDelete().Model((*Erasure)(nil)).
		Where("subject_type = ? AND subject_id = ?", ref.Type, ref.ID).Where("erased_at IS NULL").Exec(ctx)
	if err != nil {
		return errors.Wrap(err, "privacy: cancel")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "privacy: cancel")
	}
	if n == 0 {
		return ErrNotFound.New()
	}
	return nil
}

// Erase 跳过冷静期，立即删除 ref 的数据，例如管理员删除账号：执行 Check，然后在一个事务中执行全部 Erase。
// ref 有待执行的注销时一并完成；任何一项失败时整个事务回滚，返回这个错误，什么都不会改变。
func (s *Service) Erase(ctx context.Context, ref identity.Ref) error {
	if err := s.Check(ctx, ref); err != nil {
		return err
	}
	return database.RunInTx(ctx, s.db, nil, func(ctx context.Context, tx database.Tx) error {
		now := clock()
		e := new(Erasure)
		err := tx.NewSelect().Model(e).
			Where("subject_type = ? AND subject_id = ?", ref.Type, ref.ID).Where("erased_at IS NULL").
			For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			if e, err = newErasure(ref, now, now); err != nil {
				return err
			}
			if _, err := tx.NewInsert().Model(e).Exec(ctx); err != nil {
				return errors.Wrap(err, "privacy: erase")
			}
		} else if err != nil {
			return errors.Wrap(err, "privacy: erase")
		}
		if err := s.run(ctx, tx, ref); err != nil {
			return err
		}
		return s.finish(ctx, tx, e.ID, now)
	})
}

// EraseDue 删除最多 limit 个冷静期已过的主体，返回删除完成的个数，交给 schedule.Cleanup：
//
//	sched.Add("privacy:erase", "*/10 * * * *", schedule.Cleanup(deletions.EraseDue, 100))
//
// 每个主体在自己的事务中执行全部 Erase。失败时这个主体的修改全部回滚，失败原因记在 LastError，
// 推迟一段时间后重试；其他主体照常执行，失败合并返回。
func (s *Service) EraseDue(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, errors.New("privacy: erase limit must be positive")
	}
	done := 0
	var errs []error
	for range limit {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		var picked bool
		var failed error
		err := database.RunInTx(ctx, s.db, nil, func(ctx context.Context, tx database.Tx) error {
			now := clock()
			e := new(Erasure)
			err := tx.NewSelect().Model(e).Where("erased_at IS NULL").Where("due_at <= ?", now).
				OrderExpr("due_at").Limit(1).For("UPDATE SKIP LOCKED").Scan(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return errors.Wrap(err, "privacy: select due erasure")
			}
			picked = true
			// 保存点：删除失败时只回滚各项的修改，失败原因仍然写进这一行。
			failed = database.RunInTx(ctx, tx, nil, func(ctx context.Context, sp database.Tx) error {
				return s.run(ctx, sp, e.Subject())
			})
			if failed != nil {
				_, err := tx.NewUpdate().Model((*Erasure)(nil)).Where("id = ?", e.ID).
					Set("attempts = attempts + 1").Set("last_error = ?", truncate(failed.Error())).
					Set("due_at = ?", now.Add(retryDelay)).Exec(ctx)
				return errors.Wrap(err, "privacy: record failure")
			}
			return s.finish(ctx, tx, e.ID, now)
		})
		if err != nil {
			errs = append(errs, err)
			break
		}
		if !picked {
			break
		}
		if failed != nil {
			errs = append(errs, failed)
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}

// run 在 tx 中按顺序执行各项的 Erase。
func (s *Service) run(ctx context.Context, tx database.Tx, ref identity.Ref) error {
	for _, src := range s.sources {
		if src.Erase == nil {
			continue
		}
		if err := src.Erase(ctx, tx, ref); err != nil {
			return errors.Wrapf(err, "privacy: erase %s for %s", src.Name, ref)
		}
	}
	return nil
}

// finish 把注销标为已删除。
func (s *Service) finish(ctx context.Context, tx bun.IDB, id uuid.UUID, now time.Time) error {
	_, err := tx.NewUpdate().Model((*Erasure)(nil)).Where("id = ?", id).
		Set("erased_at = ?", now).Set("attempts = attempts + 1").Set("last_error = ''").Exec(ctx)
	return errors.Wrap(err, "privacy: finish erasure")
}

func newErasure(ref identity.Ref, now, due time.Time) (*Erasure, error) {
	if err := ref.Validate(); err != nil {
		return nil, errors.Wrap(err, "privacy")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, errors.Wrap(err, "privacy: new id")
	}
	return &Erasure{ID: id, SubjectType: ref.Type, SubjectID: ref.ID, RequestedAt: now, DueAt: due}, nil
}

// truncate 截短错误信息，保留前 maxError 个字符。
func truncate(s string) string {
	if utf8.RuneCountInString(s) <= maxError {
		return s
	}
	return string([]rune(s)[:maxError])
}
