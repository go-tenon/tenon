package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/cockroachdb/errors"
)

var (
	migrationName = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	createPattern = regexp.MustCompile(`^create_(\w+?)(?:_table)?$`)
	alterPattern  = regexp.MustCompile(`^.+_(?:to|from|in)_(\w+?)(?:_table)?$`)
)

// Create 在 dir 中新建一个迁移，返回文件路径。name 必须是 snake_case，例如
// create_articles_table 或 add_published_at_to_articles_table；文件名与 Laravel 相同，是
// UTC 时间加名称，例如 2026_11_17_123456_create_articles_table.sql，版本保证大于目录中已有的最大版本。
//
// Create 从 name 推断表名并生成对应的骨架：create_<table>[_table]
// 生成 CREATE TABLE，主键是由应用用 uuid.NewV7() 生成的 UUID；..._to|from|in_<table>[_table] 生成 ALTER TABLE，其余只有 goose 注解。
func Create(dir, name string) (string, error) {
	if !migrationName.MatchString(name) {
		return "", errors.Newf("migrate: migration name %q must be snake_case, for example create_articles_table", name)
	}
	_, maxVersion, err := publishedSources(dir)
	if err != nil {
		return "", err
	}
	version, err := nextVersion(maxVersion)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", errors.Wrap(err, "migrate: create migration directory")
	}
	path := filepath.Join(dir, formatVersion(version)+"_"+name+".sql")
	if err := publishFile(path, []byte(stub(name))); err != nil {
		return "", errors.Wrapf(err, "migrate: create %s", filepath.Base(path))
	}
	return path, nil
}

// createStub 是建表骨架。时间都由应用经 Bun 写入，不设数据库默认值。
const createStub = `-- +goose Up
CREATE TABLE %[1]s (
    id         uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE %[1]s;
`

// stub 按 name 推断的表名返回迁移骨架。
func stub(name string) string {
	if m := createPattern.FindStringSubmatch(name); m != nil {
		return fmt.Sprintf(createStub, m[1])
	}
	if m := alterPattern.FindStringSubmatch(name); m != nil {
		return fmt.Sprintf(`-- +goose Up
ALTER TABLE %[1]s;

-- +goose Down
ALTER TABLE %[1]s;
`, m[1])
	}
	return "-- +goose Up\n\n-- +goose Down\n"
}

// versionLayout 是版本的时间格式，例如 20261117123456。
const versionLayout = "20060102150405"

// nextVersion 返回当前 UTC 时间形式的版本，并保证大于 maxVersion：maxVersion 不早于现在时，
// 取它之后的一秒（与 Laravel 相同，123459 之后是 123500）；maxVersion 不是合法的时间时加一。
// 超过 lastVersion 时返回错误。
func nextVersion(maxVersion int64) (int64, error) {
	version, _ := strconv.ParseInt(time.Now().UTC().Format(versionLayout), 10, 64)
	if version <= maxVersion {
		version = maxVersion + 1
		if t, err := time.Parse(versionLayout, fmt.Sprintf("%014d", maxVersion)); err == nil {
			version, _ = strconv.ParseInt(t.Add(time.Second).Format(versionLayout), 10, 64)
		}
	}
	if version > lastVersion {
		return 0, errors.New("migrate: migration version overflow")
	}
	return version, nil
}
