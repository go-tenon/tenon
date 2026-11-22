package search

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/page"
)

// DefaultPerPage 是 Query.Page.PerPage 不大于 0 时每页的条数。
const DefaultPerPage = 20

// DefaultFacetLimit 是 Query.FacetLimit 不大于 0 时每个分面最多返回的值的个数。
const DefaultFacetLimit = 20

// ByKey 用于 Query.Collapse，按文档的 key 折叠结果，适合一个 key 对应多个分段的索引。
const ByKey = "$key"

// Query 是一次搜索的条件。
type Query struct {
	Text       string            // 用户输入的查询文本，切分出的每个词都要出现在文档中，最后一个词按前缀匹配（输入到一半也能搜到）。为空或者没有切分出词时不按文本过滤，相当于列出符合 Filter 的所有文档。
	Tenant     string            // 查询的租户，"" 为平台本身。文档类型有 tenant 字段时总是只查询这个租户的文档；没有 tenant 字段时必须为空。
	Filter     Filter            // nil 时不过滤
	Sort       []Sort            // 为空时有查询文本的按相关度排序，否则按 key 排序；可用 Relevance 指定相关度在排序中的位置
	Collapse   string            // 按指定字段把结果折叠为每组一条：ByKey 按文档 key 折叠，单值 filter 字段名按该字段的值折叠，留空不折叠
	Facets     []string          // 统计这些过滤字段在结果中各个值的文档数
	FacetLimit int               // 每个分面最多返回的值的个数，按文档数从多到少；不大于 0 时为 DefaultFacetLimit。
	Page       page.OffsetParams // 分页参数，零值为第 1 页、每页 DefaultPerPage 条。
}

// Hit 是一条搜索结果。
type Hit[D any] struct {
	Key        string             `json:"key"`                   // 文档所属的 key，即 Sync 时传入的 key
	Doc        D                  `json:"doc"`                   // 同步时保存的文档
	Score      float64            `json:"score"`                 // 相关度，越大越相关；不同数据库的计算方法不同，只用于比较同一次查询的结果
	GroupCount int                `json:"group_count,omitempty"` // 启用 Query.Collapse 时，这一组命中的文档总数；未折叠时为 0
	Highlights map[string]Snippet `json:"highlights,omitempty"`  // 命中查询的文本字段的摘要，按字段的 JSON 名称索引；没有查询文本时为空。
}

// Result 是一页搜索结果。
type Result[D any] struct {
	page.Offset[Hit[D]]
	Facets map[string][]FacetValue `json:"facets,omitempty"` // 按 Query.Facets 中的字段名索引
}

// FacetValue 是一个分面值和结果中带有这个值的文档数。
type FacetValue struct {
	Value string `json:"value"` // 数字、时间转换为文字，例如 42、2026-11-12T08:00:00Z
	Count int    `json:"count"`
}

// Sort 是一个排序条件，用 Asc、Desc 或 Relevance 创建。
type Sort struct {
	Field     string
	Desc      bool
	relevance bool
}

// Asc 按过滤字段 field 从小到大排序，没有值的排在最后。field 是单值字段。
func Asc(field string) Sort { return Sort{Field: field} }

// Desc 按过滤字段 field 从大到小排序，没有值的排在最后。field 是单值字段。
func Desc(field string) Sort { return Sort{Field: field, Desc: true} }

// Relevance 按全文检索的相关度从高到低排序；没有查询文本时忽略。
func Relevance() Sort { return Sort{Desc: true, relevance: true} }

