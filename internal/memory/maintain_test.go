package memory_test

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/bluememo/bluememotest"
)

func TestMaintenanceReachesEveryMemoryFile(t *testing.T) {
	stores, root := memorytest.OpenWithRoot(t)
	for _, scope := range []memory.Scope{memory.PersonScope("person-1"), memory.CircleScope("leadership"), memory.WorkspaceScope()} {
		memorytest.Remember(t, stores, scope, "이샘플 keeps the "+scope.Kind+" ledger")
	}
	if errorValue := stores.Close(); errorValue != nil {
		t.Fatalf("close: %v", errorValue)
	}
	embedder := &countingEmbedder{}
	reopened := memory.NewStores(root, bluememo.Configuration{Embedder: embedder, EmbeddingModel: "another-embed"}, memorytest.ReadsInThisProcess())
	t.Cleanup(func() { _ = reopened.Close() })

	if _, errorValue := reopened.Maintain(context.Background()); errorValue != nil {
		t.Fatalf("maintain: %v", errorValue)
	}

	if embedder.documentCount != 3 {
		t.Fatalf("a change of embedding model re-embedded %d of the 3 memories on disk", embedder.documentCount)
	}
}

type countingEmbedder struct {
	bluememotest.HashEmbedder
	documentCount int
}

func (embedder *countingEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	embedder.documentCount += len(texts)
	return embedder.HashEmbedder.EmbedDocuments(ctx, texts)
}
