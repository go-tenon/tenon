// Package opsmcp 为 Tenon 应用提供开箱即用的只读运维诊断 MCP 工具集。
//
// 所有工具均挂载在应用的 mcpx.Server 上，经由 mcpx.Handler 复用应用的认证、授权与访问日志，
// 只暴露受护栏保护的只读观测能力（自检、只读 SQL、队列、定时任务、Redis、NATS），不暴露任何写操作。
//
//	opsSrv := mcpx.New(mcpx.Options{Name: "app-ops", Instructions: "线上只读排障工具。"})
//	opsmcp.Register(opsSrv, opsmcp.Options{
//		Permissions: []string{"ops:read"},
//		Doctor:      a.doctor,
//		DB:          a.diagDB,
//		QueueAdmin:  a.queueAdmin,
//		Schedule:    a.scheduleStore,
//		Redis:       a.rdb,
//		NATS:        a.nc,
//	})
//	router.Handle("/ops/mcp", mcpx.Handler(opsSrv, mcpx.HandlerOptions{
//		Validate: tokens.Validate,
//		Accounts: api.Accounts,
//	}))
package opsmcp

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/mcpx"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/schedule"
)

const (
	// defaultQueryTimeout 是 ops_db_query 单条 SQL 的默认超时。
	defaultQueryTimeout = 3 * time.Second
	// defaultLockTimeout 是 ops_db_query 等待数据库锁的默认超时。
	defaultLockTimeout = 500 * time.Millisecond
	// defaultMaxRows 是 ops_db_query 默认返回的最大行数。
	defaultMaxRows = 50
	// hardMaxRows 是 ops_db_query 允许返回的行数硬上限。
	hardMaxRows = 200
	// defaultMaxCellLen 是单列或单个键值返回的最大字符数。
	defaultMaxCellLen = 500
	// defaultInfraTimeout 是读取 Redis、NATS、JetStream 状态的默认超时。
	defaultInfraTimeout = 3 * time.Second
	// redactedValue 是敏感字段或敏感键脱敏后的占位文本。
	redactedValue = "[REDACTED]"
)

var (
	// ErrQueryRejected 表示查询在执行前被护栏拒绝（例如空语句、多语句、非只读语句或未满足只读角色约束）。
	ErrQueryRejected = errorx.Define(errorx.InvalidArgument, "ops.query_rejected")
	// ErrQueryFailed 表示数据库或外部依赖执行只读查询时返回错误。
	ErrQueryFailed = errorx.Define(errorx.InvalidArgument, "ops.query_failed")
)

// defaultSensitiveColumns 是默认脱敏的 SQL 列名关键词（不区分大小写，子串匹配）。
var defaultSensitiveColumns = []string{
	"password",
	"passwd",
	"hash",
	"secret",
	"token",
	"private_key",
	"api_key",
	"apikey",
	"access_key",
	"signing_key",
	"encryption_key",
	"credential",
	"totp",
	"recovery_code",
}

// sensitiveValuePrefixes 是按值前缀自动脱敏的凭据格式（如密码哈希与 PEM 私钥），防止通过列别名绕过列名脱敏。
var sensitiveValuePrefixes = []string{
	"$argon2id$",
	"$argon2i$",
	"$2a$",
	"$2b$",
	"$2y$",
	"-----BEGIN ",
}

// defaultSensitiveRedisKeys 是默认脱敏的 Redis 键名关键词（不区分大小写，子串匹配）。
var defaultSensitiveRedisKeys = []string{
	"session",
	"token",
	"secret",
	"password",
	"passwd",
	"credential",
	"api_key",
	"apikey",
	"private_key",
	"cookie",
	"oauth",
}

// Options 是 Register 的配置项。各项依赖均为可选，传入哪些依赖就登记对应的只读工具。
type Options struct {
	Permissions         []string              // 所有已登记运维工具要求的权限，留空时不额外要求工具级权限
	Doctor              func() []doctor.Group // 可选：登记 ops_doctor
	DB                  *bun.DB               // 可选：登记 ops_db_tables 与 ops_db_query（建议使用 pg_read_all_data 只读账号）
	RequireReadOnlyRole bool                  // 为 true 时 ops_db_query 执行前强制校验当前 PostgreSQL 角色没有写权限
	QueryTimeout        time.Duration         // ops_db_query 语句超时，默认 3s
	LockTimeout         time.Duration         // ops_db_query 锁等待超时，默认 500ms
	MaxRows             int                   // ops_db_query 默认返回行数上限，默认 50，最大 200
	MaxCellLen          int                   // 单元格、载荷与键值截断长度（字符数），默认 500
	RedactColumns       []string              // 额外需要脱敏的 SQL 列名关键词（不区分大小写子串匹配）
	QueueDB             *bun.DB               // 可选：队列数据库（用于 ops_queue_status 与 ops_queue_failed）；留空且设置了 QueueAdmin 时自动复用 DB
	QueueAdmin          *queue.Admin          // 可选：让 ops_queue_status 包含 JetStream 各 topic 状态
	Schedule            schedule.Store        // 可选：登记 ops_schedule_status
	Redis               redis.UniversalClient // 可选：登记 ops_redis_info、ops_redis_scan 与 ops_redis_get
	RedactRedisKeys     []string              // 额外需要脱敏的 Redis 键名关键词（不区分大小写子串匹配）
	NATS                *nats.Conn            // 可选：登记 ops_nats_status
	Logger              *slog.Logger          // 记录运维工具调用审计日志；nil 表示 slog.Default()
}