// Search 在 db 中查询 ix，返回 q.Page 指定的一页结果。
func (ix *Index[D]) Search(ctx context.Context, db bun.IDB, q Query) (*Result[D], error) {
	if ix.schema.tenant == nil && q.Tenant != "" {
		return nil, errors.Newf("search: index %s has no tenant field", ix.name)
	}
	if q.Collapse != "" && q.Collapse != ByKey {
		f := ix.schema.filters[q.Collapse]
		if f == nil || f.multi {
			return nil, errors.Newf("search: cannot collapse by %q: not a single-valued filter field", q.Collapse)
		}
	}
	for _, s := range q.Sort {
		if s.relevance {
			continue
		}
		f := ix.schema.filters[s.Field]
		if f == nil || f.multi {
			return nil, errors.Newf("search: cannot sort by %q: not a single-valued filter field", s.Field)
		}
	}
	p := q.Page
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PerPage < 1 {
		p.PerPage = DefaultPerPage
	}
	terms := parseQuery(ix.tok, q.Text)
	text := len(terms.terms) > 0

	var where sqlExpr
	// 只查询 search_indexes 中记录的版本，没有记录时为 1，见 versions.go
	where.add("e.index_name = ? AND e.version = COALESCE((SELECT i.version FROM search_indexes AS i WHERE i.name = ?), 1)"+
		" AND e.tenant = ?", ix.name, ix.name, q.Tenant)
	var tsq string
	if text {
		tsq = tsquery(terms)
		where.add(" AND e.tokens @@ ?::tsquery", tsq)
	}
	if q.Filter != nil {
		where.add(" AND ")
		if err := q.Filter.build(ix.schema, &where); err != nil {
			return nil, err
		}
	}
	const from = "search_entries AS e"

	type hitRow struct {
		DocKey     string  `bun:"doc_key"`
		Doc        string  `bun:"doc"`
		Score      float64 `bun:"score"`       // 没有查询文本时不查询，为 0
		GroupCount int     `bun:"group_count"` // 未折叠时不查询，为 0
	}
	var total int64
	var rows []hitRow
	if q.Collapse == "" {
		if err := db.NewRaw("SELECT COUNT(*) FROM "+from+" WHERE "+where.sql(), where.args...).Scan(ctx, &total); err != nil {
			return nil, errors.Wrap(err, "search: count")
		}
		columns, scoreArgs := "e.doc_key, e.doc", []any(nil)
		if text {
			columns, scoreArgs = columns+", ts_rank(e.tokens, ?::tsquery) AS score", []any{tsq}
		}
		order := flatOrder(q.Sort, text)
		args := append(append(append(scoreArgs, where.args...), order.args...), p.Limit(), p.Offset())
		err := db.NewRaw("SELECT "+columns+" FROM "+from+" WHERE "+where.sql()+
			" ORDER BY "+order.sql()+" LIMIT ? OFFSET ?", args...).Scan(ctx, &rows)
		if err != nil {
			return nil, errors.Wrap(err, "search: query")
		}
	} else {
		grp := collapseExpr(q.Collapse)
		countArgs := append(append([]any(nil), grp.args...), where.args...)
		if err := db.NewRaw("SELECT COUNT(DISTINCT "+grp.sql()+") FROM "+from+" WHERE "+where.sql(), countArgs...).Scan(ctx, &total); err != nil {
			return nil, errors.Wrap(err, "search: count")
		}
		cte, orderSQL := collapsedCTE(from, where, grp, q.Sort, text, tsq)
		selectCols := "doc_key, doc, group_count"
		if text {
			selectCols = "doc_key, doc, score, group_count"
		}
		args := append(append([]any(nil), cte.args...), p.Limit(), p.Offset())
		err := db.NewRaw(cte.sql()+" SELECT "+selectCols+" FROM ranked WHERE rn = 1 ORDER BY "+orderSQL+" LIMIT ? OFFSET ?", args...).Scan(ctx, &rows)
		if err != nil {
			return nil, errors.Wrap(err, "search: query")
		}
	}

	hits := make([]Hit[D], len(rows))
	for i, r := range rows {
		h := Hit[D]{Key: r.DocKey, Score: r.Score, GroupCount: r.GroupCount}
		if err := json.Unmarshal([]byte(r.Doc), &h.Doc); err != nil {
			return nil, errors.Wrapf(err, "search: decode %s document %q", ix.name, r.DocKey)
		}
		if text {
			h.Highlights = ix.highlights(terms, h.Doc)
		}
		hits[i] = h
	}
	res := &Result[D]{Offset: page.NewOffset(hits, p, total)}
	if len(q.Facets) > 0 {
		var err error
		if res.Facets, err = ix.facets(ctx, db, q, from, where, text, tsq); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// flatOrder 构造未折叠查询的 ORDER BY 表达式：保留 Relevance 的位置，未指定 Relevance 时在有查询文本时附在末尾。
func flatOrder(sorts []Sort, text bool) sqlExpr {
	var order sqlExpr
	hasRelevance := false
	for _, s := range sorts {
		if s.relevance {
			hasRelevance = true
			if text {
				order.add("score DESC, ")
			}
			continue
		}
		value := "(SELECT v.value FROM search_values AS v WHERE v.entry_id = e.id AND v.field = ?)"
		dir := "ASC"
		if s.Desc {
			dir = "DESC"
		}
		order.add(value+" IS NULL, "+value+" "+dir+", ", s.Field, s.Field)
	}
	if text && !hasRelevance {
		order.add("score DESC, ")
	}
	order.add("e.doc_key, e.position")
	return order
}

// collapseExpr 返回按 field 折叠时的分组键 SQL 表达式。没有值的文档各自独立成组，避免多个 NULL 合并为一组。
func collapseExpr(field string) sqlExpr {
	var e sqlExpr
	if field == ByKey {
		e.add("e.doc_key")
		return e
	}
	e.add("COALESCE('v:' || (SELECT v.value FROM search_values AS v WHERE v.entry_id = e.id AND v.field = ?), 'n:' || e.id::text)", field)
	return e
}

// collapsedCTE 构造折叠查询用的 WITH base, ranked 语句以及供窗口和外层共用的 ORDER BY 片段。
func collapsedCTE(from string, where, grp sqlExpr, sorts []Sort, text bool, tsq string) (sqlExpr, string) {
	var cte sqlExpr
	cte.add("WITH base AS (SELECT e.id, e.doc_key, e.position, e.doc, "+grp.sql()+" AS grp", grp.args...)
	if text {
		cte.add(", ts_rank(e.tokens, ?::tsquery) AS score", tsq)
	}
	var orderParts []string
	var sortCols []string
	hasRelevance := false
	sortIdx := 0
	for _, s := range sorts {
		if s.relevance {
			hasRelevance = true
			if text {
				orderParts = append(orderParts, "score DESC")
			}
			continue
		}
		col := fmt.Sprintf("s%d", sortIdx)
		sortIdx++
		cte.add(", (SELECT v.value FROM search_values AS v WHERE v.entry_id = e.id AND v.field = ?) AS "+col, s.Field)
		sortCols = append(sortCols, col)
		dir := "ASC"
		if s.Desc {
			dir = "DESC"
		}
		orderParts = append(orderParts, col+" IS NULL, "+col+" "+dir)
	}
	if text && !hasRelevance {
		orderParts = append(orderParts, "score DESC")
	}
	orderParts = append(orderParts, "doc_key", "position")
	orderSQL := strings.Join(orderParts, ", ")

	cte.add(" FROM "+from+" WHERE "+where.sql()+"), ranked AS (SELECT id, doc_key, position, doc", where.args...)
	if text {
		cte.add(", score")
	}
	for _, col := range sortCols {
		cte.add(", " + col)
	}
	cte.add(", ROW_NUMBER() OVER (PARTITION BY grp ORDER BY " + orderSQL + ") AS rn, COUNT(*) OVER (PARTITION BY grp) AS group_count FROM base)")
	return cte, orderSQL
}

// highlights 返回文档 doc 中各文本字段命中查询词的片段。
func (ix *Index[D]) highlights(q queryTerms, doc D) map[string]Snippet {
	v := reflect.ValueOf(doc)
	out := map[string]Snippet{}
	for _, f := range ix.schema.text {
		if s, ok := highlight(ix.tok, q, strings.Join(f.texts(v), " ")); ok {
			out[f.name] = s
		}
	}
	return out
}

// facets 统计符合条件的文档在各分面字段上的取值和数量，每个字段最多 FacetLimit 个。
// 启用折叠时统计各分组排在最前的代表文档，与 Total 口径一致。
func (ix *Index[D]) facets(ctx context.Context, db bun.IDB, q Query, from string, where sqlExpr, text bool, tsq string) (map[string][]FacetValue, error) {
	limit := q.FacetLimit
	if limit <= 0 {
		limit = DefaultFacetLimit
	}
	var cte sqlExpr
	if q.Collapse != "" {
		cte, _ = collapsedCTE(from, where, collapseExpr(q.Collapse), q.Sort, text, tsq)
	}
	out := map[string][]FacetValue{}
	for _, name := range q.Facets {
		f := ix.schema.filters[name]
		if f == nil {
			return nil, errors.Newf("search: cannot facet by %q: not a filter field", name)
		}
		var rows []struct {
			Value string `bun:"value"`
			N     int    `bun:"n"`
		}
		var err error
		if q.Collapse == "" {
			args := append(append([]any{name}, where.args...), limit)
			err = db.NewRaw("SELECT v.value, COUNT(*) AS n FROM search_values AS v WHERE v.field = ? AND v.entry_id IN "+
				"(SELECT e.id FROM "+from+" WHERE "+where.sql()+") GROUP BY v.value ORDER BY n DESC, v.value LIMIT ?", args...).Scan(ctx, &rows)
		} else {
			args := append(append(append([]any(nil), cte.args...), name), limit)
			err = db.NewRaw(cte.sql()+" SELECT v.value, COUNT(*) AS n FROM search_values AS v WHERE v.field = ? AND v.entry_id IN "+
				"(SELECT id FROM ranked WHERE rn = 1) GROUP BY v.value ORDER BY n DESC, v.value LIMIT ?", args...).Scan(ctx, &rows)
		}
		if err != nil {
			return nil, errors.Wrapf(err, "search: facet %q", name)
		}
		values := make([]FacetValue, len(rows))
		for i, r := range rows {
			values[i] = FacetValue{Value: f.decode(r.Value), Count: r.N}
		}
		out[name] = values
	}
	return out, nil
}

// sqlExpr 是拼接中的 SQL 片段和它的参数。
type sqlExpr struct {
	b    strings.Builder
	args []any
}

// add 追加 SQL 片段和它的参数。
func (e *sqlExpr) add(sql string, args ...any) {
	e.b.WriteString(sql)
	e.args = append(e.args, args...)
}

// sql 返回拼好的 SQL。
func (e *sqlExpr) sql() string { return e.b.String() }
