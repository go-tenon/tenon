package files

import (
	"time"

	"github.com/cockroachdb/errors"
)

// Config 是文件上传限制。所有文件均通过服务登记到 files 表。
type Config struct {
	DefaultDisk    string        `yaml:"default_disk"`
	MaxUploadBytes int64         `yaml:"max_upload_bytes"`
	PartSize       int64         `yaml:"part_size"`
	TempTTL        time.Duration `yaml:"temp_ttl"`
	SignTTL        time.Duration `yaml:"sign_ttl"`
}

// DefaultConfig 返回默认上传限制；默认使用名为 local 的存储配置。
func DefaultConfig() Config {
	return Config{DefaultDisk: "local", MaxUploadBytes: 5 << 30, PartSize: 8 << 20, TempTTL: 24 * time.Hour, SignTTL: 15 * time.Minute}
}

func (c *Config) SetDefaults() { *c = DefaultConfig() }

// Validate 检查上传大小、分片数量和会话期限。
func (c Config) Validate() error {
	if c.DefaultDisk == "" {
		return errors.New("default_disk is required")
	}
	if c.PartSize < MinPartSize || c.PartSize > MaxPartSize {
		return errors.New("part_size must be between 5 MiB and 5 GiB")
	}
	if c.MaxUploadBytes <= 0 || (c.MaxUploadBytes-1)/c.PartSize+1 > MaxParts {
		return errors.New("max_upload_bytes must be positive and fit in 10000 parts")
	}
	if c.TempTTL <= 0 || c.SignTTL < time.Second || c.SignTTL > 7*24*time.Hour || c.SignTTL > c.TempTTL {
		return errors.New("sign_ttl must be between 1s and 7d and no greater than temp_ttl")
	}
	return nil
}
