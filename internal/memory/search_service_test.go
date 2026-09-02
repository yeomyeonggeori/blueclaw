package memory

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeFactRepository struct {
	hasVectorSearch bool
	hits            []RankedFact
	lastQuery       FactSearchQuery
	recalledFactIDs []string
}

func (repository *fakeFactRepository) HasVectorSearch(context.Context) (bool, error) {
	return repository.hasVectorSearch, nil
}

func (repository *fakeFactRepository) SaveEpisode(context.Context, EpisodeWrite) error {
	return errors.New("not used")
}

func (repository *fakeFactRepository) SearchFacts(_ context.Context, query FactSearchQuery) ([]RankedFact, error) {
	repository.lastQuery = query
	return repository.hits, nil
}

func (repository *fakeFactRepository) ListFactsByID(context.Context, Reader, []string, time.Time) ([]Fact, error) {
	return nil, errors.New("not used")
}

func (repository *fakeFactRepository) ListReadableFacts(context.Context, Reader, int, time.Time) ([]Fact, error) {
	return nil, errors.New("not used")
}

func (repository *fakeFactRepository) ListLiveFactsAboutPerson(context.Context, string, time.Time) ([]Fact, error) {
	return nil, errors.New("not used")
}

func (repository *fakeFactRepository) MarkFactsRecalled(_ context.Context, factIDs []string, _ time.Time) error {
	repository.recalledFactIDs = factIDs
	return nil
}

func (repository *fakeFactRepository) ForgetFacts(context.Context, Reader, []string, string, time.Time) ([]string, error) {
	return nil, errors.New("not used")
}

type fakeEmbedder struct {
	embedding []float32
	failure   error
}

func (embedder fakeEmbedder) EmbedQuery(context.Context, string) ([]float32, error) {
	return embedder.embedding, embedder.failure
}

func (embedder fakeEmbedder) EmbedDocuments(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("not used")
}

func rankedHits(count int) []RankedFact {
	hits := make([]RankedFact, 0, count)
	for index := 0; index < count; index++ {
		hits = append(hits, RankedFact{Fact: Fact{FactID: string(rune('a' + index)), Kind: FactKindFact}, LexicalRank: index + 1})
	}
	return hits
}

func TestSearchServiceRunsHybridWhenEmbeddingsAndVectorsExist(t *testing.T) {
	repository := &fakeFactRepository{hasVectorSearch: true, hits: rankedHits(3)}
	service := SearchService{Facts: repository, Embedder: fakeEmbedder{embedding: make([]float32, EmbeddingDimensionCount)}}
	result, errorValue := service.Search(context.Background(), Reader{PersonID: "person-1"}, "  Q3 review  ", 2)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Mode != SearchModeHybrid || result.DegradedReason != "" {
		t.Fatalf("expected a hybrid search, got mode=%s reason=%q", result.Mode, result.DegradedReason)
	}
	if len(repository.lastQuery.Embedding) != EmbeddingDimensionCount || repository.lastQuery.Text != "Q3 review" {
		t.Fatalf("expected the query embedding and trimmed text to reach the repository, got %+v", repository.lastQuery)
	}
	if len(result.Facts) != 2 || len(repository.recalledFactIDs) != 2 {
		t.Fatalf("expected the result limited to 2 and marked recalled, got %d results, %v recalled", len(result.Facts), repository.recalledFactIDs)
	}
}

func TestSearchServiceFallsBackToLexicalAndSaysWhy(t *testing.T) {
	cases := map[string]struct {
		repository *fakeFactRepository
		embedder   Embedder
		reason     string
	}{
		"no embedder":      {&fakeFactRepository{hasVectorSearch: true}, nil, ""},
		"no vector table":  {&fakeFactRepository{hasVectorSearch: false}, fakeEmbedder{}, "the database has no vector extension"},
		"embedding failed": {&fakeFactRepository{hasVectorSearch: true}, fakeEmbedder{failure: errors.New("gateway down")}, "query embedding failed: gateway down"},
		"wrong dimensions": {&fakeFactRepository{hasVectorSearch: true}, fakeEmbedder{embedding: make([]float32, 8)}, "query embedding rejected: embedding has 8 dimensions, memory stores 1024"},
	}
	for name, testCase := range cases {
		result, errorValue := SearchService{Facts: testCase.repository, Embedder: testCase.embedder}.Search(context.Background(), Reader{}, "anything", 5)
		if errorValue != nil {
			t.Fatalf("%s: %v", name, errorValue)
		}
		if result.Mode != SearchModeLexical || result.DegradedReason != testCase.reason {
			t.Fatalf("%s: expected lexical with reason %q, got mode=%s reason=%q", name, testCase.reason, result.Mode, result.DegradedReason)
		}
		if len(testCase.repository.lastQuery.Embedding) != 0 {
			t.Fatalf("%s: expected no embedding to reach the repository", name)
		}
	}
}

func TestSearchServiceRejectsEmptyText(t *testing.T) {
	if _, errorValue := (SearchService{Facts: &fakeFactRepository{}}).Search(context.Background(), Reader{}, "   ", 5); errorValue == nil {
		t.Fatal("expected empty search text to be rejected")
	}
}
