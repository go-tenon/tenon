package logintest

import (
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/hashing"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/ratelimit"
)

// Realm 返回测试用的 login.Realm：opts 中没有设置的 Hasher、Crypt、Attempts、Sends 用适合测试的值补上——
// 最低成本的 bcrypt（生产的哈希配置在每个测试中都很慢）、随机的应用密钥、每分钟 1000 次的内存限流（不同的测试互不影响）。
// opts.Type 必须设置；其余字段（Policy、Resolve、Send 等）按测试的需要设置，例如 Send 用 Outbox.Send。
func Realm(t testing.TB, db bun.IDB, opts login.Options) *login.Realm {
	t.Helper()
	if opts.Hasher == nil {
		opts.Hasher = hashing.New(hashing.Config{Driver: "bcrypt", Bcrypt: hashing.BcryptConfig{Cost: 4}})
	}
	if opts.Crypt == nil {
		opts.Crypt = crypt.New(crypt.Config{Key: crypt.GenerateKey()})
	}
	loose := ratelimit.Config{Rate: 1000, Interval: time.Minute, Burst: 1000}
	if opts.Attempts == nil {
		opts.Attempts = ratelimit.NewMemory(loose)
	}
	if opts.Sends == nil {
		opts.Sends = ratelimit.NewMemory(loose)
	}
	return login.New(db, opts)
}
