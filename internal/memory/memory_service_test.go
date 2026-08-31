package memory

import (
	"context"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func TestMemoryServiceSeparatesUserWorkspaceAndConversationNamespaces(t *testing.T) {
	memoryService := &MemoryService{}
	memoryService.StoreMemoryFact(MemoryFact{
		ScopeType:   ScopeTypeUser,
		NamespaceID: "user:person-1",
		Content:     "the user's name is Sam.",
	})
	memoryService.StoreMemoryFact(MemoryFact{
		ScopeType:         ScopeTypeWorkspace,
		NamespaceID:       WorkspaceNamespace("default", 50, []string{"finance"}).NamespaceID,
		Content:           "only the finance team uses the corporate card.",
		SecurityLevelRank: 50,
		RequiredClasses:   []string{"finance"},
	})
	memoryService.StoreMemoryFact(MemoryFact{
		ScopeType:         ScopeTypeConversation,
		NamespaceID:       ConversationNamespace("channel-1", 10, []string{"internal"}).NamespaceID,
		Content:           "this channel is for release meetings.",
		SecurityLevelRank: 10,
		RequiredClasses:   []string{"internal"},
	})

	personOneFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		ReaderPersonID:          "person-1",
		ReaderSecurityLevelRank: 100,
		ReaderGrantedClasses:    []string{"internal", "finance"},
		Namespaces: []MemoryNamespace{
			UserNamespace("person-1"),
			WorkspaceNamespace("default", 50, []string{"finance"}),
			ConversationNamespace("channel-1", 10, []string{"internal"}),
		},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if len(personOneFacts) != 3 {
		t.Fatalf("expected user, workspace, and conversation memory, got %d", len(personOneFacts))
	}

	personTwoFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		ReaderPersonID:          "person-2",
		ReaderSecurityLevelRank: 100,
		ReaderGrantedClasses:    []string{"internal", "finance"},
		Namespaces: []MemoryNamespace{
			UserNamespace("person-2"),
			WorkspaceNamespace("default", 50, []string{"finance"}),
			ConversationNamespace("channel-1", 10, []string{"internal"}),
		},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if containsMemory(personTwoFacts, "the user's name is Sam.") {
		t.Fatal("expected other user not to read person-1 user memory")
	}

	lowAccessFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		ReaderPersonID:          "person-1",
		ReaderSecurityLevelRank: 10,
		ReaderGrantedClasses:    []string{"internal"},
		Namespaces: []MemoryNamespace{
			UserNamespace("person-1"),
			WorkspaceNamespace("default", 50, []string{"finance"}),
			ConversationNamespace("channel-2", 10, []string{"internal"}),
		},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if containsMemory(lowAccessFacts, "only the finance team uses the corporate card.") {
		t.Fatal("expected workspace memory to respect security classes")
	}
	if containsMemory(lowAccessFacts, "this channel is for release meetings.") {
		t.Fatal("expected conversation memory to stay in its conversation")
	}
}

func TestMemoryServiceRanksAfterPolicyFiltering(t *testing.T) {
	memoryService := &MemoryService{}
	olderTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newerTime := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:            "inaccessible",
		ScopeType:         ScopeTypeWorkspace,
		NamespaceID:       WorkspaceNamespace("default", 80, []string{"finance"}).NamespaceID,
		Content:           "the Project Aurora budget is confidential.",
		Score:             50,
		SecurityLevelRank: 80,
		RequiredClasses:   []string{"finance"},
		ValidAt:           newerTime,
	})
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "query-match",
		ScopeType:   ScopeTypeUser,
		NamespaceID: UserNamespace("person-1").NamespaceID,
		Content:     "the user prioritizes Project Aurora.",
		Score:       0.1,
		ValidAt:     olderTime,
	})
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "high-score",
		ScopeType:   ScopeTypeUser,
		NamespaceID: UserNamespace("person-1").NamespaceID,
		Content:     "the user prefers a concise design.",
		Score:       0.9,
		ValidAt:     newerTime,
	})
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "old-low-score",
		ScopeType:   ScopeTypeUser,
		NamespaceID: UserNamespace("person-1").NamespaceID,
		Content:     "the user avoids Monday morning meetings.",
		Score:       0.2,
		ValidAt:     olderTime,
	})

	memoryFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		Query:                   "Project Aurora",
		ReaderPersonID:          "person-1",
		ReaderSecurityLevelRank: 10,
		ReaderGrantedClasses:    []string{"internal"},
		Limit:                   2,
		Namespaces: []MemoryNamespace{
			UserNamespace("person-1"),
			WorkspaceNamespace("default", 80, []string{"finance"}),
		},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}

	if len(memoryFacts) != 2 {
		t.Fatalf("expected limit after ranking, got %d", len(memoryFacts))
	}
	if memoryFacts[0].FactID != "query-match" {
		t.Fatalf("expected query match first, got %+v", memoryFacts)
	}
	if memoryFacts[1].FactID != "high-score" {
		t.Fatalf("expected score-ranked fact second, got %+v", memoryFacts)
	}
	if containsMemory(memoryFacts, "the Project Aurora budget is confidential.") {
		t.Fatal("expected inaccessible high-score memory to be filtered before ranking")
	}
}

