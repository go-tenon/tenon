package dashscope_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-tenon/tenon/ai"
	"github.com/go-tenon/tenon/ai/dashscope"
)

type request struct {
	Path string
	Body map[string]any
}

func newClient(t *testing.T, respond func(r request) (int, any), opts ...dashscope.Option) (*dashscope.Client, *[]request) {
	t.Helper()
	var got []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer sk-ds", r.Header.Get("Authorization"))
		req := request{Path: r.URL.Path}
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &req.Body))
		got = append(got, req)
		status, resp := respond(req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	cfg := ai.ModelConfig{Model: "qwen3-vl-embedding", BaseURL: srv.URL + "/compatible-mode/v1/", APIKey: "sk-ds", MaxRetries: -1}
	return dashscope.New(cfg, opts...), &got
}

var pngImage = ai.Media{Data: []byte("PNG"), MIME: "image/png"}

const pngURL = "data:image/png;base64,UE5H"

// embeddings 按收到的输入倒序返回向量：第一个分量是输入在请求中的位置，第二个是 typ。
func embeddings(typ string) func(r request) (int, any) {
	return func(r request) (int, any) {
		contents := r.Body["input"].(map[string]any)["contents"].([]any)
		var out []map[string]any
		for i := len(contents) - 1; i >= 0; i-- {
			out = append(out, map[string]any{"index": i, "embedding": []float64{float64(i), 1}, "type": typ})
		}
		return http.StatusOK, map[string]any{"output": map[string]any{"embeddings": out}, "request_id": "r1"}
	}
}

func TestEmbed(t *testing.T) {
	c, got := newClient(t, embeddings("vl"), dashscope.WithBatchSize(2), dashscope.WithDimensions(1024))
	inputs := []ai.Content{
		ai.Text("红色"),
		{ai.MediaPart(pngImage)},
		{ai.TextPart("红色的方块"), ai.MediaPart(pngImage), ai.TextPart("边长 64")},
	}
	vecs, err := c.Embed(t.Context(), ai.EmbedRequest{Inputs: inputs})
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{0, 1}, {1, 1}, {0, 1}}, vecs)

	require.Len(t, *got, 2)
	assert.Equal(t, request{Path: "/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding", Body: map[string]any{
		"model": "qwen3-vl-embedding",
		"input": map[string]any{"contents": []any{
			map[string]any{"text": "红色"},
			map[string]any{"image": pngURL},
		}},
		"parameters": map[string]any{"dimension": float64(1024)},
	}}, (*got)[0])
	assert.Equal(t, []any{map[string]any{"text": "红色的方块\n边长 64", "image": pngURL}}, (*got)[1].Body["input"].(map[string]any)["contents"])

	// 查询附带检索指令。
	_, err = c.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{ai.Text("红色")}, Purpose: ai.ForQuery})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"dimension": float64(1024), "instruct": dashscope.DefaultQueryInstruct}, (*got)[2].Body["parameters"])
}

func TestEmbedParameters(t *testing.T) {
	c, got := newClient(t, embeddings("vl"), dashscope.WithQueryInstruct(""))
	for _, purpose := range []ai.Purpose{ai.ForDocument, ai.ForQuery} {
		_, err := c.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{ai.Text("红色")}, Purpose: purpose})
		require.NoError(t, err)
	}
	for _, r := range *got {
		assert.NotContains(t, r.Body, "parameters")
	}

	c, got = newClient(t, embeddings("vl"), dashscope.WithQueryInstruct("根据描述检索相关的图片"))
	_, err := c.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{ai.Text("红色")}, Purpose: ai.ForQuery})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"instruct": "根据描述检索相关的图片"}, (*got)[0].Body["parameters"])
}

func TestEmbedRejects(t *testing.T) {
	// 模型没有把文字和图片融合为一个向量（只返回了图片的向量）。
	c, _ := newClient(t, embeddings("image"))
	vecs, err := c.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{{ai.MediaPart(pngImage)}}})
	require.NoError(t, err)
	assert.Len(t, vecs, 1)
	_, err = c.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{{ai.TextPart("红色"), ai.MediaPart(pngImage)}}})
	assert.ErrorIs(t, err, ai.ErrUnsupportedInput)
	assert.ErrorContains(t, err, `cannot combine text and media in input 0 (got a "image" embedding)`)

	// 不发请求就拒绝的输入。
	c, got := newClient(t, embeddings("vl"))
	audio := ai.Media{Data: []byte("ID3"), MIME: "audio/mpeg"}
	for _, in := range []ai.Content{
		{ai.MediaPart(audio)},
		{ai.MediaPart(pngImage), ai.MediaPart(pngImage)},
	} {
		_, err := c.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{in}})
		assert.ErrorIs(t, err, ai.ErrUnsupportedInput)
	}
	_, err = c.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{{}}})
	assert.ErrorContains(t, err, "embed input 0: ai: empty content")
	assert.Empty(t, *got)
}

