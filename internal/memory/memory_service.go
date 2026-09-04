package memory

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/blueclaw/internal/access"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type GraphMemoryStore interface {
	AddEpisode(context.Context, MemoryEpisode) (MemoryIngestionResult, error)
	SearchFacts(context.Context, MemorySearchRequest) ([]MemoryFact, error)
}

type GraphMemoryEpisodeDeleter interface {
	DeleteEpisode(context.Context, MemoryEpisodeDeleteRequest) (MemoryEpisodeDeleteResult, error)
}

type GraphMemoryHealthChecker interface {
	CheckHealth(context.Context) error
}

type GraphMemoryLister interface {
	ListFacts(context.Context, MemorySearchRequest) ([]MemoryFact, error)
}

type GraphMemoryMirror interface {
	SaveGraphNamespaces(context.Context, []MemoryNamespace) error
	SaveGraphEpisode(context.Context, MemoryEpisode, string, string) error
	ListAccessibleNamespaces(context.Context, MemorySearchRequest) ([]MemoryNamespace, error)
}

type GraphMemoryMirrorEditor interface {
	DeleteGraphEpisodeNamespaces(context.Context, string, []string) (bool, error)
}

type GraphMemoryReporter interface {
	ListMemoryGraph(context.Context, int) (MemoryGraph, error)
}

type GraphMemoryEpisodeReader interface {
	GetMemoryGraphEpisode(context.Context, string) (MemoryGraphEpisode, bool, error)
	ListMemoryGraphNamespacesByID(context.Context, []string) ([]MemoryGraphNamespace, error)
}

type GraphMemoryMigrator interface {
	MigrateMemoryIdentities(context.Context, []MemoryIdentityMapping) ([]MemoryIdentityMigrationResult, error)
}

type MemorySearchRequest struct {
	Query                     string   `json:"query"`
	ReaderPersonID            string   `json:"readerPersonID"`
	ReaderCircles             []string `json:"readerCircles"`
	ResourceAccessRules       []policy.ResourceAccessPolicy
	ReaderSecurityLevelRank   int               `json:"readerSecurityLevelRank"`
	ReaderGrantedClasses      []string          `json:"readerGrantedClasses"`
	ConversationID            string            `json:"conversationID"`
	AccessibleConversationIDs []string          `json:"accessibleConversationIDs"`
	Namespaces                []MemoryNamespace `json:"namespaces"`
	ExplicitNamespacesOnly    bool              `json:"explicitNamespacesOnly"`
	Limit                     int               `json:"limit"`
}

type MemoryService struct {
	mutex              sync.RWMutex
	store              GraphMemoryStore
	mirror             GraphMemoryMirror
	lastSearchError    string
	lastIngestionError string
}

func (memoryService *MemoryService) UseGraphStore(store GraphMemoryStore) {
	memoryService.store = store
}

func (memoryService *MemoryService) UseMirror(mirror GraphMemoryMirror) {
	memoryService.mirror = mirror
}

func (memoryService *MemoryService) HasGraphStore() bool {
	if memoryService == nil {
		return false
	}
	memoryService.mutex.RLock()
	defer memoryService.mutex.RUnlock()
	return memoryService.store != nil
}

func (memoryService *MemoryService) AddEpisode(ctx context.Context, episode MemoryEpisode) (MemoryIngestionResult, error) {
	if memoryService.mirror != nil {
		_ = memoryService.mirror.SaveGraphNamespaces(ctx, episode.Namespaces)
	}
	if memoryService.store == nil {
		result := MemoryIngestionResult{EpisodeID: episode.EpisodeID, NamespaceCount: len(episode.Namespaces)}
		memoryService.recordIngestionError("")
		return result, nil
	}

	result, errorValue := memoryService.store.AddEpisode(ctx, episode)
	if memoryService.mirror != nil {
		status := "succeeded"
		errorMessage := ""
		if errorValue != nil {
			status = "failed"
			errorMessage = errorValue.Error()
		}
		if mirrorError := memoryService.mirror.SaveGraphEpisode(ctx, episode, status, errorMessage); mirrorError != nil {
			memoryService.recordIngestionError("graph mirror write failed: " + mirrorError.Error())
			return MemoryIngestionResult{}, mirrorError
		}
	}
	if errorValue != nil {
		memoryService.recordIngestionError(errorValue.Error())
		return MemoryIngestionResult{}, errorValue
	}
	if result.EpisodeID == "" {
		result.EpisodeID = episode.EpisodeID
	}
	if result.NamespaceCount == 0 {
		result.NamespaceCount = len(episode.Namespaces)
	}
	memoryService.recordIngestionError("")
	return result, nil
}

