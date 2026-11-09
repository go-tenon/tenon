// Package crypt 用一个密钥对数据做对称加密与解密。
//
// 算法为 AES-256-GCM：既保密，也防篡改，被改动过的密文无法解密。
// 每次加密都使用标准库生成的随机 nonce，并放在密文开头，
// 因此同一明文每次加密的结果都不同，调用方也无需管理 nonce。
//
// 用法：
//
//	// 生成密钥：openssl rand -base64 32，放进配置（config.Secret）
//	key, err := crypt.ParseKey(cfg.AppKey.Value())
//	c, err := crypt.New(key)
//
//	token := c.EncryptString(accessToken)   // 存库、写 Cookie、放 URL 均可
//	accessToken, err := c.DecryptString(token)
//
// 注意：
//
//   - 用户密码请用 support/password，不要加密存储；
//     API token 等只需比对、不需还原的值请用 support/random.HashToken。
//   - 同一个密钥加密次数不应超过 2^32 次（随机 nonce 的安全上限），
//     对普通业务数据绰绰有余。
//   - 本包不支持密钥轮换：更换密钥前需要用旧密钥解密、新密钥重新加密已有数据。
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"

	"github.com/cockroachdb/errors"
)

// KeySize 是密钥的字节数（AES-256）。
const KeySize = 32

var (
	// ErrInvalidKey 表示密钥长度不是 KeySize，或不是合法的 base64。
	ErrInvalidKey = errors.New("crypt: key must be 32 bytes")
	// ErrDecrypt 表示密文格式有误、被篡改，或不是用同一密钥加密的。
	ErrDecrypt = errors.New("crypt: message authentication failed")
)

// ParseKey 解析标准 base64 编码的密钥，例如 `openssl rand -base64 32` 的输出。
// 解码后不是 KeySize 字节时返回 ErrInvalidKey。
func ParseKey(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(key) != KeySize {
		return nil, ErrInvalidKey
	}
	return key, nil
}

// Cipher 用固定密钥加解密，可以被多个 goroutine 并发使用。
type Cipher struct {
	aead cipher.AEAD
}

// New 返回使用 key 的 Cipher。key 必须是 KeySize 字节，否则返回 ErrInvalidKey。
func New(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 加密 plaintext，返回 nonce、密文与认证标签拼接而成的字节，
// 比明文长 28 字节。不修改 plaintext。
func (c *Cipher) Encrypt(plaintext []byte) []byte {
	return c.aead.Seal(nil, nil, plaintext, nil)
}

// Decrypt 解密 Encrypt 的结果。密文过短、被篡改或密钥不符时返回 ErrDecrypt。
func (c *Cipher) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < c.aead.Overhead() {
		return nil, ErrDecrypt
	}
	plaintext, err := c.aead.Open(nil, nil, ciphertext, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// EncryptString 加密 s，并编码为无填充的 base64url 字符串，
// 可以直接存入文本字段、Cookie 或 URL。
func (c *Cipher) EncryptString(s string) string {
	return base64.RawURLEncoding.EncodeToString(c.Encrypt([]byte(s)))
}

// DecryptString 解密 EncryptString 的结果。
// 不是合法的 base64url、被篡改或密钥不符时返回 ErrDecrypt。
func (c *Cipher) DecryptString(s string) (string, error) {
	ciphertext, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", ErrDecrypt
	}
	plaintext, err := c.Decrypt(ciphertext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
