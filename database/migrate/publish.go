package migrate

import (
	"bytes"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
)

const sourceMarker = "-- mygoravel: "

var sourceName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// Source 是一个显式登记的迁移来源。Name 是稳定标识，不应包含框架版本号；
// FS 根目录直接包含带数字版本前缀的 SQL 文件，例如 0001_create_files.sql。
// Name 和源文件名、版本一经发布不可改变，升级只能追加更高版本的迁移。
type Source struct {
	Name string
	FS   fs.FS
}

type publication struct {
	source  string
	name    string
	marker  string
	version int64
	data    []byte
}

// Publish 将 sources 尚未发布的迁移追加到 dir，返回本次生成的文件名。
// 来源按参数顺序处理，每个来源内按数字版本排序。来源标记相同的迁移会跳过，
// 不比较或覆盖应用维护的 SQL；框架升级应新增源迁移。发布不会连接数据库。
//
// 新版本从当前 UTC 时间戳和目录已有最大版本之后分配。单个文件原子发布，
// 整批不是事务；中途发生 I/O 错误时，返回已发布的文件，重试会跳过这些文件。
func Publish(dir string, sources ...Source) ([]string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("migrate: publish directory is required")
	}
	items, err := publications(sources)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, errors.Wrap(err, "migrate: create publish directory")
	}
	// 锁文件保持原位，避免删除后另一个进程锁到不同 inode；关闭文件自动释放锁。
	lock, err := lockPublish(filepath.Join(dir, ".mygoravel-publish.lock"))
	if err != nil {
		return nil, errors.Wrap(err, "migrate: lock publish directory")
	}
	defer lock.Close()

	published, maxVersion, err := publishedSources(dir)
	if err != nil {
		return nil, err
	}
	if err := validateSourceHistory(items, published); err != nil {
		return nil, err
	}
	pending := make([]publication, 0, len(items))
	for _, item := range items {
		if _, exists := published[item.marker]; !exists {
			pending = append(pending, item)
		}
	}
	if len(pending) == 0 {
		return nil, nil
	}
	version, _ := strconv.ParseInt(time.Now().UTC().Format("20060102150405"), 10, 64)
	if maxVersion >= version {
		version, err = nextMigrationVersion(maxVersion)
		if err != nil {
			return nil, err
		}
	}
	// 先分配整批名称，版本溢出时不写入半批迁移。
	names := make([]string, len(pending))
	for i, item := range pending {
		names[i] = fmt.Sprintf("%d_%s_%s", version, item.source, item.name)
		if i+1 < len(pending) {
			version, err = nextMigrationVersion(version)
			if err != nil {
				return nil, err
			}
		}
	}

	var written []string
	for i, item := range pending {
		name := names[i]
		content := append([]byte(sourceMarker+item.marker+"\n"), item.data...)
		if err := publishFile(dir, name, content); err != nil {
			return written, errors.Wrapf(err, "migrate: publish %s", name)
		}
		written = append(written, name)
	}
	return written, nil
}

// nextMigrationVersion 对合法的四位年份 UTC 时间戳按秒递增，其他数字版本按一递增。
func nextMigrationVersion(version int64) (int64, error) {
	if version == math.MaxInt64 {
		return 0, errors.New("migrate: migration version overflow")
	}
	stamp := strconv.FormatInt(version, 10)
	if len(stamp) == 14 {
		if date, err := time.Parse("20060102150405", stamp); err == nil {
			next := date.Add(time.Second)
			if next.Year() <= 9999 {
				return strconv.ParseInt(next.Format("20060102150405"), 10, 64)
			}
		}
	}
	return version + 1, nil
}

// validateSourceHistory 阻止同一来源改名重发旧版本，或在已有迁移之后插入更早的版本。
// 应用发布文件本身的名称和 SQL 可以调整，校验只依据保留的来源标记。
func validateSourceHistory(items []publication, published map[string]string) error {
	type history struct {
		versions map[int64]string
		maximum  int64
	}
	sources := make(map[string]*history)
	for _, item := range items {
		if sources[item.source] == nil {
			sources[item.source] = &history{versions: make(map[int64]string)}
		}
	}
	for marker := range published {
		source, name, _ := strings.Cut(marker, "/")
		h := sources[source]
		if h == nil {
			continue
		}
		version, err := migrationVersion(name + ".sql")
		if err != nil {
			return errors.Wrapf(err, "migrate: invalid source marker %q", marker)
		}
		if previous, ok := h.versions[version]; ok {
			return errors.Newf("migrate: duplicate source version in %q and %q", previous, marker)
		}
		h.versions[version] = marker
		h.maximum = max(h.maximum, version)
	}
	for _, item := range items {
		if _, exists := published[item.marker]; exists {
			continue
		}
		h := sources[item.source]
		if previous, exists := h.versions[item.version]; exists {
			return errors.Newf("migrate: source migration %q renames published %q", item.marker, previous)
		}
		if item.version < h.maximum {
			return errors.Newf("migrate: source migration %q precedes published version %d", item.marker, h.maximum)
		}
	}
	return nil
}