func TestMemoryServiceFiltersPrivateAndCircleResources(t *testing.T) {
	memoryService := &MemoryService{}
	privateNamespace := PrivatePersonNamespace("person-1")
	financeNamespace := CircleNamespace("default", "finance")
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "private",
		ScopeType:   ScopeTypePrivate,
		NamespaceID: privateNamespace.NamespaceID,
		Content:     "a one-on-one note between Sam and the bot.",
	})
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "finance",
		ScopeType:   ScopeTypeCircle,
		NamespaceID: financeNamespace.NamespaceID,
		Content:     "finance circle material.",
	})

	ownerFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		ReaderPersonID: "person-1",
		ReaderCircles:  []string{"member", "finance"},
		Namespaces:     []MemoryNamespace{privateNamespace, financeNamespace},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if !containsMemory(ownerFacts, "a one-on-one note between Sam and the bot.") || !containsMemory(ownerFacts, "finance circle material.") {
		t.Fatalf("expected owner finance access, got %+v", ownerFacts)
	}

	otherFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		ReaderPersonID: "person-2",
		ReaderCircles:  []string{"member"},
		Namespaces:     []MemoryNamespace{privateNamespace, financeNamespace},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if containsMemory(otherFacts, "a one-on-one note between Sam and the bot.") {
		t.Fatal("expected other person not to read private memory")
	}
	if containsMemory(otherFacts, "finance circle material.") {
		t.Fatal("expected finance non-member not to read finance memory")
	}
}

func TestMemoryServiceAppliesResourceAccessRulesBeforeRanking(t *testing.T) {
	memoryService := &MemoryService{}
	resourceAccessRules := []policy.ResourceAccessPolicy{{
		Resource: "memory:workspace",
		Actions:  []string{"read"},
		Circles:  []string{"admin"},
	}}
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "workspace-secret",
		ScopeType:   ScopeTypeWorkspace,
		NamespaceID: WorkspaceNamespace("default", 0, nil).NamespaceID,
		Content:     "a workspace-wide note that a rule still blocks.",
		Score:       100,
	})
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "private",
		ScopeType:   ScopeTypePrivate,
		NamespaceID: PrivatePersonNamespace("person-1").NamespaceID,
		Content:     "a low-scoring personal note.",
		Score:       1,
	})

	memoryFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		ReaderPersonID:      "person-1",
		ReaderCircles:       []string{"member"},
		ResourceAccessRules: resourceAccessRules,
		Namespaces: []MemoryNamespace{
			WorkspaceNamespace("default", 0, nil),
			PrivatePersonNamespace("person-1"),
		},
	})
	if errorValue != nil {
		t.Fatalf("expected search to succeed: %v", errorValue)
	}
	if containsMemory(memoryFacts, "a workspace-wide note that a rule still blocks.") {
		t.Fatal("expected resource access rule to filter workspace memory before ranking")
	}
	if !containsMemory(memoryFacts, "a low-scoring personal note.") {
		t.Fatalf("expected private memory to remain, got %+v", memoryFacts)
	}
}