func (memoryService *MemoryService) DeleteEpisode(ctx context.Context, request MemoryEpisodeDeleteRequest) (MemoryEpisodeDeleteResult, error) {
	result := MemoryEpisodeDeleteResult{EpisodeID: request.EpisodeID}
	if memoryService == nil {
		return result, nil
	}
	deleter, hasDeleter := memoryService.store.(GraphMemoryEpisodeDeleter)
	if memoryService.store != nil && !hasDeleter {
		return result, nil
	}
	if hasDeleter {
		deletedResult, errorValue := deleter.DeleteEpisode(ctx, request)
		if errorValue != nil {
			return result, errorValue
		}
		result = deletedResult
	}
	editor, hasEditor := memoryService.mirror.(GraphMemoryMirrorEditor)
	if hasEditor {
		wasDeleted, errorValue := editor.DeleteGraphEpisodeNamespaces(ctx, request.EpisodeID, request.NamespaceIDs)
		if errorValue != nil {
			return result, errorValue
		}
		result.Deleted = result.Deleted || wasDeleted
	}
	return result, nil
}

func (memoryService *MemoryService) SearchMemory(ctx context.Context, request MemorySearchRequest) ([]MemoryFact, error) {
	if request.Limit <= 0 {
		request.Limit = 12
	}
	request.Namespaces = memoryService.resolveAccessibleNamespaces(ctx, request)
	if memoryService.store == nil {
		return nil, nil
	}
	memoryFacts, errorValue := memoryService.store.SearchFacts(ctx, request)
	if errorValue != nil {
		memoryService.recordSearchError(errorValue.Error())
		return nil, errorValue
	}
	memoryService.recordSearchError("")
	return limitMemoryFacts(rankMemoryFacts(deduplicateMemoryFacts(filterReadableMemoryFacts(request, memoryFacts)), request.Query), request.Limit), nil
}

func (memoryService *MemoryService) ListMemory(ctx context.Context, request MemorySearchRequest) ([]MemoryFact, error) {
	if request.Limit <= 0 {
		request.Limit = 50
	}
	request.Namespaces = memoryService.resolveAccessibleNamespaces(ctx, request)
	lister, hasLister := memoryService.store.(GraphMemoryLister)
	if memoryService.store == nil || !hasLister {
		return nil, nil
	}
	memoryFacts, errorValue := lister.ListFacts(ctx, request)
	if errorValue != nil {
		memoryService.recordSearchError(errorValue.Error())
		return nil, errorValue
	}
	memoryService.recordSearchError("")
	return limitMemoryFacts(rankMemoryFacts(deduplicateMemoryFacts(filterReadableMemoryFacts(request, memoryFacts)), ""), request.Limit), nil
}

func filterReadableMemoryFacts(request MemorySearchRequest, memoryFacts []MemoryFact) []MemoryFact {
	filteredMemoryFacts := []MemoryFact{}
	for _, memoryFact := range memoryFacts {
		if canReadMemoryFact(request, memoryFact) {
			filteredMemoryFacts = append(filteredMemoryFacts, memoryFact)
		}
	}
	return filteredMemoryFacts
}