func TestEmbedErrors(t *testing.T) {
	c, _ := newClient(t, func(request) (int, any) {
		// 两个向量的序号相同，第二项没有向量。
		return http.StatusOK, map[string]any{"output": map[string]any{"embeddings": []map[string]any{
			{"index": 0, "embedding": []float64{1}}, {"index": 0, "embedding": []float64{2}},
		}}}
	})
	_, err := c.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{ai.Text("a"), ai.Text("b")}})
	assert.ErrorContains(t, err, "no embedding returned for input 1")

	c, _ = newClient(t, func(request) (int, any) {
		return http.StatusBadRequest, map[string]any{"code": "InvalidParameter", "message": "The image format is illegal and cannot be opened", "request_id": "r1"}
	})
	_, err = c.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{{ai.MediaPart(pngImage)}}})
	assert.EqualError(t, err, "ai/dashscope: embed: 400 InvalidParameter: The image format is illegal and cannot be opened (request_id r1)")
}

func TestRerank(t *testing.T) {
	c, got := newClient(t, func(request) (int, any) {
		// 故意乱序返回，检查按分数排序。
		return http.StatusOK, map[string]any{"output": map[string]any{"results": []map[string]any{
			{"index": 0, "relevance_score": 0.2},
			{"index": 2, "relevance_score": 0.9},
		}}}
	})

	got1, err := c.Rerank(t.Context(), ai.Text("红色"), []ai.Content{ai.Text("蓝天"), ai.Text("草地"), ai.Text("红苹果")}, 2)
	require.NoError(t, err)
	assert.Equal(t, []ai.Ranked{{Index: 2, Score: 0.9}, {Index: 0, Score: 0.2}}, got1)
	assert.Equal(t, request{Path: "/api/v1/services/rerank/text-rerank/text-rerank", Body: map[string]any{
		"model":      "qwen3-vl-embedding",
		"input":      map[string]any{"query": "红色", "documents": []any{"蓝天", "草地", "红苹果"}},
		"parameters": map[string]any{"top_n": float64(2), "return_documents": false},
	}}, (*got)[0])

	// 有图片时查询和文档都以对象发送；limit 不小于文档数时不发 top_n。
	_, err = c.Rerank(t.Context(), ai.Content{ai.MediaPart(pngImage)}, []ai.Content{ai.Text("蓝天"), ai.Text("草地"), {ai.MediaPart(pngImage)}}, 0)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"query":     map[string]any{"image": pngURL},
		"documents": []any{map[string]any{"text": "蓝天"}, map[string]any{"text": "草地"}, map[string]any{"image": pngURL}},
	}, (*got)[1].Body["input"])
	assert.Equal(t, map[string]any{"return_documents": false}, (*got)[1].Body["parameters"])

	empty, err := c.Rerank(t.Context(), ai.Text("q"), nil, 1)
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.Len(t, *got, 2)

	_, err = c.Rerank(t.Context(), ai.Text("q"), []ai.Content{ai.Text("a")}, 0)
	assert.ErrorContains(t, err, "rerank index 2 out of range")
	_, err = c.Rerank(t.Context(), ai.Text("q"), []ai.Content{{ai.MediaPart(ai.Media{Data: []byte("x"), MIME: "audio/mpeg"})}}, 0)
	assert.ErrorIs(t, err, ai.ErrUnsupportedInput)
	assert.ErrorContains(t, err, "rerank document 0")
}

var mp4Video = ai.Media{Data: []byte("MP4"), MIME: "video/mp4"}

const mp4URL = "data:video/mp4;base64,TVA0"

func TestEmbedVideo(t *testing.T) {
	c, got := newClient(t, embeddings("vl"))
	inputs := []ai.Content{
		ai.Text("花"),
		{ai.MediaPart(mp4Video)},
		{ai.MediaPart(mp4Video)},
		{ai.TextPart("一朵花"), ai.MediaPart(mp4Video)},
		{ai.MediaPart(pngImage)},
	}
	vecs, err := c.Embed(t.Context(), ai.EmbedRequest{Inputs: inputs})
	require.NoError(t, err)
	assert.Len(t, vecs, 5)
	// 每次请求最多一段视频。
	var contents [][]any
	for _, r := range *got {
		contents = append(contents, r.Body["input"].(map[string]any)["contents"].([]any))
	}
	assert.Equal(t, [][]any{
		{map[string]any{"text": "花"}, map[string]any{"video": mp4URL}},
		{map[string]any{"video": mp4URL}},
		{map[string]any{"text": "一朵花", "video": mp4URL}, map[string]any{"image": pngURL}},
	}, contents)

	// 没有把文字和视频融合为一个向量。
	c, _ = newClient(t, embeddings("text"))
	_, err = c.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{{ai.TextPart("花"), ai.MediaPart(mp4Video)}}})
	assert.ErrorIs(t, err, ai.ErrUnsupportedInput)
	assert.ErrorContains(t, err, "cannot combine text and media in input 0")
}

