package vector

import (
	"context"
	"encoding/json"

	"github.com/cockroachdb/errors"
	"github.com/qdrant/go-client/qdrant"

	"github.com/go-tenon/tenon/ai"
)

// DefaultLimit 是 Query.Limit 不大于 0 时返回的结果数。
const DefaultLimit = 10

// 混合索引每一路检索的候选数：Limit 的 prefetchFactor 倍，至少 minPrefetch 个。
const (
	prefetchFactor = 4
	minPrefetch    = 20
)

// Query 是一次检索的条件。
type Query struct {
	Input      Content // 查询的内容，用建立索引当前版本的模型计算向量后检索语义最接近的文档。必填。文字查询用 Text("…")；以图搜图用 Content{MediaPart(上传的图片)} 或 Content{FilePart(id)}，需要模型接受图片（Model.Inputs）。
	Tenant     string  // 查询的租户，"" 为平台本身。文档类型有 tenant 字段时总是只查询这个租户的文档；没有 tenant 字段时必须为空。
	Filter     Filter  // nil 时不过滤
	Limit      int     // 最多返回的结果数，不大于 0 时为 DefaultLimit。
	MinScore   float64 // 结果的最低相似度，0 时不限制。相似度是余弦相似度，范围是 -1 到 1。混合索引中它只过滤语义那一路的候选，关键词命中的结果全部保留。以文搜图这类跨模态的检索，相似度普遍偏低，合适的阈值很难定，通常不设。
	GroupByKey bool    // 让每个 key 只返回最接近的一个文档，返回最多 Limit 个 key。一个 key 有多个文档时使用，例如商品的标题和每张图片各是一个文档，结果中每个商品只出现一次。
}

// Hit 是一条检索结果。
type Hit[D any] struct {
	Key   string  `json:"key"`   // 文档所属的 key，即 Sync 时传入的 key
	Doc   D       `json:"doc"`   // 同步时保存的文档
	Score float64 `json:"score"` // 越大越相关，只用于比较同一次查询的结果：普通索引中是与查询文本的余弦相似度，混合索引中是两路排名的 RRF 分数。
}