func rankMemoryFacts(memoryFacts []MemoryFact, query string) []MemoryFact {
	rankedMemoryFacts := append([]MemoryFact{}, memoryFacts...)
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	sort.SliceStable(rankedMemoryFacts, func(leftIndex int, rightIndex int) bool {
		leftMemoryFact := rankedMemoryFacts[leftIndex]
		rightMemoryFact := rankedMemoryFacts[rightIndex]
		leftScore := relevanceScore(leftMemoryFact, normalizedQuery)
		rightScore := relevanceScore(rightMemoryFact, normalizedQuery)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if !leftMemoryFact.ValidAt.Equal(rightMemoryFact.ValidAt) {
			return leftMemoryFact.ValidAt.After(rightMemoryFact.ValidAt)
		}
		leftSourceRank := memorySourceKindRank(leftMemoryFact.SourceKind)
		rightSourceRank := memorySourceKindRank(rightMemoryFact.SourceKind)
		if leftSourceRank != rightSourceRank {
			return leftSourceRank > rightSourceRank
		}
		return memoryFactStableKey(leftMemoryFact) < memoryFactStableKey(rightMemoryFact)
	})
	return rankedMemoryFacts
}

func relevanceScore(memoryFact MemoryFact, normalizedQuery string) float64 {
	score := memoryFact.Score
	score += float64(memorySourceKindRank(memoryFact.SourceKind)) * 0.01
	normalizedContent := strings.ToLower(memoryFact.Content)
	if normalizedQuery == "" {
		return score
	}
	if strings.Contains(normalizedContent, normalizedQuery) {
		score += 1
	}
	for _, queryTerm := range strings.Fields(normalizedQuery) {
		if strings.Contains(normalizedContent, queryTerm) {
			score += 0.25
		}
	}
	return score
}

func memorySourceKindRank(sourceKind string) int {
	switch strings.TrimSpace(sourceKind) {
	case MemorySourceKindFact:
		return 3
	case MemorySourceKindNode:
		return 2
	case MemorySourceKindEpisode:
		return 1
	default:
		return 2
	}
}

func deduplicateMemoryFacts(memoryFacts []MemoryFact) []MemoryFact {
	memoryFactByKey := map[string]MemoryFact{}
	orderedKeys := []string{}
	for _, memoryFact := range memoryFacts {
		key := memoryFactDeduplicationKey(memoryFact)
		if key == "" {
			continue
		}
		currentMemoryFact, isFound := memoryFactByKey[key]
		if !isFound {
			orderedKeys = append(orderedKeys, key)
			memoryFactByKey[key] = memoryFact
			continue
		}
		if isBetterDuplicate(memoryFact, currentMemoryFact) {
			memoryFactByKey[key] = memoryFact
		}
	}
	deduplicatedMemoryFacts := []MemoryFact{}
	for _, key := range orderedKeys {
		deduplicatedMemoryFacts = append(deduplicatedMemoryFacts, memoryFactByKey[key])
	}
	return deduplicatedMemoryFacts
}

func isBetterDuplicate(candidate MemoryFact, current MemoryFact) bool {
	candidateSourceRank := memorySourceKindRank(candidate.SourceKind)
	currentSourceRank := memorySourceKindRank(current.SourceKind)
	if candidateSourceRank != currentSourceRank {
		return candidateSourceRank > currentSourceRank
	}
	if candidate.Score != current.Score {
		return candidate.Score > current.Score
	}
	return candidate.ValidAt.After(current.ValidAt)
}

func memoryFactDeduplicationKey(memoryFact MemoryFact) string {
	content := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(memoryFact.Content))), " ")
	if content == "" {
		return ""
	}
	return memoryFact.NamespaceID + ":" + content
}

func limitMemoryFacts(memoryFacts []MemoryFact, limit int) []MemoryFact {
	if limit <= 0 || len(memoryFacts) <= limit {
		return memoryFacts
	}
	return memoryFacts[:limit]
}

func memoryFactStableKey(memoryFact MemoryFact) string {
	if strings.TrimSpace(memoryFact.FactID) != "" {
		return memoryFact.FactID
	}
	return memoryFact.ScopeType + ":" + memoryFact.NamespaceID + ":" + memoryFact.Content
}

func (memoryService *MemoryService) resolveAccessibleNamespaces(ctx context.Context, request MemorySearchRequest) []MemoryNamespace {
	namespaces := append([]MemoryNamespace{}, request.Namespaces...)
	if request.ExplicitNamespacesOnly {
		return namespaces
	}
	if memoryService.mirror == nil {
		return namespaces
	}
	mirrorNamespaces, errorValue := memoryService.mirror.ListAccessibleNamespaces(ctx, request)
	if errorValue != nil {
		return namespaces
	}
	return mergeNamespaces(namespaces, mirrorNamespaces)
}

