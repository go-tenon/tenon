package main

import (
	"os"

	"github.com/cockroachdb/errors"

	"github.com/ryan053857/mygoravel/config"
	"github.com/ryan053857/mygoravel/database"
)

type appConfig struct {
	HTTP     httpConfig      `yaml:"http"`
	Database database.Config `yaml:"database"`
	Auth     authConfig      `yaml:"auth"`
}

type httpConfig struct {
	Addr string `yaml:"addr"`
}

type authConfig struct {
	// Tokens 是演示用静态 token -> user id。
	Tokens map[string]string `yaml:"tokens"`
}

func defaultConfig() appConfig {
	return appConfig{
		HTTP:     httpConfig{Addr: ":8080"},
		Database: database.DefaultConfig(),
		Auth:     authConfig{Tokens: map[string]string{"dev": "alice"}},
	}
}

func (c httpConfig) Validate() error {
	if c.Addr == "" {
		return errors.New("http.addr is required")
	}
	return nil
}

func (c authConfig) Validate() error {
	if len(c.Tokens) == 0 {
		return errors.New("auth.tokens is required")
	}
	return nil
}

func readDoc(path string, sections ...string) (*config.Document, error) {
	return config.ReadFile(path, config.WithSections(sections...), config.WithLookup(os.LookupEnv))
}