// Search 检索与 q.Input 语义最接近的文档，按相似度从高到低返回。查询使用索引当前的版本，
// 用建立这个版本的模型计算 q.Input 的向量。索引还没有同步过任何文档时返回空。
// 模型不接受 q.Input 中的内容（例如只接受文字的模型收到了图片）时，不调用模型直接返回错误。
func (ix *Index[D]) Search(ctx context.Context, s *Store, q Query) ([]Hit[D], error) {
	if err := q.Input.validate(); err != nil {
		return nil, errors.Wrap(err, "vector: query input")
	}
	filter, err := ix.filter(q)
	if err != nil {
		return nil, err
	}
	in, err := q.Input.resolve(ctx, s.files)
	if err != nil {
		return nil, errors.Wrap(err, "vector: query input")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	// 读到版本之后、查询之前，重建可能刚好切换并删除了这个版本：重新读取版本再查一次
	found, err := ix.query(ctx, s, in, q, filter, limit)
	if notFound(err) {
		found, err = ix.query(ctx, s, in, q, filter, limit)
	}
	if err != nil {
		return nil, err
	}
	hits := make([]Hit[D], len(found))
	for i, p := range found {
		payload := p.GetPayload()
		h := Hit[D]{Key: payload[payloadKey].GetStringValue(), Score: float64(p.GetScore())}
		if err := json.Unmarshal([]byte(payload[payloadDoc].GetStringValue()), &h.Doc); err != nil {
			return nil, errors.Wrapf(err, "vector: decode %s document %q", ix.name, h.Key)
		}
		hits[i] = h
	}
	return hits, nil
}

// filter 把租户和 q.Filter 转换为 Qdrant 的过滤条件。
func (ix *Index[D]) filter(q Query) (*qdrant.Filter, error) {
	var must []*qdrant.Condition
	if ix.schema.tenant != nil {
		must = append(must, qdrant.NewMatchKeyword(payloadTenant, q.Tenant))
	} else if q.Tenant != "" {
		return nil, errors.Newf("vector: index %s has no tenant field", ix.name)
	}
	if q.Filter != nil {
		c, err := q.Filter.condition(ix.schema)
		if err != nil {
			return nil, err
		}
		must = append(must, c)
	}
	if len(must) == 0 {
		return nil, nil
	}
	return &qdrant.Filter{Must: must}, nil
}

// query 在查询使用的版本中检索与 in 最接近的 limit 个文档（q.GroupByKey 时是 limit 个 key 中各自最接近的文档），
// 用建立这个版本的模型计算 in 的向量。
func (ix *Index[D]) query(ctx context.Context, s *Store, in ai.Content, q Query, filter *qdrant.Filter, limit int) ([]*qdrant.ScoredPoint, error) {
	st, err := indexes.Read(ctx, s.db, ix.name, false)
	if err != nil {
		return nil, err
	}
	collection := s.collection(ix.name, st.Version)
	c, ok, err := s.open(ctx, collection, ix.schema, false)
	if err != nil || !ok {
		return nil, err
	}
	m := c.model
	if err := m.check(in); err != nil {
		return nil, errors.Wrap(err, "vector: query input")
	}
	vecs, err := m.Embedder.Embed(ctx, ai.EmbedRequest{Inputs: []ai.Content{in}, Purpose: ai.ForQuery})
	if err != nil {
		return nil, errors.Wrap(err, "vector: embed query")
	}
	if len(vecs) != 1 || len(vecs[0]) != m.Dims {
		return nil, errors.Newf("vector: embedder returned an unexpected vector for model %q", m.Name)
	}
	req := &qdrant.QueryPoints{
		CollectionName: collection,
		Query:          qdrant.NewQueryDense(vecs[0]),
		Filter:         filter,
		Limit:          qdrant.PtrOf(uint64(limit)),
		WithPayload:    qdrant.NewWithPayloadInclude(payloadKey, payloadDoc),
	}
	if q.MinScore != 0 {
		req.ScoreThreshold = qdrant.PtrOf(float32(q.MinScore))
	}
	if c.hybrid {
		req.Using = qdrant.PtrOf(vectorDense)
	}
	if tok := ix.schema.tokenizer; tok != nil && c.hybrid {
		// 语义和关键词两路各取 prefetchFactor 倍的候选，按排名用 RRF 融合；MinScore 只作用于语义那一路
		n := qdrant.PtrOf(uint64(max(limit*prefetchFactor, minPrefetch)))
		prefetch := []*qdrant.PrefetchQuery{{
			Query: qdrant.NewQueryDense(vecs[0]), Using: qdrant.PtrOf(vectorDense), Filter: filter, Limit: n,
			ScoreThreshold: req.ScoreThreshold,
		}}
		// 只有图片的查询没有关键词，只有语义那一路
		if sp := querySparse(tok, q.Input.text()); len(sp.indices) > 0 {
			prefetch = append(prefetch, &qdrant.PrefetchQuery{
				Query: qdrant.NewQuerySparse(sp.indices, sp.values), Using: qdrant.PtrOf(vectorSparse), Filter: filter, Limit: n,
			})
		}
		req.Prefetch, req.Query = prefetch, qdrant.NewQueryFusion(qdrant.Fusion_RRF)
		req.Using, req.Filter, req.ScoreThreshold = nil, nil, nil // 已经在每一路中
	}
	var found []*qdrant.ScoredPoint
	if q.GroupByKey {
		found, err = s.queryGroups(ctx, req)
	} else {
		found, err = s.client.Query(ctx, req)
	}
	if notFound(err) {
		s.mu.Lock()
		delete(s.ready, collection)
		s.mu.Unlock()
	}
	return found, errors.Wrapf(err, "vector: query %s", ix.name)
}

// queryGroups 按 _key 分组执行 req，每组取最接近的一个文档，返回最多 req.Limit 组。
func (s *Store) queryGroups(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error) {
	groups, err := s.client.QueryGroups(ctx, &qdrant.QueryPointGroups{
		CollectionName: req.CollectionName,
		Prefetch:       req.Prefetch,
		Query:          req.Query,
		Using:          req.Using,
		Filter:         req.Filter,
		ScoreThreshold: req.ScoreThreshold,
		WithPayload:    req.WithPayload,
		Limit:          req.Limit,
		GroupBy:        payloadKey,
		GroupSize:      qdrant.PtrOf(uint64(1)),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*qdrant.ScoredPoint, 0, len(groups))
	for _, g := range groups {
		if len(g.GetHits()) > 0 {
			out = append(out, g.GetHits()[0])
		}
	}
	return out, nil
}
