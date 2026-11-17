package queue

import (
	"strings"
	"time"

	"github.com/cockroachdb/errors"
)

// Config 是 Worker 的配置，对应配置文件的 queue 段。Concurrency 和 MaxAttempts 是各 topic 的默认值，
// 注册处理函数时可以用同名的 HandleOption 覆盖。NATS 的地址和凭据不在这里：连接由应用用 natsx.Connect 创建，
// 交给 NewWorker 和 NewAdmin。
type Config struct {
	Stream          string        `yaml:"stream"`           // 保存任务的 JetStream stream 的名字，也是任务 subject 的第一段：topic mail.send 的任务发布到 <stream>.job.mail.send。多个应用或环境共用 NATS 时用它区分。由 1 到 32 个字母、数字、'_' 或 '-' 组成。Worker 启动时创建这个 stream，已经存在时检查它的设置。
	Replicas        int           `yaml:"replicas"`         // stream 的副本数：单个 NATS 服务器时为 1；集群中设为 3，一台服务器损坏时任务不丢失。
	Concurrency     int           `yaml:"concurrency"`      // 每个 topic 同时处理的任务数上限。
	MaxAttempts     int           `yaml:"max_attempts"`     // 任务最多处理的次数，用完后转入失败任务表。
	Lease           time.Duration `yaml:"lease"`            // JetStream 等待处理结果的时间（AckWait）。处理期间每 Lease/3 自动延长；进程崩溃时任务在 Lease 之后被重新投递。
	ReplicationDSN  string        `yaml:"replication_dsn"`  // 转发任务的逻辑复制连接，为空时使用 Worker 的数据库连接的配置。数据库连接经过 PgBouncer 等连接池时需要设置：复制连接必须直连 PostgreSQL。用户需要 REPLICATION 属性。
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"` // 停止时等待处理中的任务完成的最长时间，超时的任务立即放回 JetStream。
}

// DefaultConfig 返回默认配置：stream 为 jobs，单副本，并发 10，最多处理 10 次，租约 30 秒，
// 停止时最多等待 30 秒。
func DefaultConfig() Config {
	return Config{
		Stream:          "jobs",
		Replicas:        1,
		Concurrency:     10,
		MaxAttempts:     10,
		Lease:           30 * time.Second,
		ShutdownTimeout: 30 * time.Second,
	}
}

// SetDefaults 实现 config.Defaulter。
func (c *Config) SetDefaults() { *c = DefaultConfig() }

// Validate 检查各项为正数，shutdown_timeout 可以为 0。
func (c Config) Validate() error {
	var errs []error
	if !validStream(c.Stream) {
		errs = append(errs, errors.Newf("stream %q must be 1 to 32 letters, digits, '_' or '-'", c.Stream))
	}
	if c.Replicas < 1 || c.Replicas > 5 {
		errs = append(errs, errors.New("replicas must be between 1 and 5"))
	}
	if c.Concurrency <= 0 {
		errs = append(errs, errors.New("concurrency must be positive"))
	}
	if c.MaxAttempts <= 0 {
		errs = append(errs, errors.New("max_attempts must be positive"))
	}
	if c.Lease < time.Second {
		errs = append(errs, errors.New("lease must be at least 1s"))
	}
	if c.ShutdownTimeout < 0 {
		errs = append(errs, errors.New("shutdown_timeout must not be negative"))
	}
	return errors.Join(errs...)
}

// validName 报告 s 是否是合法的 topic 名：1 到 64 个字母、数字、'.'、'_' 或 '-'。
// topic 是 NATS subject 的一部分，所以 '.' 分隔的每一段都不能为空。
func validName(s string) bool {
	if s == "" || len(s) > 64 || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") {
		return false
	}
	for _, c := range s {
		if !nameChar(c) && c != '.' {
			return false
		}
	}
	return true
}

// validStream 报告 s 是否是合法的 stream 名：1 到 32 个字母、数字、'_' 或 '-'。
func validStream(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if !nameChar(c) {
			return false
		}
	}
	return true
}

// nameChar 报告 c 能否出现在 stream 名中：字母、数字、_ 和 -。
func nameChar(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}