func mergeNamespaces(leftNamespaces []MemoryNamespace, rightNamespaces []MemoryNamespace) []MemoryNamespace {
	seenNamespaceIDs := map[string]bool{}
	namespaces := []MemoryNamespace{}
	for _, namespace := range append(append([]MemoryNamespace{}, leftNamespaces...), rightNamespaces...) {
		if namespace.NamespaceID == "" || seenNamespaceIDs[namespace.NamespaceID] {
			continue
		}
		seenNamespaceIDs[namespace.NamespaceID] = true
		namespaces = append(namespaces, namespace)
	}
	return namespaces
}

func canReadMemoryFact(request MemorySearchRequest, memoryFact MemoryFact) bool {
	if !containsNamespace(request.Namespaces, memoryFact.NamespaceID) {
		return false
	}
	if request.ReaderSecurityLevelRank < memoryFact.SecurityLevelRank {
		return false
	}
	if !containsAll(request.ReaderGrantedClasses, memoryFact.RequiredClasses) {
		return false
	}
	return access.CanAccess(access.Request{
		PersonAccess: policy.PersonAccess{
			PersonID:            request.ReaderPersonID,
			Circles:             request.ReaderCircles,
			ResourceAccessRules: request.ResourceAccessRules,
		},
		Action:   access.ActionRead,
		Resource: memoryResourceForFact(request.Namespaces, memoryFact),
	})
}

func memoryResourceForFact(namespaces []MemoryNamespace, memoryFact MemoryFact) string {
	for _, namespace := range namespaces {
		if namespace.NamespaceID != memoryFact.NamespaceID {
			continue
		}
		switch namespace.ScopeType {
		case ScopeTypeCircle:
			return "memory:circle:" + namespace.ScopeCircleID
		case ScopeTypePrivate, ScopeTypeUser:
			return "memory:private:" + namespace.ScopePersonID
		case ScopeTypeConversation:
			return "memory:conversation"
		default:
			return "memory:workspace"
		}
	}
	return "memory:workspace"
}

func containsNamespace(namespaces []MemoryNamespace, namespaceID string) bool {
	for _, namespace := range namespaces {
		if namespace.NamespaceID == namespaceID {
			return true
		}
	}
	return false
}

func containsAll(grantedClasses []string, requiredClasses []string) bool {
	grantedSet := map[string]bool{}
	for _, grantedClass := range grantedClasses {
		grantedSet[grantedClass] = true
	}
	for _, requiredClass := range requiredClasses {
		if !grantedSet[requiredClass] {
			return false
		}
	}
	return true
}

func (memoryService *MemoryService) Health(ctx context.Context) MemoryHealth {
	memoryService.mutex.RLock()
	lastSearchError := memoryService.lastSearchError
	lastIngestionError := memoryService.lastIngestionError
	store := memoryService.store
	memoryService.mutex.RUnlock()

	if store == nil {
		return MemoryHealth{
			Configured:         false,
			LastSearchError:    lastSearchError,
			LastIngestionError: lastIngestionError,
			Error:              "graphiti store is not configured",
		}
	}
	health := MemoryHealth{
		Configured:         true,
		Reachable:          true,
		LastSearchError:    lastSearchError,
		LastIngestionError: lastIngestionError,
	}
	if healthChecker, hasHealthChecker := store.(GraphMemoryHealthChecker); hasHealthChecker {
		if errorValue := healthChecker.CheckHealth(ctx); errorValue != nil {
			health.Reachable = false
			health.Error = errorValue.Error()
			return health
		}
	}
	if lastSearchError != "" {
		health.Reachable = false
		health.Error = lastSearchError
	}
	return health
}

func (memoryService *MemoryService) recordSearchError(errorMessage string) {
	memoryService.mutex.Lock()
	defer memoryService.mutex.Unlock()
	memoryService.lastSearchError = strings.TrimSpace(errorMessage)
}

func (memoryService *MemoryService) recordIngestionError(errorMessage string) {
	memoryService.mutex.Lock()
	defer memoryService.mutex.Unlock()
	memoryService.lastIngestionError = strings.TrimSpace(errorMessage)
}
