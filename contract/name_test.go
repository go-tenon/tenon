package contract

import (
	"log/slog"
	"net/url"
	"reflect"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/page"
)

func TestQualify(t *testing.T) {
	t.Parallel()
	for part, want := range map[string]string{
		"github.com/acme/blog/article.Changes":          "ArticleChanges",
		"github.com/acme/blog/comment.Changes":          "CommentChanges",
		"github.com/acme/blog/article.ArticleDTO":       "ArticleDTO",
		"github.com/acme/blog/apitoken.APIToken":        "APIToken",
		"github.com/acme/blog/article_test.Item":        "ArticleItem",
		"github.com/acme/go-blog/v2.Item":               "GoblogItem",
		"github.com/acme/blog/article.changes":          "ArticleChanges",
		"main.Item":                                     "Item",
		"int":                                           "Int",
		"github.com/danielgtaylor/huma/v2.ErrorDetail":  "ErrorDetail",
		"github.com/go-tenon/tenon/page.Offset":         "Offset",
		"github.com/go-tenon/tenon/apitoken/jwt.Pair":   "Pair",
		"github.com/go-tenon/tenonx/article.ArticleDTO": "ArticleDTO",
		"github.com/go-tenon/tenonx/article.Changes":    "ArticleChanges",
	} {
		if got := qualify(part); got != want {
			t.Errorf("qualify(%q) = %q, want %q", part, got, want)
		}
	}
}

func TestSchemaName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ  any
		hint string
		want string
	}{
		{slog.Level(0), "", "SlogLevel"},
		{&url.URL{}, "", "URL"},
		{page.Offset[slog.Level]{}, "", "OffsetSlogLevel"},
		{FieldError{}, "", "FieldError"},
		{huma.ErrorDetail{}, "", "ErrorDetail"},
		{struct{}{}, "publishArticleRequest", "PublishArticleRequest"},
	} {
		if got := SchemaName(reflect.TypeOf(c.typ), c.hint); got != c.want {
			t.Errorf("SchemaName(%T) = %q, want %q", c.typ, got, c.want)
		}
	}
}
