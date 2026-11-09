// Package password 使用 argon2id 对用户密码做哈希与校验。
//
// golang.org/x/crypto/argon2 只提供底层的密钥派生函数；本包补上盐的生成、
// 参数编码、常数时间校验与参数升级判断。哈希采用通用的 PHC 字符串格式，
// 与 libsodium、Python argon2-cffi、PHP password_hash 等互通：
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
//
// 用法：
//
//	// 注册
//	hash := password.Hash(plain) // 存入数据库
//
//	// 登录
//	ok, err := password.Verify(plain, user.PasswordHash)
//	if err != nil { ... } // 库中的哈希格式有误
//	if ok && password.NeedsRehash(user.PasswordHash) {
//		user.PasswordHash = password.Hash(plain) // 参数升级后逐步迁移旧哈希
//	}
//
// API token、重置链接等高熵随机值不需要 argon2，请用 support/random 的 HashToken。
package password

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/ryan053857/go-kit/support/random"
)

// ErrInvalidHash 表示待校验的哈希不是本包支持的 argon2id PHC 字符串。
var ErrInvalidHash = errors.New("password: invalid argon2id hash")

// Params 是 argon2id 的参数。
type Params struct {
	// Memory 为内存开销，单位 KiB。
	Memory uint32
	// Iterations 为迭代次数，至少为 1。
	Iterations uint32
	// Parallelism 为并行度，至少为 1。
	Parallelism uint8
	// SaltLength 为盐的字节数。
	SaltLength uint32
	// KeyLength 为派生出的哈希字节数。
	KeyLength uint32
}

// DefaultParams 采用 OWASP Password Storage Cheat Sheet 推荐的 argon2id 最低配置
// （m=19 MiB, t=2, p=1）。单次哈希约占 19 MiB 内存、在常见服务器上耗时数十毫秒，
// 适合登录接口有并发的 Web 服务。
var DefaultParams = Params{
	Memory:      19 * 1024,
	Iterations:  2,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

// Hash 用 DefaultParams 对 password 做哈希，返回 PHC 格式字符串。
func Hash(password string) string {
	return DefaultParams.Hash(password)
}

// NeedsRehash 报告 encoded 是否不是用 DefaultParams 生成的，
// 用于在调高参数后，在用户下次登录成功时重新哈希。
func NeedsRehash(encoded string) bool {
	return DefaultParams.NeedsRehash(encoded)
}

// Hash 用参数 p 对 password 做哈希，返回 PHC 格式字符串。
// Iterations 或 Parallelism 为 0 时按 1 处理。
func (p Params) Hash(password string) string {
	p.Iterations = max(p.Iterations, 1)
	p.Parallelism = max(p.Parallelism, 1)
	salt := random.Bytes(int(p.SaltLength))
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

// NeedsRehash 报告 encoded 的参数是否与 p 不同；无法解析时也返回 true。
func (p Params) NeedsRehash(encoded string) bool {
	got, _, key, err := decode(encoded)
	if err != nil {
		return true
	}
	return got.Memory != p.Memory || got.Iterations != p.Iterations ||
		got.Parallelism != p.Parallelism || got.SaltLength != p.SaltLength ||
		uint32(len(key)) != p.KeyLength
}

// Verify 报告 password 是否与 encoded 匹配，以常数时间比较。
// encoded 无法解析时返回 ErrInvalidHash。
// 校验使用 encoded 中记录的参数，因此参数升级后旧哈希仍能校验通过。
func Verify(password, encoded string) (bool, error) {
	p, salt, key, err := decode(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

// decode 解析 PHC 字符串。
func decode(encoded string) (p Params, salt, key []byte, err error) {
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, ErrInvalidHash
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	// argon2.IDKey panics on zero iterations or parallelism.
	if p.Iterations < 1 || p.Parallelism < 1 || len(salt) == 0 || len(key) == 0 {
		return p, nil, nil, ErrInvalidHash
	}
	p.SaltLength = uint32(len(salt))
	p.KeyLength = uint32(len(key))
	return p, salt, key, nil
}
