package vector_test

import (
	"bytes"
	"context"
	"encoding/json"
	goimage "image"
	"image/color"
	"image/png"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/ai"
	"github.com/go-tenon/tenon/ai/aitest"
	"github.com/go-tenon/tenon/ai/dashscope"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/search"
	"github.com/go-tenon/tenon/vector"
	"github.com/go-tenon/tenon/vector/vectortest"
)

// productDoc 是商品的一个文档：标题一个，每张图片一个。
type productDoc struct {
	Kind  string         `json:"kind" vector:"filter"`
	Embed vector.Content `json:"embed" vector:"embed"`
}

var (
	products       = vector.Define[productDoc]("products")
	hybridProducts = vector.Define[productDoc]("hybrid-products", vector.Hybrid(search.Bigram))
)

// memFiles 是内存中的文件存储，记录每个文件被打开的次数。
type memFiles struct {
	mu    sync.Mutex
	files map[uuid.UUID]ai.Media
	opens map[uuid.UUID]int
}

func (m *memFiles) add(media ai.Media) uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := uuid.New()
	m.files[id] = media
	return id
}

func (m *memFiles) Open(_ context.Context, id uuid.UUID) (io.ReadCloser, filesystem.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	media, ok := m.files[id]
	if !ok {
		return nil, filesystem.File{}, filesystem.ErrNotFound
	}
	m.opens[id]++
	f := filesystem.File{ID: id, ContentType: media.MIME, Size: int64(len(media.Data)), Filename: "f"}
	return io.NopCloser(bytes.NewReader(media.Data)), f, nil
}

func (m *memFiles) opened() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.opens {
		n += c
	}
	return n
}

type productFixture struct {
	emb   *aitest.Embedder
	files *memFiles
	store *vector.Store
	db    *bun.DB
	mu    sync.Mutex
	docs  map[string][]productDoc
}

func setupProducts(t *testing.T, inputs []ai.Modality, opts ...vector.Option) *productFixture {
	t.Helper()
	db := dbtest.Open(t, migrations(t))
	f := &productFixture{
		emb:   aitest.NewEmbedder(dims),
		files: &memFiles{files: map[uuid.UUID]ai.Media{}, opens: map[uuid.UUID]int{}},
		db:    db,
		docs:  map[string][]productDoc{},
	}
	opts = append([]vector.Option{vector.WithFiles(f.files)}, opts...)
	f.store = vectortest.New(t, db, vector.Model{Name: "mm", Dims: dims, Embedder: f.emb, Inputs: inputs}, opts...)
	return f
}

func (f *productFixture) load(_ context.Context, _ bun.IDB, key string) ([]productDoc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.docs[key], nil
}

func (f *productFixture) put(ctx context.Context, ix *vector.Index[productDoc], key string, docs ...productDoc) error {
	f.mu.Lock()
	f.docs[key] = docs
	f.mu.Unlock()
	return ix.Refresh(ctx, f.store, f.load, key)
}

func image(data string) ai.Media { return ai.Media{Data: []byte(data), MIME: "image/png"} }

func imageContent(m ai.Media) ai.Content { return ai.Content{ai.MediaPart(m)} }

var textAndImage = []ai.Modality{ai.ModalityText, ai.ModalityImage}

func keys(hits []vector.Hit[productDoc]) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Key + ":" + h.Doc.Kind
	}
	return out
}