func TestMemoryServiceRanksSourceKindAndDeduplicatesBeforeLimit(t *testing.T) {
	memoryService := &MemoryService{}
	namespace := UserNamespace("person-1")
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "episode",
		ScopeType:   ScopeTypeUser,
		NamespaceID: namespace.NamespaceID,
		Content:     "the user prefers graph memory.",
		Score:       0.9,
		SourceKind:  MemorySourceKindEpisode,
	})
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "fact",
		ScopeType:   ScopeTypeUser,
		NamespaceID: namespace.NamespaceID,
		Content:     "the user prefers graph memory.",
		Score:       0.8,
		SourceKind:  MemorySourceKindFact,
	})
	memoryService.StoreMemoryFact(MemoryFact{
		FactID:      "node",
		ScopeType:   ScopeTypeUser,
		NamespaceID: namespace.NamespaceID,
		Content:     "the user prefers a concise design.",
		Score:       0.85,
		SourceKind:  MemorySourceKindNode,
	})

	memoryFacts, errorValue := memoryService.SearchMemory(context.Background(), MemorySearchRequest{
		Query:          "graph memory",
		ReaderPersonID: "person-1",
		Limit:          2,
		Namespaces:     []MemoryNamespace{namespace},
	})
	if errorValue != nil {
		t.Fatalf("expected memory search to succeed: %v", errorValue)
	}
	if len(memoryFacts) != 2 {
		t.Fatalf("expected deduplicated limited facts, got %+v", memoryFacts)
	}
	if memoryFacts[0].SourceKind != MemorySourceKindFact {
		t.Fatalf("expected durable fact to outrank duplicate episode, got %+v", memoryFacts)
	}
	if containsFactID(memoryFacts, "episode") {
		t.Fatalf("expected duplicate raw episode to be removed, got %+v", memoryFacts)
	}
}

func containsMemory(memoryFacts []MemoryFact, content string) bool {
	for _, memoryFact := range memoryFacts {
		if memoryFact.Content == content {
			return true
		}
	}
	return false
}

func containsFactID(memoryFacts []MemoryFact, factID string) bool {
	for _, memoryFact := range memoryFacts {
		if memoryFact.FactID == factID {
			return true
		}
	}
	return false
}

type answeringGraphStore struct{}

func (answeringGraphStore) AddEpisode(context.Context, MemoryEpisode) (MemoryIngestionResult, error) {
	return MemoryIngestionResult{}, nil
}

func (answeringGraphStore) SearchFacts(context.Context, MemorySearchRequest) ([]MemoryFact, error) {
	return nil, nil
}

func (answeringGraphStore) CheckHealth(context.Context) error {
	return nil
}

func TestMemoryHealthReportsTheCapabilityRatherThanTheDaemon(t *testing.T) {
	memoryService := &MemoryService{store: answeringGraphStore{}}

	health := memoryService.Health(context.Background())
	if !health.Reachable || health.Error != "" {
		t.Fatalf("a store answering its health check must be reachable, got %+v", health)
	}

	const searchFailure = "AttributeError: 'CapabilityLLMClient' object has no attribute 'set_tracer'"
	memoryService.recordSearchError(searchFailure)
	health = memoryService.Health(context.Background())
	if health.Reachable {
		t.Fatalf("memory whose every search fails must not report itself reachable, got %+v", health)
	}
	if health.Error != searchFailure {
		t.Fatalf("the search failure must be the reported error, got %q", health.Error)
	}

	memoryService.recordSearchError("")
	if health := memoryService.Health(context.Background()); !health.Reachable || health.Error != "" {
		t.Fatalf("a later successful search must clear the health error, got %+v", health)
	}
}
