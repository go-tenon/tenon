package migrate

import (
	"bytes"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
)

// VerifyIssue 是 Verify 发现的一条框架迁移差异。
type VerifyIssue struct {
	File   string // 迁移文件名，例如 2026_11_10_114451_create_files.sql
	Kind   string // 差异类型："missing"（尚未发布）或 "modified"（本地内容与框架内嵌不一致）
	Detail string // 问题说明
}

// Verify 检查应用迁移目录 dir 中已发布的框架迁移是否与 sources（功能包的 Migrations()）一致：
//   - 缺少 sources 中新增的迁移时，报告 Kind="missing"；
//   - 本地文件与同版本框架迁移的名称或内容不一致时，报告 Kind="modified"。
//
// 应用自己新增的迁移文件不影响检查结果。适合升级框架后及在 CI 中运行。
func Verify(dir string, sources ...fs.FS) ([]VerifyIssue, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("migrate: verify directory is required")
	}
	files, _, err := collectSources(sources)
	if err != nil {
		return nil, err
	}
	existing, err := migrationFiles(dir)
	if err != nil {
		return nil, err
	}
	var issues []VerifyIssue
	for _, name := range slices.Sorted(maps.Keys(files)) {
		version, _, _ := parseFileName(name)
		localName, ok := existing[version]
		if !ok {
			issues = append(issues, VerifyIssue{
				File:   name,
				Kind:   "missing",
				Detail: "not published in " + dir + "; run migrate publish",
			})
			continue
		}
		if localName != name {
			issues = append(issues, VerifyIssue{
				File:   name,
				Kind:   "modified",
				Detail: fmt.Sprintf("same version exists as %s in %s", localName, dir),
			})
			continue
		}
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, errors.Wrapf(err, "migrate: read %s", name)
		}
		if !bytes.Equal(got, files[name]) {
			issues = append(issues, VerifyIssue{
				File:   name,
				Kind:   "modified",
				Detail: "content differs from embedded framework migration",
			})
		}
	}
	return issues, nil
}