// publications 在写入目标目录之前读取和校验全部迁移来源。
func publications(sources []Source) ([]publication, error) {
	if len(sources) == 0 {
		return nil, errors.New("migrate: at least one migration source is required")
	}
	seen := make(map[string]bool)
	var items []publication
	for _, source := range sources {
		if !sourceName.MatchString(source.Name) {
			return nil, errors.Newf("migrate: invalid source name %q", source.Name)
		}
		if seen[source.Name] {
			return nil, errors.Newf("migrate: duplicate source %q", source.Name)
		}
		seen[source.Name] = true
		if source.FS == nil {
			return nil, errors.Newf("migrate: source %s has nil filesystem", source.Name)
		}
		entries, err := fs.ReadDir(source.FS, ".")
		if err != nil {
			return nil, errors.Wrapf(err, "migrate: read source %s", source.Name)
		}
		var group []publication
		versions := make(map[int64]string)
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			if !fs.ValidPath(entry.Name()) || strings.ContainsAny(entry.Name(), "/\\\r\n") {
				return nil, errors.Newf("migrate: invalid source filename %q", entry.Name())
			}
			if !entry.Type().IsRegular() {
				return nil, errors.Newf("migrate: source %s/%s is not a regular SQL file", source.Name, entry.Name())
			}
			version, err := migrationVersion(entry.Name())
			if err != nil {
				return nil, err
			}
			if previous, ok := versions[version]; ok {
				return nil, errors.Newf("migrate: source %s has duplicate version in %s and %s", source.Name, previous, entry.Name())
			}
			versions[version] = entry.Name()
			data, err := fs.ReadFile(source.FS, entry.Name())
			if err != nil {
				return nil, errors.Wrapf(err, "migrate: read %s/%s", source.Name, entry.Name())
			}
			if !bytes.Contains(data, []byte("-- +goose Up")) {
				return nil, errors.Newf("migrate: %s/%s is missing goose Up annotation", source.Name, entry.Name())
			}
			group = append(group, publication{
				source: source.Name, name: entry.Name(), version: version, data: data,
				marker: source.Name + "/" + strings.TrimSuffix(entry.Name(), ".sql"),
			})
		}
		slices.SortFunc(group, func(a, b publication) int {
			if a.version < b.version {
				return -1
			}
			if a.version > b.version {
				return 1
			}
			return 0
		})
		items = append(items, group...)
	}
	return items, nil
}

func migrationVersion(name string) (int64, error) {
	versionText, description, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
	if !ok || description == "" || !strings.HasSuffix(name, ".sql") {
		return 0, errors.Newf("migrate: invalid SQL migration filename %q", name)
	}
	for _, ch := range versionText {
		if ch < '0' || ch > '9' {
			return 0, errors.Newf("migrate: invalid migration version in %q", name)
		}
	}
	version, err := strconv.ParseInt(versionText, 10, 64)
	if err != nil || version <= 0 {
		return 0, errors.Newf("migrate: invalid migration version in %q", name)
	}
	return version, nil
}

// publishedSources 只读取根目录，与 goose 的线性 SQL 目录保持一致。
func publishedSources(dir string) (map[string]string, int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, errors.Wrap(err, "migrate: read publish directory")
	}
	published := make(map[string]string)
	versions := make(map[int64]string)
	var maxVersion int64
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		if !entry.Type().IsRegular() {
			return nil, 0, errors.Newf("migrate: %s is not a regular SQL file", entry.Name())
		}
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return nil, 0, err
		}
		if previous, ok := versions[version]; ok {
			return nil, 0, errors.Newf("migrate: duplicate version in %s and %s", previous, entry.Name())
		}
		versions[version] = entry.Name()
		maxVersion = max(maxVersion, version)
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, 0, errors.Wrapf(err, "migrate: read %s", entry.Name())
		}
		line, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
		marker, ok := strings.CutPrefix(strings.TrimSpace(line), sourceMarker)
		if !ok {
			continue
		}
		marker = strings.TrimSpace(marker)
		if marker == "" {
			return nil, 0, errors.Newf("migrate: empty source marker in %s", entry.Name())
		}
		if previous, ok := published[marker]; ok {
			return nil, 0, errors.Newf("migrate: duplicate source marker %q in %s and %s", marker, previous, entry.Name())
		}
		published[marker] = entry.Name()
	}
	return published, maxVersion, nil
}

// publishFile 先完整写入同目录临时文件，再以硬链接独占创建正式名称。
// Link 不覆盖已有文件，读者也不会看到只有来源标记的半份 SQL。
func publishFile(dir, name string, data []byte) error {
	f, err := os.CreateTemp(dir, ".mygoravel-publish-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Link(f.Name(), filepath.Join(dir, name))
}
