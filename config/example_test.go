package config_test

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/ryan053857/go-kit/config"
)

type Config struct {
	HTTP HTTPConfig `yaml:"http"`
	DB   DBConfig   `yaml:"db"`
}

type HTTPConfig struct {
	Addr    string        `yaml:"addr"`
	Timeout time.Duration `yaml:"timeout"`
}

type DBConfig struct {
	DSN config.Secret `yaml:"dsn"`
}

// DefaultConfig holds the defaults; the YAML file only overrides them.
func DefaultConfig() Config {
	return Config{
		HTTP: HTTPConfig{Addr: ":8080", Timeout: 5 * time.Second},
	}
}

func (c *Config) Validate() error {
	if c.DB.DSN == "" {
		return errors.New("db.dsn is required")
	}
	return nil
}

func Example() {
	os.Setenv("DATABASE_URL", "postgres://app:hunter2@db/app")

	// In a real program this is config.Load(path, &cfg), with path taken
	// from a -config flag.
	cfg := DefaultConfig()
	err := config.Parse([]byte(`
http:
  timeout: 10s
db:
  dsn: ${DATABASE_URL}
`), &cfg)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(cfg.HTTP.Addr, cfg.HTTP.Timeout)
	fmt.Println(cfg.DB.DSN)         // masked when printed
	fmt.Println(string(cfg.DB.DSN)) // real value
	// Output:
	// :8080 10s
	// ******
	// postgres://app:hunter2@db/app
}