func TestImageDocuments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setupProducts(t, textAndImage)
	red, blue := image("RED"), image("BLUE")
	redID, blueID := f.files.add(red), f.files.add(blue)
	// 测试中让“红色”这段文字与红色的图片得到相同的向量。
	f.emb.Same(ai.Text("红色"), imageContent(red))

	if err := f.put(ctx, products, "p1",
		productDoc{Kind: "title", Embed: vector.Text("连衣裙")},
		productDoc{Kind: "image", Embed: vector.Content{vector.FilePart(redID)}},
	); err != nil {
		t.Fatal(err)
	}
	if err := f.put(ctx, products, "p2",
		productDoc{Kind: "title", Embed: vector.Text("牛仔裤")},
		productDoc{Kind: "image", Embed: vector.Content{vector.FilePart(blueID)}},
	); err != nil {
		t.Fatal(err)
	}
	if n := f.files.opened(); n != 2 {
		t.Fatalf("opened %d files, want 2", n)
	}
	if got := f.emb.Requests()[0].Inputs; len(got) != 2 || got[1][0].Media == nil || string(got[1][0].Media.Data) != "RED" {
		t.Fatalf("embed inputs = %+v", got)
	}

	search := func(q vector.Query) []vector.Hit[productDoc] {
		t.Helper()
		hits, err := products.Search(ctx, f.store, q)
		if err != nil {
			t.Fatal(err)
		}
		return hits
	}
	// 以文搜图：最接近的是 p1 的图片；每个文档单独返回。
	hits := search(vector.Query{Input: vector.Text("红色")})
	if len(hits) != 4 || keys(hits)[0] != "p1:image" {
		t.Fatalf("hits = %v", keys(hits))
	}
	if hits[0].Doc.Embed[0].File != redID {
		t.Fatalf("decoded doc = %+v", hits[0].Doc)
	}
	// GroupByKey：每个商品只返回最接近的一个文档。
	hits = search(vector.Query{Input: vector.Text("红色"), GroupByKey: true})
	if got := keys(hits); len(got) != 2 || got[0] != "p1:image" || !strings.HasPrefix(got[1], "p2:") {
		t.Fatalf("grouped hits = %v", got)
	}
	if hits := search(vector.Query{Input: vector.Text("红色"), GroupByKey: true, Limit: 1}); len(hits) != 1 {
		t.Fatalf("grouped hits with limit 1 = %v", keys(hits))
	}
	// 以图搜图：上传的图片，或者文件存储中的图片。
	for _, in := range []vector.Content{{vector.MediaPart(blue)}, {vector.FilePart(blueID)}} {
		if hits := search(vector.Query{Input: in, Limit: 1}); keys(hits)[0] != "p2:image" {
			t.Fatalf("query %+v: hits = %v", in, keys(hits))
		}
	}
	// 过滤照常可用。
	if hits := search(vector.Query{Input: vector.Text("红色"), Filter: vector.Eq("kind", "title")}); len(hits) != 2 {
		t.Fatalf("filtered hits = %v", keys(hits))
	}

	// 内容没有变化：不读取文件，也不调用模型。
	opened, requests := f.files.opened(), len(f.emb.Requests())
	if err := products.Refresh(ctx, f.store, f.load, "p1"); err != nil {
		t.Fatal(err)
	}
	if f.files.opened() != opened || len(f.emb.Requests()) != requests {
		t.Fatalf("unchanged document was re-embedded: opened %d→%d, requests %d→%d", opened, f.files.opened(), requests, len(f.emb.Requests()))
	}
	// 换了图片（新的文件 ID）：只读取新的文件。
	newID := f.files.add(image("GREEN"))
	if err := f.put(ctx, products, "p1",
		productDoc{Kind: "title", Embed: vector.Text("连衣裙")},
		productDoc{Kind: "image", Embed: vector.Content{vector.FilePart(newID)}},
	); err != nil {
		t.Fatal(err)
	}
	if f.files.opened() != opened+1 {
		t.Fatalf("opened %d files after changing the image, want %d", f.files.opened(), opened+1)
	}
	last := f.emb.Requests()[len(f.emb.Requests())-1]
	if len(last.Inputs) != 1 || last.Purpose != ai.ForDocument {
		t.Fatalf("last embed request = %+v", last)
	}
}

func TestTextAndImageInOneDocument(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setupProducts(t, textAndImage)
	red := image("RED")
	id := f.files.add(red)
	doc := productDoc{Kind: "card", Embed: vector.Content{vector.TextPart("红色连衣裙"), vector.FilePart(id)}}
	if err := f.put(ctx, products, "p1", doc); err != nil {
		t.Fatal(err)
	}
	got := f.emb.Requests()[0].Inputs[0]
	want := ai.Content{ai.TextPart("红色连衣裙"), imageContent(red)[0]}
	if len(got) != 2 || got[0].Text != want[0].Text || string(got[1].Media.Data) != "RED" {
		t.Fatalf("embed input = %+v", got)
	}
	// 文档中的 Content 编码为 JSON 保存，图片只保存文件 ID。
	b, _ := json.Marshal(doc.Embed)
	if string(b) != `[{"text":"红色连衣裙"},{"file":"`+id.String()+`"}]` {
		t.Fatalf("json = %s", b)
	}
	b, _ = json.Marshal(vector.Content{vector.MediaPart(red)})
	if string(b) != `[{"media":"image/png"}]` {
		t.Fatalf("media json = %s", b)
	}
}

func TestHybridImageOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setupProducts(t, textAndImage)
	red := image("RED")
	id := f.files.add(red)
	if err := f.put(ctx, hybridProducts, "p1",
		productDoc{Kind: "title", Embed: vector.Text("红色连衣裙")},
		productDoc{Kind: "image", Embed: vector.Content{vector.FilePart(id)}},
	); err != nil {
		t.Fatal(err)
	}
	// 只有图片的文档没有关键词；只有图片的查询只走语义那一路。
	hits, err := hybridProducts.Search(ctx, f.store, vector.Query{Input: vector.Content{vector.MediaPart(red)}})
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(hits); len(got) != 2 || got[0] != "p1:image" {
		t.Fatalf("hits = %v", got)
	}
	hits, err = hybridProducts.Search(ctx, f.store, vector.Query{Input: vector.Text("连衣裙"), GroupByKey: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(hits); !slices.Equal(got, []string{"p1:title"}) {
		t.Fatalf("hits = %v", got)
	}
}

func TestImageRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 模型只接受文字：不读文件也不调用模型就拒绝。
	f := setupProducts(t, nil)
	id := f.files.add(image("RED"))
	err := f.put(ctx, products, "p1", productDoc{Embed: vector.Content{vector.FilePart(id)}})
	if err == nil || !strings.Contains(err.Error(), `model "mm" does not accept image input`) {
		t.Fatalf("err = %v", err)
	}
	if len(f.emb.Requests()) != 0 {
		t.Fatalf("embedder was called: %+v", f.emb.Requests())
	}
	if err := f.put(ctx, products, "p2", productDoc{Embed: vector.Text("连衣裙")}); err != nil {
		t.Fatal(err)
	}
	_, err = products.Search(ctx, f.store, vector.Query{Input: vector.Content{vector.MediaPart(image("RED"))}})
	if err == nil || !strings.Contains(err.Error(), "does not accept image input") {
		t.Fatalf("search err = %v", err)
	}

	f = setupProducts(t, textAndImage)
	for _, c := range []struct {
		doc  productDoc
		want string
	}{
		{productDoc{Embed: vector.Content{vector.FilePart(uuid.New())}}, "not found"},
		{productDoc{Embed: vector.Content{vector.FilePart(f.files.add(ai.Media{Data: []byte("%PDF"), MIME: "application/pdf"}))}}, `unsupported media type "application/pdf"`},
		{productDoc{Embed: vector.Content{}}, "nothing to embed: vector: empty content"},
		{productDoc{Embed: vector.Content{{Text: "a", File: uuid.New()}}}, "exactly one of text, file and media"},
	} {
		if err := f.put(ctx, products, "bad", c.doc); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("doc %+v: err = %v, want %q", c.doc, err, c.want)
		}
	}
	if _, err := products.Search(ctx, f.store, vector.Query{}); err == nil || !strings.Contains(err.Error(), "empty content") {
		t.Errorf("empty query: err = %v", err)
	}

	// Store 没有 WithFiles。
	db := dbtest.Open(t, migrations(t))
	plain := vectortest.New(t, db, vector.Model{Name: "mm", Dims: dims, Embedder: aitest.NewEmbedder(dims), Inputs: textAndImage})
	load := func(context.Context, bun.IDB, string) ([]productDoc, error) {
		return []productDoc{{Embed: vector.Content{vector.FilePart(uuid.New())}}}, nil
	}
	if err := products.Refresh(ctx, plain, load, "p1"); err == nil || !strings.Contains(err.Error(), "vector.WithFiles") {
		t.Fatalf("err = %v", err)
	}
}

