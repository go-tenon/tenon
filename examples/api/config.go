package main

import (
	"net/netip"
	"os"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/ryan053857/mygoravel/config"
	"github.com/ryan053857/mygoravel/database"
)

type appConfig struct {
	HTTP     httpConfig      `yaml:"http" json:"http"`
	Database database.Config `yaml:"database" json:"database"`
	Auth     authConfig      `yaml:"auth" json:"auth"`
	Cache    cacheConfig     `yaml:"cache" json:"cache"`
}
type httpConfig struct {
	Addr            string        `yaml:"addr" json:"addr"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout"`
	RequestTimeout  time.Duration `yaml:"request_timeout" json:"request_timeout"`
	MaxBodyBytes    int64         `yaml:"max_body_bytes" json:"max_body_bytes"`
	TrustedProxies  []string      `yaml:"trusted_proxies" json:"trusted_proxies"`
	DefaultLanguage string        `yaml:"default_language" json:"default_language"`
}
type credential struct {
	Token     config.Secret `yaml:"token" json:"token"`
	SubjectID string        `yaml:"subject_id" json:"subject_id"`
}
type authConfig struct {
	Credentials []credential `yaml:"credentials" json:"credentials"`
}
type cacheConfig struct {
	Driver    string        `yaml:"driver" json:"driver"`
	Addr      string        `yaml:"addr" json:"addr"`
	Namespace string        `yaml:"namespace" json:"namespace"`
	TTL       time.Duration `yaml:"ttl" json:"ttl"`
}

func defaultConfig() appConfig {
	return appConfig{
		HTTP:     httpConfig{Addr: ":8080", ShutdownTimeout: 10 * time.Second, RequestTimeout: 30 * time.Second, MaxBodyBytes: 1 << 20, DefaultLanguage: "en"},
		Database: database.DefaultConfig(),
		Cache:    cacheConfig{Driver: "none", Namespace: "orders", TTL: time.Minute},
	}
}
func (c httpConfig) Validate() error {
	var errs []error
	if c.Addr == "" {
		errs = append(errs, errors.New("http.addr is required"))
	}
	if c.ShutdownTimeout < 0 || c.RequestTimeout <= 0 || c.MaxBodyBytes <= 0 {
		errs = append(errs, errors.New("http limits must be positive; shutdown_timeout may be zero"))
	}
	if c.DefaultLanguage != "en" && c.DefaultLanguage != "zh-CN" {
		errs = append(errs, errors.New("http.default_language must be en or zh-CN"))
	}
	for _, p := range c.TrustedProxies {
		if _, err := netip.ParsePrefix(p); err != nil {
			errs = append(errs, errors.New("http.trusted_proxies must contain CIDR prefixes"))
		}
	}
	return errors.Join(errs...)
}
func (c authConfig) Validate() error {
	if len(c.Credentials) == 0 {
		return errors.New("auth.credentials is required")
	}
	seen := map[config.Secret]bool{}
	for _, v := range c.Credentials {
		if v.Token == "" || v.SubjectID == "" {
			return errors.New("auth credentials require token and subject_id")
		}
		if seen[v.Token] {
			return errors.New("auth credentials contain duplicate tokens")
		}
		seen[v.Token] = true
	}
	return nil
}
func (c cacheConfig) Validate() error {
	switch c.Driver {
	case "none":
		return nil
	case "memory":
	case "redis":
		if c.Addr == "" {
			return errors.New("cache.addr is required for redis")
		}
	default:
		return errors.New("cache.driver must be none, memory or redis")
	}
	if c.TTL <= 0 || c.Namespace == "" {
		return errors.New("cache requires positive ttl and namespace")
	}
	return nil
}
func readDoc(path string, sections ...string) (*config.Document, error) {
	return config.ReadFile(path, config.WithSections(sections...), config.WithLookup(os.LookupEnv))
}
