// Package dashscope 调用阿里云百炼（DashScope）：多模态向量和重排使用百炼的原生接口，其余能力使用它的 OpenAI 兼容接口。
//
// ai.ModelConfig 的 BaseURL 与对话模型相同，是兼容接口的地址，例如 https://dashscope.aliyuncs.com/compatible-mode/v1，
// 或工作空间的 https://<工作空间>.cn-beijing.maas.aliyuncs.com/compatible-mode/v1；为空时使用前者。
// 原生接口的地址由它去掉 /compatible-mode/v1 得到。
package dashscope

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/cockroachdb/errors"
	oa "github.com/openai/openai-go"

	"github.com/go-tenon/tenon/ai"
	"github.com/go-tenon/tenon/ai/openai"
	"github.com/go-tenon/tenon/ai/systemone"
)

const (
	defaultBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	compatiblePath = "/compatible-mode/v1"
	embedPath      = "/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding"
	rerankPath     = "/api/v1/services/rerank/text-rerank/text-rerank"

	// DefaultBatchSize 是一次请求最多计算的向量数，与百炼的上限相同。
	DefaultBatchSize = 20
	// DefaultQueryInstruct 是给查询计算向量时附带的检索指令。
	DefaultQueryInstruct = "Retrieve images or text relevant to the user's query."
)

// Client 是百炼上的一个模型。它嵌入 openai.Client，兼容接口上的对话、图片、语音等照常可用；Embed 和 Rerank 改用原生接口，
// Decide 使用 System One 格式。
//
// Embed 需要多模态向量模型，例如 qwen3-vl-embedding、qwen2.5-vl-embedding、tongyi-embedding-vision-plus、
// multimodal-embedding-v1；只有文字的向量模型（例如 qwen3.7-text-embedding）用 ai/openai。每项输入可以是一段文字、
// 一张图片、一段视频，或一段文字加一张图片（一段视频）；后者只有 qwen3-vl-embedding、qwen2.5-vl-embedding 这类模型
// 能融合为一个向量，其他模型会只取其中一种，此时 Embed 返回 ai.ErrUnsupportedInput。百炼没有音频向量模型。
//
// 一次请求最多放 WithBatchSize 项、一段视频，超过时分成多次请求。qwen2.5-vl-embedding 把一次请求中的所有项融合为
// 一个向量，要用 WithBatchSize(1)。
//
// Rerank 可以用文字重排模型（例如 qwen3.7-text-rerank），也可以用多模态重排模型（例如 qwen3-vl-rerank），
// 后者的查询和文档可以是图片或视频。
type Client struct {
	*openai.Client
	native     string // 原生接口的地址，不带结尾的 /
	dims       int
	batchSize  int
	instruct   string
	openaiOpts []openai.Option
}

var (
	_ ai.Embedder = (*Client)(nil)
	_ ai.Reranker = (*Client)(nil)
	_ ai.Decider  = (*Client)(nil)
)

// Option 是 New 的选项。
type Option func(*Client)

// WithDimensions 设置向量的维度，例如 qwen3-vl-embedding 可以输出 2560、1024、512 等维度；0 使用模型的默认维度。
// 有的模型（例如 multimodal-embedding-v1）不支持，总是返回默认维度的向量。
func WithDimensions(n int) Option { return func(c *Client) { c.dims = n } }

// WithBatchSize 设置一次请求最多计算的向量数，默认 DefaultBatchSize。
func WithBatchSize(n int) Option { return func(c *Client) { c.batchSize = n } }

// WithQueryInstruct 设置给查询计算向量时附带的检索指令，默认 DefaultQueryInstruct，"" 表示不带。
// 指令只对支持它的模型（例如 qwen3-vl-embedding）起作用，其他模型忽略它。
func WithQueryInstruct(s string) Option { return func(c *Client) { c.instruct = s } }

// WithOpenAI 传入嵌入的 openai.Client 的选项，例如 openai.WithHTTPClient。
func WithOpenAI(opts ...openai.Option) Option {
	return func(c *Client) { c.openaiOpts = append(c.openaiOpts, opts...) }
}