// TestLiveImageSearch 用百炼的多模态向量模型检索图片，设置了 DASHSCOPE_TEST_BASE_URL 和 DASHSCOPE_TEST_API_KEY 时运行。
func TestLiveImageSearch(t *testing.T) {
	base, key := os.Getenv("DASHSCOPE_TEST_BASE_URL"), os.Getenv("DASHSCOPE_TEST_API_KEY")
	if base == "" || key == "" {
		t.Skip("DASHSCOPE_TEST_BASE_URL and DASHSCOPE_TEST_API_KEY are not set")
	}
	ctx := context.Background()
	files := &memFiles{files: map[uuid.UUID]ai.Media{}, opens: map[uuid.UUID]int{}}
	emb := dashscope.New(ai.ModelConfig{Model: "qwen3-vl-embedding", BaseURL: base, APIKey: key}, dashscope.WithDimensions(1024))
	db := dbtest.Open(t, migrations(t))
	store := vectortest.New(t, db, vector.Model{Name: "qwen3-vl-embedding", Dims: 1024, Embedder: emb, Inputs: textAndImage},
		vector.WithFiles(files))
	docs := map[string][]productDoc{}
	load := func(_ context.Context, _ bun.IDB, key string) ([]productDoc, error) { return docs[key], nil }
	colors := map[string]color.RGBA{"red": {220, 30, 30, 255}, "blue": {30, 30, 220, 255}, "green": {30, 180, 30, 255}}
	media := map[string]ai.Media{}
	for name, c := range colors {
		media[name] = solidPNG(c)
		docs[name] = []productDoc{{Kind: name, Embed: vector.Content{vector.FilePart(files.add(media[name]))}}}
		if err := products.Refresh(ctx, store, load, name); err != nil {
			t.Fatal(err)
		}
	}
	for q, want := range map[string]string{"一张纯红色的图片": "red", "一张纯蓝色的图片": "blue", "一张纯绿色的图片": "green"} {
		hits, err := products.Search(ctx, store, vector.Query{Input: vector.Text(q), Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if hits[0].Key != want {
			t.Errorf("%s: got %s (%.3f), want %s", q, hits[0].Key, hits[0].Score, want)
		}
	}
	hits, err := products.Search(ctx, store, vector.Query{Input: vector.Content{vector.MediaPart(media["blue"])}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].Key != "blue" {
		t.Errorf("image query: got %s", hits[0].Key)
	}
}

func solidPNG(c color.Color) ai.Media {
	img := goimage.NewRGBA(goimage.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return ai.Media{Data: b.Bytes(), MIME: "image/png"}
}

// TestVideoDocuments 不并行：它调小了全局的 embedBytes。
func TestVideoDocuments(t *testing.T) {
	defer vector.SetEmbedBytes(15)()
	ctx := context.Background()
	video := func(s string) ai.Media { return ai.Media{Data: []byte(s), MIME: "video/mp4"} }
	f := setupProducts(t, []ai.Modality{ai.ModalityText, ai.ModalityImage, ai.ModalityVideo})
	flower := video("FLOWER-MP4")
	f.emb.Same(ai.Text("一朵花"), ai.Content{ai.MediaPart(flower)})
	ids := []uuid.UUID{f.files.add(flower), f.files.add(video("BUNNY--MP4")), f.files.add(video("CITY---MP4"))}
	var docs []productDoc
	for _, id := range ids {
		docs = append(docs, productDoc{Kind: "video", Embed: vector.Content{vector.FilePart(id)}})
	}
	if err := f.put(ctx, products, "v1", docs...); err != nil {
		t.Fatal(err)
	}
	// 每段 10 字节，累计到 15 字节就先计算一批：两次请求。
	var sizes []int
	for _, r := range f.emb.Requests() {
		sizes = append(sizes, len(r.Inputs))
	}
	if !slices.Equal(sizes, []int{2, 1}) {
		t.Fatalf("embed batches %v, want [2 1]", sizes)
	}
	hits, err := products.Search(ctx, f.store, vector.Query{Input: vector.Text("一朵花"), Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].Doc.Embed[0].File != ids[0] {
		t.Fatalf("hit %+v", hits[0].Doc)
	}

	// 模型的 Inputs 没有视频。
	g := setupProducts(t, textAndImage)
	id := g.files.add(flower)
	err = g.put(ctx, products, "v1", productDoc{Embed: vector.Content{vector.FilePart(id)}})
	if err == nil || !strings.Contains(err.Error(), "does not accept video input") {
		t.Fatalf("err = %v", err)
	}
}
