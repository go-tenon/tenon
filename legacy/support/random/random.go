// Package random 生成密码学安全的随机值：token、验证码、随机字符串，
// 以及 token 的哈希与校验。
//
// 全部基于 crypto/rand，可用于会话、API key、重置密码链接、短信验证码等场景。
// 生成 ID 请用标准库 uuid.NewV7()（按时间有序，适合做数据库主键）。
//
//	token := random.Token(32)         // 发给客户端
//	hash := random.HashToken(token)   // 只把 hash 存入数据库
//	ok := random.VerifyToken(presented, hash)
//
//	code := random.String(6, random.Digits) // 短信验证码
package random

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	mrand "math/rand/v2"
)

// String 常用的字符集。
const (
	Digits       = "0123456789"
	Alphanumeric = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	// Readable 去掉了容易混淆的 0/O、1/I/L，适合需要人工输入的邀请码、兑换码。
	Readable = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
)

// Bytes 返回 n 个随机字节；n <= 0 时返回空切片。
func Bytes(n int) []byte {
	if n <= 0 {
		return []byte{}
	}
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read 不会返回错误
	return b
}

// Token 返回由 nBytes 个随机字节编码而成的 URL 安全 token（无填充 base64url），
// 可直接放进 URL、Cookie 与 HTTP 头。Token(32) 即 256 位熵、43 个字符。
// nBytes <= 0 时返回空串。
func Token(nBytes int) string {
	return base64.RawURLEncoding.EncodeToString(Bytes(nBytes))
}

// String 返回 n 个从 alphabet 中均匀随机选取的字符（按 rune 处理，无取模偏差）。
// n <= 0 或 alphabet 为空时返回空串。
func String(n int, alphabet string) string {
	chars := []rune(alphabet)
	if n <= 0 || len(chars) == 0 {
		return ""
	}
	r := mrand.New(source{})
	out := make([]rune, n)
	for i := range out {
		out[i] = chars[r.IntN(len(chars))]
	}
	return string(out)
}

// source 是以 crypto/rand 为熵源的 math/rand/v2 Source，
// 借用 math/rand/v2 无偏差的区间取值算法。
type source struct{}

func (source) Uint64() uint64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:])
}

// HashToken 返回 token 的 SHA-256 十六进制摘要，用于在数据库中保存 token 而不保存原文。
//
// token 本身是高熵随机值，因此直接 SHA-256 即可，不需要加盐或使用 bcrypt；
// 用户自己设置的密码熵低，不适用本函数，请使用 support/password。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// VerifyToken 报告 HashToken(token) 是否等于 hash，以常数时间比较。
func VerifyToken(token, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(hash)) == 1
}