// New 按 cfg 创建 Client。创建时不访问网络。
func New(cfg ai.ModelConfig, opts ...Option) *Client {
	c := &Client{batchSize: DefaultBatchSize, instruct: DefaultQueryInstruct}
	for _, opt := range opts {
		opt(c)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if c.batchSize <= 0 {
		c.batchSize = DefaultBatchSize
	}
	c.native = strings.TrimSuffix(strings.TrimSuffix(cfg.BaseURL, "/"), compatiblePath)
	c.Client = openai.New(cfg, c.openaiOpts...)
	return c
}

// post 调用原生接口：鉴权、重试与兼容接口相同（ai.ModelConfig.MaxRetries）。
// 出错时把百炼返回的错误码、说明和请求 ID 写进错误中。
func (c *Client) post(ctx context.Context, path string, body, out any) error {
	err := c.OpenAI().Post(ctx, c.native+path, body, out)
	var apiErr *oa.Error
	if !errors.As(err, &apiErr) || apiErr.Response == nil || apiErr.Response.Body == nil {
		return err
	}
	raw, _ := io.ReadAll(apiErr.Response.Body)
	var e struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if json.Unmarshal(raw, &e) != nil || e.Message == "" {
		return err
	}
	return errors.Newf("%d %s: %s (request_id %s)", apiErr.StatusCode, e.Code, e.Message, e.RequestID)
}

// item 是原生接口的一项多模态输入。
type item struct {
	Text  string `json:"text,omitempty"`
	Image string `json:"image,omitempty"`
	Video string `json:"video,omitempty"`
}

// itemOf 把 Content 转换为一项输入：文字各段用换行连接，最多一张图片或一段视频，不接受音频（百炼没有音频向量模型）。
func itemOf(c ai.Content) (item, error) {
	if err := c.Validate(); err != nil {
		return item{}, err
	}
	it := item{Text: c.Text()}
	media := c.Media()
	if len(media) > 1 {
		return item{}, errors.Wrap(ai.ErrUnsupportedInput, "more than one media in an input")
	}
	if len(media) == 1 {
		url := media[0].DataURL()
		switch media[0].Modality() {
		case ai.ModalityImage:
			it.Image = url
		case ai.ModalityVideo:
			it.Video = url
		default:
			return item{}, errors.Wrapf(ai.ErrUnsupportedInput, "media type %q, only images and videos are supported", media[0].MIME)
		}
	}
	return it, nil
}

// mixed 报告一项是否同时有文字和媒体：只有融合的结果（vl、fusion）才代表整项。
func (it item) mixed() bool { return it.Text != "" && (it.Image != "" || it.Video != "") }

// batches 把 items 分成多次请求的范围：每次最多 size 项、最多一段视频（qwen3-vl-embedding 等一次只接受一段视频）。
func batches(items []item, size int) [][2]int {
	var out [][2]int
	start, videos := 0, 0
	for i, it := range items {
		if i-start == size || (it.Video != "" && videos == 1) {
			out = append(out, [2]int{start, i})
			start, videos = i, 0
		}
		if it.Video != "" {
			videos++
		}
	}
	if start < len(items) {
		out = append(out, [2]int{start, len(items)})
	}
	return out
}

type embedParams struct {
	Dimension int    `json:"dimension,omitempty"`
	Instruct  string `json:"instruct,omitempty"`
}

type embedBody struct {
	Model string `json:"model"`
	Input struct {
		Contents []item `json:"contents"`
	} `json:"input"`
	Parameters *embedParams `json:"parameters,omitempty"`
}

type embedResponse struct {
	Output struct {
		Embeddings []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
			Type      string    `json:"type"`
		} `json:"embeddings"`
	} `json:"output"`
}