// kit 保存规范化后的依赖与安全护栏参数。
type kit struct {
	perms               []string
	logger              *slog.Logger
	doctor              func() []doctor.Group
	db                  *bun.DB
	requireReadOnlyRole bool
	queryTimeout        time.Duration
	lockTimeout         time.Duration
	maxRows             int
	maxCellLen          int
	redactColumns       []string
	queueDB             *bun.DB
	queueAdmin          *queue.Admin
	schedule            schedule.Store
	rdb                 redis.UniversalClient
	redactRedisKeys     []string
	nc                  *nats.Conn
}

// Register 按 opts 中提供的依赖向 srv 登记对应的只读运维诊断工具。srv 为 nil 时 panic。
func Register(srv *mcpx.Server, opts Options) {
	if srv == nil {
		panic("opsmcp: nil server")
	}
	k := newKit(opts)
	if k.doctor != nil {
		k.registerDoctor(srv)
	}
	if k.db != nil {
		k.registerDB(srv)
	}
	if k.queueDB != nil || k.queueAdmin != nil {
		k.registerQueue(srv)
	}
	if k.schedule != nil {
		k.registerSchedule(srv)
	}
	if k.rdb != nil {
		k.registerRedis(srv)
	}
	if k.nc != nil {
		k.registerNATS(srv)
	}
}

// newKit 补齐默认值并合并内置与自定义的脱敏关键词。
func newKit(opts Options) *kit {
	queryTimeout := opts.QueryTimeout
	if queryTimeout <= 0 {
		queryTimeout = defaultQueryTimeout
	}
	lockTimeout := opts.LockTimeout
	if lockTimeout <= 0 {
		lockTimeout = defaultLockTimeout
	}
	maxRows := opts.MaxRows
	if maxRows <= 0 {
		maxRows = defaultMaxRows
	}
	if maxRows > hardMaxRows {
		maxRows = hardMaxRows
	}
	maxCellLen := opts.MaxCellLen
	if maxCellLen <= 0 {
		maxCellLen = defaultMaxCellLen
	}
	queueDB := opts.QueueDB
	if queueDB == nil && opts.QueueAdmin != nil {
		queueDB = opts.DB
	}
	return &kit{
		perms:               slices.Clone(opts.Permissions),
		logger:              logging.Or(opts.Logger).With(logging.AttrComponent, "opsmcp"),
		doctor:              opts.Doctor,
		db:                  opts.DB,
		requireReadOnlyRole: opts.RequireReadOnlyRole,
		queryTimeout:        queryTimeout,
		lockTimeout:         lockTimeout,
		maxRows:             maxRows,
		maxCellLen:          maxCellLen,
		redactColumns:       mergePatterns(defaultSensitiveColumns, opts.RedactColumns),
		queueDB:             queueDB,
		queueAdmin:          opts.QueueAdmin,
		schedule:            opts.Schedule,
		rdb:                 opts.Redis,
		redactRedisKeys:     mergePatterns(defaultSensitiveRedisKeys, opts.RedactRedisKeys),
		nc:                  opts.NATS,
	}
}

// tool 构造统一的只读工具描述。
func (k *kit) tool(name, title, desc string) mcpx.Tool {
	return mcpx.Tool{
		Name:        name,
		Title:       title,
		Description: desc,
		Permissions: k.perms,
		ReadOnly:    true,
	}
}

// audit 记录运维工具的调用日志，附带调用主体与附加属性。
func (k *kit) audit(ctx context.Context, tool string, attrs ...any) {
	sub, _ := identity.FromContext(ctx)
	args := make([]any, 0, len(attrs)+4)
	args = append(args, "tool", tool, "actor", sub.Ref())
	args = append(args, attrs...)
	k.logger.InfoContext(ctx, "ops tool called", args...)
}

// registerDoctor 登记运行环境自检工具 ops_doctor。
func (k *kit) registerDoctor(srv *mcpx.Server) {
	type doctorArgs struct{}
	mcpx.Register(srv, k.tool(
		"ops_doctor",
		"运行环境自检",
		"运行应用装配的全部 doctor 自检项（数据库、迁移、队列、存储、Redis 等），返回每项的通过/警告/失败状态及修复建议。",
	), func(ctx context.Context, _ doctorArgs) (doctor.DoctorReport, error) {
		k.audit(ctx, "ops_doctor")
		return doctor.Run(ctx, k.doctor()...), nil
	})
}

// mergePatterns 合并并归一化小写关键词列表，跳过空字符串。
func mergePatterns(base, extra []string) []string {
	out := make([]string, 0, len(base)+len(extra))
	for _, p := range base {
		if s := strings.ToLower(strings.TrimSpace(p)); s != "" {
			out = append(out, s)
		}
	}
	for _, p := range extra {
		if s := strings.ToLower(strings.TrimSpace(p)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// matchesAny 报告 name（忽略大小写）是否包含 patterns 中的任一子串。
func matchesAny(name string, patterns []string) bool {
	lower := strings.ToLower(name)
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// truncateString 将字符串按最大字符数截断并附加省略标记。
func truncateString(s string, maxChars int) string {
	if maxChars <= 0 || utf8.RuneCountInString(s) <= maxChars {
		return s
	}
	var b strings.Builder
	b.Grow(maxChars + 16)
	n := 0
	for _, r := range s {
		if n >= maxChars {
			break
		}
		b.WriteRune(r)
		n++
	}
	b.WriteString("…(truncated)")
	return b.String()
}