func TestEmbedCombinedBatch(t *testing.T) {
	// qwen2.5-vl-embedding 把一次请求中的所有项融合为一个向量。
	c, got := newClient(t, func(request) (int, any) {
		return http.StatusOK, map[string]any{"output": map[string]any{"embeddings": []map[string]any{{"index": 0, "embedding": []float64{1}, "type": "vl"}}}}
	})
	_, err := c.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{ai.Text("a"), ai.Text("b")}})
	assert.ErrorContains(t, err, `returned 1 embeddings for 2 inputs`)
	assert.ErrorContains(t, err, "dashscope.WithBatchSize(1)")

	c, got = newClient(t, embeddings("vl"), dashscope.WithBatchSize(1))
	vecs, err := c.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{ai.Text("a"), ai.Text("b")}})
	require.NoError(t, err)
	assert.Len(t, vecs, 2)
	assert.Len(t, *got, 2)
}

func TestRerankVideo(t *testing.T) {
	c, got := newClient(t, func(request) (int, any) {
		return http.StatusOK, map[string]any{"output": map[string]any{"results": []map[string]any{{"index": 0, "relevance_score": 0.7}}}}
	})
	_, err := c.Rerank(t.Context(), ai.Text("一朵花"), []ai.Content{{ai.MediaPart(mp4Video)}}, 0)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"query":     map[string]any{"text": "一朵花"},
		"documents": []any{map[string]any{"video": mp4URL}},
	}, (*got)[0].Body["input"])
}

func TestCompatibleAPI(t *testing.T) {
	// 兼容接口的能力仍然发到 /compatible-mode/v1。
	c, got := newClient(t, func(request) (int, any) {
		return http.StatusOK, map[string]any{"answers": map[string]any{"ok": map[string]any{"type": "noul", "noul": 0.9}}}
	})
	_, err := c.Decide(t.Context(), ai.DecisionRequest{Input: "x", Questions: []ai.Question{ai.YesNo("ok", "?")}})
	require.NoError(t, err)
	assert.Equal(t, "/compatible-mode/v1/systemone", (*got)[0].Path)
}

// TestLive 调用真实的百炼接口，设置了 DASHSCOPE_TEST_BASE_URL 和 DASHSCOPE_TEST_API_KEY 时运行。
// BaseURL 是兼容接口的地址，例如 https://dashscope.aliyuncs.com/compatible-mode/v1。
func TestLive(t *testing.T) {
	base, key := os.Getenv("DASHSCOPE_TEST_BASE_URL"), os.Getenv("DASHSCOPE_TEST_API_KEY")
	if base == "" || key == "" {
		t.Skip("DASHSCOPE_TEST_BASE_URL and DASHSCOPE_TEST_API_KEY are not set")
	}
	red, blue := solid(color.RGBA{220, 30, 30, 255}), solid(color.RGBA{30, 30, 220, 255})

	emb := dashscope.New(ai.ModelConfig{Model: "qwen3-vl-embedding", BaseURL: base, APIKey: key}, dashscope.WithDimensions(1024))
	docs, err := emb.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{
		{ai.MediaPart(red)}, {ai.MediaPart(blue)}, {ai.TextPart("一个方块"), ai.MediaPart(red)},
	}})
	require.NoError(t, err)
	require.Len(t, docs, 3)
	assert.Len(t, docs[0], 1024)
	query, err := emb.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{ai.Text("一张纯红色的图片")}, Purpose: ai.ForQuery})
	require.NoError(t, err)
	assert.Greater(t, cosine(query[0], docs[0]), cosine(query[0], docs[1]), "红色的描述应当更接近红色的图片")

	rr := dashscope.New(ai.ModelConfig{Model: "qwen3-vl-rerank", BaseURL: base, APIKey: key})
	ranked, err := rr.Rerank(t.Context(), ai.Content{ai.MediaPart(red)}, []ai.Content{ai.Text("蓝色"), ai.Text("红色")}, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, ranked[0].Index)

	text := dashscope.New(ai.ModelConfig{Model: "multimodal-embedding-v1", BaseURL: base, APIKey: key})
	_, err = text.Embed(t.Context(), ai.EmbedRequest{Inputs: []ai.Content{{ai.TextPart("一个方块"), ai.MediaPart(red)}}})
	assert.ErrorIs(t, err, ai.ErrUnsupportedInput, "multimodal-embedding-v1 不融合文字和图片")
}

func solid(c color.Color) ai.Media {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return ai.Media{Data: b.Bytes(), MIME: "image/png"}
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}
