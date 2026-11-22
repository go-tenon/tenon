package dbtest_test

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-tenon/tenon/database/dbtest"
)

// recordingTB 捕获 CheckSchema 与 CheckComments 报告的错误，而不使外层测试失败。
type recordingTB struct {
	testing.TB
	errors []string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func TestCheckSchemaAndComments(t *testing.T) {
	t.Parallel()
	migrations := fstest.MapFS{
		"2026_01_01_000001_create_tables.sql": {Data: []byte(`-- +goose Up
CREATE TABLE articles (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz,
    title text NOT NULL,
    legacy text
);
COMMENT ON TABLE articles IS '文章';
COMMENT ON COLUMN articles.id IS '主键';
COMMENT ON COLUMN articles.created_at IS '创建时间';
COMMENT ON COLUMN articles.updated_at IS '更新时间';
COMMENT ON COLUMN articles.deleted_at IS '软删除时间';
COMMENT ON COLUMN articles.title IS '标题';

CREATE TABLE article_tags (
    article_id uuid NOT NULL,
    tag text NOT NULL,
    PRIMARY KEY (article_id, tag)
);
COMMENT ON TABLE article_tags IS '文章标签';
COMMENT ON COLUMN article_tags.article_id IS '文章 ID';
COMMENT ON COLUMN article_tags.tag IS '标签名';

CREATE TABLE legacy_raw (
    code text PRIMARY KEY,
    body text NOT NULL
);

-- +goose Down
DROP TABLE legacy_raw;
DROP TABLE article_tags;
DROP TABLE articles;
`)},
		"2026_01_01_000002_alter_articles.sql": {Data: []byte(`-- +goose Up
ALTER TABLE articles DROP COLUMN legacy;
ALTER TABLE articles ADD COLUMN slug text NOT NULL DEFAULT '';
COMMENT ON COLUMN articles.slug IS 'URL 别名';

-- +goose Down
ALTER TABLE articles DROP COLUMN slug;
ALTER TABLE articles ADD COLUMN legacy text;
`)},
	}

	db := dbtest.Open(t, migrations)

	// 豁免 legacy_raw 时，articles（含删列、加列后补注释）与非 id 主键的 article_tags 都应通过。
	dbtest.CheckSchema(t, db, "legacy_raw")

	// 不豁免 legacy_raw 时，应同时报告缺失的表注释和字段注释。
	rec := &recordingTB{TB: t}
	dbtest.CheckComments(rec, db)
	got := strings.Join(rec.errors, "\n")
	for _, want := range []string{
		"legacy_raw: missing table comment",
		"legacy_raw.code: missing column comment",
		"legacy_raw.body: missing column comment",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CheckComments errors:\n%s\nwant substring %q", got, want)
		}
	}
	if len(rec.errors) != 3 {
		t.Errorf("got %d errors, want 3:\n%s", len(rec.errors), got)
	}
}

func TestCheckCommentsRejectsBlankComments(t *testing.T) {
	t.Parallel()
	migrations := fstest.MapFS{
		"2026_01_01_000001_create_items.sql": {Data: []byte(`-- +goose Up
CREATE TABLE items (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
COMMENT ON TABLE items IS '   ';
COMMENT ON COLUMN items.id IS '';
COMMENT ON COLUMN items.created_at IS '创建时间';
COMMENT ON COLUMN items.updated_at IS '更新时间';

-- +goose Down
DROP TABLE items;
`)},
	}

	db := dbtest.Open(t, migrations)
	rec := &recordingTB{TB: t}
	dbtest.CheckSchema(rec, db)
	got := strings.Join(rec.errors, "\n")
	for _, want := range []string{
		"items: missing table comment",
		"items.id: missing column comment",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CheckSchema errors:\n%s\nwant substring %q", got, want)
		}
	}
	if len(rec.errors) != 2 {
		t.Errorf("got %d errors, want 2:\n%s", len(rec.errors), got)
	}
}
