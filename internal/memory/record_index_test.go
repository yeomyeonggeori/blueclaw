package memory_test

import (
	"context"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/bluememo/bluememotest"
)

const (
	frameText     = `Data room document ""`
	leaseText     = `Data room document "본사 사무실 임대차계약": 월세 800만원`
	auditText     = `Data room document "2025 감사보고서": 적정 의견`
	otherText     = `Some other source's fact about rent`
	rentQuestion  = "사무실 월세 얼마 내?"
	documentsName = "company_document_recall"
)

type tallyingTableEmbedder struct {
	bluememotest.TableEmbedder
	mutex    sync.Mutex
	embedded map[string]int
}

func (embedder *tallyingTableEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	embedder.mutex.Lock()
	for _, text := range texts {
		embedder.embedded[text]++
	}
	embedder.mutex.Unlock()
	return embedder.TableEmbedder.EmbedDocuments(ctx, texts)
}

func rentEmbedder() *tallyingTableEmbedder {
	return &tallyingTableEmbedder{embedded: map[string]int{}, TableEmbedder: bluememotest.TableEmbedder{Vectors: map[string][]float32{
		frameText:    bluememotest.Axes(map[int]float32{0: 1}),
		leaseText:    bluememotest.Axes(map[int]float32{0: 1, 1: 1}),
		auditText:    bluememotest.Axes(map[int]float32{0: 1, 2: 1}),
		otherText:    bluememotest.Axes(map[int]float32{1: 1}),
		rentQuestion: bluememotest.Axes(map[int]float32{0: 1, 1: 1}),
	}}}
}

func holdRentRecords(t *testing.T, index *memory.RecordIndex) {
	t.Helper()
	ctx := context.Background()
	if errorValue := index.HoldFrame(ctx, documentsName, frameText); errorValue != nil {
		t.Fatalf("hold frame: %v", errorValue)
	}
	for _, entry := range []memory.RecordEntry{
		{ID: "lease", Content: leaseText, ChangedAt: "2026-10-01T00:00:00Z"},
		{ID: "audit", Content: auditText, ChangedAt: "2026-10-01T00:00:00Z"},
	} {
		if errorValue := index.Hold(ctx, documentsName, entry); errorValue != nil {
			t.Fatalf("hold %s: %v", entry.ID, errorValue)
		}
	}
	if errorValue := index.Hold(ctx, "another_recall", memory.RecordEntry{ID: "rent", Content: otherText, ChangedAt: "2026-10-01T00:00:00Z"}); errorValue != nil {
		t.Fatalf("hold another source: %v", errorValue)
	}
}

func TestRecordIndexAnswersOnlyWhatRisesAboveItsSourcesFrame(t *testing.T) {
	index := memorytest.OpenEmbeddingWith(t, rentEmbedder()).RecordIndex()
	holdRentRecords(t, index)

	rises, errorValue := index.Nearest(context.Background(), documentsName, rentQuestion, 0.1, 10)

	if errorValue != nil || len(rises) != 1 || rises[0].ID != "lease" {
		t.Fatalf("expected only the lease to rise above the frame, got %+v, %v", rises, errorValue)
	}
}

func TestRecordIndexAnswersNothingForASourceWithoutAFrame(t *testing.T) {
	index := memorytest.OpenEmbeddingWith(t, rentEmbedder()).RecordIndex()
	holdRentRecords(t, index)

	rises, errorValue := index.Nearest(context.Background(), "another_recall", rentQuestion, 0.1, 10)

	if errorValue != nil || len(rises) != 0 {
		t.Fatalf("expected no candidates without a frame to rise above, got %+v, %v", rises, errorValue)
	}
}

func TestRecordIndexEmbedsAnEntryAgainOnlyWhenItChanged(t *testing.T) {
	embedder := rentEmbedder()
	index := memorytest.OpenEmbeddingWith(t, embedder).RecordIndex()
	holdRentRecords(t, index)
	ctx := context.Background()

	unchanged := index.Hold(ctx, documentsName, memory.RecordEntry{ID: "lease", Content: leaseText, ChangedAt: "2026-10-01T00:00:00Z"})
	changed := index.Hold(ctx, documentsName, memory.RecordEntry{ID: "lease", Content: leaseText, ChangedAt: "2026-10-02T00:00:00Z"})

	if unchanged != nil || changed != nil || embedder.embedded[leaseText] != 2 {
		t.Fatalf("expected the lease embedded once more after it changed, got %d (%v, %v)", embedder.embedded[leaseText], unchanged, changed)
	}
}
