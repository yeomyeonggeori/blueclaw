package memory

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

const (
	SearchModeHybrid  = "hybrid"
	SearchModeLexical = "lexical"

	DefaultSearchCandidateLimit = 40
	DefaultSearchResultLimit    = 12
)

type Embedder interface {
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error)
}

type SearchService struct {
	Facts          FactRepository
	Embedder       Embedder
	CandidateLimit int
	Logger         *slog.Logger
	Now            func() time.Time
}

type SearchResult struct {
	Facts          []ScoredFact `json:"facts"`
	Mode           string       `json:"mode"`
	DegradedReason string       `json:"degradedReason,omitempty"`
}

func (service SearchService) Search(ctx context.Context, reader Reader, text string, limit int) (SearchResult, error) {
	if service.Facts == nil {
		return SearchResult{}, errors.New("memory fact repository is not configured")
	}
	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return SearchResult{}, errors.New("memory search text is required")
	}
	referenceTime := service.now()
	query := FactSearchQuery{
		Reader:         reader,
		Text:           trimmedText,
		CandidateLimit: service.candidateLimit(),
		ReferenceTime:  referenceTime,
	}
	mode, degradedReason := service.resolveSearchMode(ctx, &query)
	hits, errorValue := service.Facts.SearchFacts(ctx, query)
	if errorValue != nil {
		return SearchResult{}, errorValue
	}
	scoredFacts := limitScoredFacts(RankFacts(hits, referenceTime), limit)
	service.markRecalled(ctx, scoredFacts, referenceTime)
	return SearchResult{Facts: scoredFacts, Mode: mode, DegradedReason: degradedReason}, nil
}

func (service SearchService) resolveSearchMode(ctx context.Context, query *FactSearchQuery) (string, string) {
	if service.Embedder == nil {
		return SearchModeLexical, ""
	}
	hasVectorSearch, errorValue := service.Facts.HasVectorSearch(ctx)
	if errorValue != nil {
		return SearchModeLexical, "vector search availability check failed: " + errorValue.Error()
	}
	if !hasVectorSearch {
		return SearchModeLexical, "the database has no vector extension"
	}
	embedding, errorValue := service.Embedder.EmbedQuery(ctx, query.Text)
	if errorValue != nil {
		return SearchModeLexical, "query embedding failed: " + errorValue.Error()
	}
	if errorValue := ValidateEmbedding(embedding); errorValue != nil {
		return SearchModeLexical, "query embedding rejected: " + errorValue.Error()
	}
	query.Embedding = embedding
	return SearchModeHybrid, ""
}

func (service SearchService) markRecalled(ctx context.Context, scoredFacts []ScoredFact, recalledAt time.Time) {
	if len(scoredFacts) == 0 {
		return
	}
	factIDs := make([]string, 0, len(scoredFacts))
	for _, scoredFact := range scoredFacts {
		factIDs = append(factIDs, scoredFact.Fact.FactID)
	}
	if errorValue := service.Facts.MarkFactsRecalled(ctx, factIDs, recalledAt); errorValue != nil {
		service.logger().Warn("memory.search.mark_recalled_failed", "error", errorValue.Error(), "factCount", len(factIDs))
	}
}

func (service SearchService) candidateLimit() int {
	if service.CandidateLimit > 0 {
		return service.CandidateLimit
	}
	return DefaultSearchCandidateLimit
}

func (service SearchService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}

func (service SearchService) logger() *slog.Logger {
	if service.Logger != nil {
		return service.Logger
	}
	return slog.Default()
}

func limitScoredFacts(scoredFacts []ScoredFact, limit int) []ScoredFact {
	if limit <= 0 {
		limit = DefaultSearchResultLimit
	}
	if len(scoredFacts) <= limit {
		return scoredFacts
	}
	return scoredFacts[:limit]
}
