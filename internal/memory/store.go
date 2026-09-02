package memory

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

const DefaultEmbeddingModelName = "qwen/qwen3-embedding-8b"

const (
	DefaultProfileCharacterBudget  = 1200
	DefaultRecalledCharacterBudget = 2400
)

type Store struct {
	Facts          FactRepository
	Profiles       ProfileRepository
	Jobs           JobRepository
	Embedder       Embedder
	EmbeddingModel string
	Logger         *slog.Logger
	Now            func() time.Time
}

type RecallRequest struct {
	Reader         Reader
	PersonID       string
	Query          string
	Limit          int
	ProfileBudget  int
	RecalledBudget int
}

type Recall struct {
	Profile        Profile      `json:"profile"`
	Facts          []ScoredFact `json:"facts"`
	Mode           string       `json:"mode"`
	DegradedReason string       `json:"degradedReason,omitempty"`
}

type SecurityLabel struct {
	SecurityLevelRank int
	RequiredClasses   []string
}

func (store Store) Recall(ctx context.Context, request RecallRequest) (Recall, error) {
	recall := Recall{Mode: SearchModeLexical}
	if store.Profiles != nil && strings.TrimSpace(request.PersonID) != "" {
		profile, isFound, errorValue := store.Profiles.FindProfile(ctx, request.PersonID)
		if errorValue != nil {
			return Recall{}, errorValue
		}
		if isFound {
			recall.Profile = profile
		}
	}
	if strings.TrimSpace(request.Query) != "" {
		searchResult, errorValue := store.Search(ctx, request.Reader, request.Query, request.Limit)
		if errorValue != nil {
			return Recall{}, errorValue
		}
		recall.Facts = searchResult.Facts
		recall.Mode = searchResult.Mode
		recall.DegradedReason = searchResult.DegradedReason
	}
	return BudgetRecall(recall, request.ProfileBudget, request.RecalledBudget), nil
}

func (store Store) Search(ctx context.Context, reader Reader, query string, limit int) (SearchResult, error) {
	return SearchService{Facts: store.Facts, Embedder: store.Embedder, Logger: store.Logger, Now: store.Now}.Search(ctx, reader, query, limit)
}

func (store Store) Forget(ctx context.Context, reader Reader, factIDs []string, reason string) ([]string, error) {
	if store.Facts == nil {
		return nil, errors.New("memory fact repository is not configured")
	}
	forgottenFactIDs, errorValue := store.Facts.ForgetFacts(ctx, reader, factIDs, reason, store.now())
	if errorValue != nil {
		return nil, errorValue
	}
	if len(forgottenFactIDs) > 0 {
		store.enqueueProfileRebuild(ctx, reader.PersonID)
	}
	return forgottenFactIDs, nil
}

func (store Store) EnqueueExtraction(ctx context.Context, taskRunID string) (Job, bool, error) {
	if store.Jobs == nil {
		return Job{}, false, errors.New("memory job repository is not configured")
	}
	return store.Jobs.EnqueueJob(ctx, JobKindExtract, taskRunID, store.now())
}

func (store Store) enqueueProfileRebuild(ctx context.Context, personID string) {
	if store.Jobs == nil || strings.TrimSpace(personID) == "" {
		return
	}
	if _, _, errorValue := store.Jobs.EnqueueJob(ctx, JobKindProfile, personID, store.now()); errorValue != nil {
		store.logger().Warn("memory.profile.enqueue_failed", "personID", personID, "error", errorValue.Error())
	}
}

func (store Store) now() time.Time {
	if store.Now != nil {
		return store.Now().UTC()
	}
	return time.Now().UTC()
}

func (store Store) logger() *slog.Logger {
	if store.Logger != nil {
		return store.Logger
	}
	return slog.Default()
}

func BudgetRecall(recall Recall, profileBudget int, recalledBudget int) Recall {
	if profileBudget <= 0 {
		profileBudget = DefaultProfileCharacterBudget
	}
	if recalledBudget <= 0 {
		recalledBudget = DefaultRecalledCharacterBudget
	}
	recall.Profile.IdentityLines, profileBudget = takeLinesWithinBudget(recall.Profile.IdentityLines, profileBudget)
	recall.Profile.CurrentLines, _ = takeLinesWithinBudget(recall.Profile.CurrentLines, profileBudget)
	budgetedFacts := make([]ScoredFact, 0, len(recall.Facts))
	for _, scoredFact := range recall.Facts {
		cost := len([]rune(scoredFact.Fact.Content))
		if cost > recalledBudget {
			break
		}
		recalledBudget -= cost
		budgetedFacts = append(budgetedFacts, scoredFact)
	}
	recall.Facts = budgetedFacts
	return recall
}

func takeLinesWithinBudget(lines []string, budget int) ([]string, int) {
	taken := make([]string, 0, len(lines))
	for _, line := range lines {
		cost := len([]rune(line))
		if cost > budget {
			break
		}
		budget -= cost
		taken = append(taken, line)
	}
	return taken, budget
}

func (recall Recall) ProfileLines() []string {
	return append(append([]string{}, recall.Profile.IdentityLines...), recall.Profile.CurrentLines...)
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func nonNilStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
