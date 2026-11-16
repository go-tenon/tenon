package login

import (
	"context"
	"time"
)

// SetClock 供测试替换 Realm 的时钟。
func SetClock(r *Realm, now func() time.Time) { r.now = now }

// TOTP 供测试计算验证码。
func TOTP(key []byte, step int64) string { return totp(key, step) }

// Rehash 供测试模拟登录校验之后、重新哈希之前密码被修改：old 是校验时读到的哈希。
func Rehash(ctx context.Context, r *Realm, subjectID, old, password string) error {
	return r.rehash(ctx, subjectID, old, password)
}