// Embed 实现 ai.Embedder，调用多模态向量接口。查询（ai.ForQuery）附带检索指令，见 WithQueryInstruct。
func (c *Client) Embed(ctx context.Context, req ai.EmbedRequest) ([][]float32, error) {
	items := make([]item, len(req.Inputs))
	for i, in := range req.Inputs {
		it, err := itemOf(in)
		if err != nil {
			return nil, errors.Wrapf(err, "ai/dashscope: embed input %d", i)
		}
		items[i] = it
	}
	var params *embedParams
	if c.dims > 0 || (req.Purpose == ai.ForQuery && c.instruct != "") {
		params = &embedParams{Dimension: c.dims}
		if req.Purpose == ai.ForQuery {
			params.Instruct = c.instruct
		}
	}
	out := make([][]float32, len(items))
	for _, b := range batches(items, c.batchSize) {
		start, part := b[0], items[b[0]:b[1]]
		body := embedBody{Model: c.Model(), Parameters: params}
		body.Input.Contents = part
		var resp embedResponse
		if err := c.post(ctx, embedPath, body, &resp); err != nil {
			return nil, errors.Wrap(err, "ai/dashscope: embed")
		}
		if n := len(resp.Output.Embeddings); n != len(part) {
			// qwen2.5-vl-embedding 这类模型把一次请求中的所有项融合为一个向量
			return nil, errors.Newf("ai/dashscope: model %q returned %d embeddings for %d inputs; "+
				"if it combines all inputs of a request into one, use dashscope.WithBatchSize(1)", c.Model(), n, len(part))
		}
		for _, e := range resp.Output.Embeddings {
			if e.Index < 0 || e.Index >= len(part) {
				return nil, errors.Newf("ai/dashscope: embedding index %d out of range", e.Index)
			}
			// 一项中同时有文字和媒体时，只有融合的结果（vl、fusion）才代表整项；其他模型只取了其中一种。
			if part[e.Index].mixed() && e.Type != "vl" && e.Type != "fusion" {
				return nil, errors.Wrapf(ai.ErrUnsupportedInput,
					"ai/dashscope: model %q cannot combine text and media in input %d (got a %q embedding)", c.Model(), start+e.Index, e.Type)
			}
			out[start+e.Index] = e.Embedding
		}
	}
	for i, v := range out {
		if v == nil {
			return nil, errors.Newf("ai/dashscope: no embedding returned for input %d", i)
		}
	}
	return out, nil
}

type rerankBody struct {
	Model string `json:"model"`
	Input struct {
		Query     any   `json:"query"`
		Documents []any `json:"documents"`
	} `json:"input"`
	Parameters struct {
		TopN            int  `json:"top_n,omitempty"`
		ReturnDocuments bool `json:"return_documents"`
	} `json:"parameters"`
}

type rerankResponse struct {
	Output struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
	} `json:"output"`
}

// Rerank 实现 ai.Reranker，调用重排接口。全是文字时以字符串发送，各种重排模型都接受；
// 有图片或视频时以对象发送，需要多模态重排模型（例如 qwen3-vl-rerank）。
func (c *Client) Rerank(ctx context.Context, query ai.Content, docs []ai.Content, limit int) ([]ai.Ranked, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	all := append([]ai.Content{query}, docs...)
	items := make([]item, len(all))
	hasMedia := false
	for i, in := range all {
		it, err := itemOf(in)
		if err != nil {
			if i == 0 {
				return nil, errors.Wrap(err, "ai/dashscope: rerank query")
			}
			return nil, errors.Wrapf(err, "ai/dashscope: rerank document %d", i-1)
		}
		items[i] = it
		hasMedia = hasMedia || it.Image != "" || it.Video != ""
	}
	value := func(it item) any {
		if hasMedia {
			return it
		}
		return it.Text
	}
	var body rerankBody
	body.Model = c.Model()
	body.Input.Query = value(items[0])
	for _, it := range items[1:] {
		body.Input.Documents = append(body.Input.Documents, value(it))
	}
	if limit > 0 && limit < len(docs) {
		body.Parameters.TopN = limit
	}
	var resp rerankResponse
	if err := c.post(ctx, rerankPath, body, &resp); err != nil {
		return nil, errors.Wrap(err, "ai/dashscope: rerank")
	}
	out := make([]ai.Ranked, len(resp.Output.Results))
	for i, r := range resp.Output.Results {
		out[i] = ai.Ranked{Index: r.Index, Score: r.RelevanceScore}
	}
	ranked, err := ai.TopRanked(out, len(docs), limit)
	return ranked, errors.Wrap(err, "ai/dashscope: rerank")
}

// Decide 实现 ai.Decider，使用百炼的 System One 接口。
func (c *Client) Decide(ctx context.Context, req ai.DecisionRequest) (ai.Decision, error) {
	return systemone.Wrap(c.Client).Decide(ctx, req)
}
