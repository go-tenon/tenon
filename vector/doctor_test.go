package vector_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/ai/aitest"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/vector"
	"github.com/go-tenon/tenon/vector/vectortest"
)

// indexResult 返回 store 上 chunks 索引的检查结果，并确认 Qdrant 连得上。
func indexResult(t *testing.T, store *vector.Store) doctor.Result {
	t.Helper()
	results := doctor.Run(context.Background(), vector.Doctor(store, chunks.Name())).Results
	if results[0].Check != "qdrant" || results[0].Status != doctor.Passed {
		t.Fatalf("qdrant = %+v", results[0])
	}
	return results[1]
}

func TestDoctor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t)
	shared := newShared(t, f.db)
	oldModel := vector.Model{Name: "old", Dims: dims, Embedder: aitest.NewEmbedder(dims)}
	old := shared.store(oldModel)
	if res := indexResult(t, old); res.Status != doctor.Passed {
		t.Errorf("before the index is built: %+v", res)
	}
	f.src.set("a", chunkDoc{Text: "付款条件"})
	if err := chunks.Refresh(ctx, old, f.src.load, "a"); err != nil {
		t.Fatal(err)
	}
	if res := indexResult(t, old); res.Status != doctor.Passed {
		t.Errorf("built with the configured model: %+v", res)
	}

	newModel := vector.Model{Name: "new", Dims: dims / 2, Embedder: aitest.NewEmbedder(dims / 2)}
	res := indexResult(t, shared.store(newModel, vector.WithPrevious(oldModel)))
	if res.Status != doctor.Warning || !strings.Contains(res.Summary, `previous model "old"`) ||
		!strings.Contains(res.Fix, "search reindex "+chunks.Name()) {
		t.Errorf("still on the previous model: %+v", res)
	}
	res = indexResult(t, shared.store(newModel))
	if res.Status != doctor.Failed || !strings.Contains(res.Summary, "not configured") || !strings.Contains(res.Fix, "WithPrevious") {
		t.Errorf("model changed without WithPrevious: %+v", res)
	}
}

func TestDoctorBeforeMigrations(t *testing.T) {
	t.Parallel()
	store := vectortest.New(t, dbtest.Open(t, nil), vector.Model{Name: "m", Dims: dims, Embedder: aitest.NewEmbedder(dims)})
	if res := indexResult(t, store); res.Status != doctor.Skipped {
		t.Errorf("before migrations: %+v", res)
	}
}
