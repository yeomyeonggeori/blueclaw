package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func TestMemoryRememberToolEnqueuesPersonMemory(t *testing.T) {
	queue := &recordingMemoryUpdateQueue{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryUpdateQueue(queue)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_remember"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_remember",
		Input:    toolcontract.MarshalToolInput(map[string]string{"content": "Call the user master."}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected memory_remember success, got %s", result.ContentText())
	}
	if len(queue.jobs) != 1 {
		t.Fatalf("expected one queued memory job, got %+v", queue.jobs)
	}
	job := queue.jobs[0]
	if job.Namespace.NamespaceID != memory.UserNamespace("person-1").NamespaceID || job.Content != "Call the user master." {
		t.Fatalf("expected person memory job, got %+v", job)
	}
	if !strings.Contains(result.ContentText(), `"accepted":true`) {
		t.Fatalf("expected accepted result, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), `"status":"queued_volatile"`) {
		t.Fatalf("expected queued volatile result, got %s", result.ContentText())
	}
}

func TestMemoryRememberToolLeavesMeaningToTheModel(t *testing.T) {
	queue := &recordingMemoryUpdateQueue{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryUpdateQueue(queue)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_remember"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_remember",
		Input:    toolcontract.MarshalToolInput(map[string]string{"content": "thanks"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected explicit model tool call to remain authoritative, got %s", result.ContentText())
	}
	if len(queue.jobs) != 1 || queue.jobs[0].Content != "thanks" {
		t.Fatalf("expected explicit content to be queued without phrase filtering, got %+v", queue.jobs)
	}
	if len(result.Effects) != 1 || result.Effects[0].ID != "job-1" {
		t.Fatalf("expected exact memory update effect, got %+v", result.Effects)
	}
}

func TestMemoryRememberToolRejectsInvalidBoundaryInput(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryUpdateQueue(&recordingMemoryUpdateQueue{})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_remember"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})

	for _, input := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"content":"   "}`),
		json.RawMessage(`{"content":"durable fact","reason":"model judgment"}`),
		toolcontract.MarshalToolInput(map[string]string{"content": strings.Repeat("a", memory.RememberContentRuneLimit+1)}),
	} {
		result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
			ToolName: "memory_remember",
			Input:    input,
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() || result.FailureStage() != "tool_input_schema" {
			t.Fatalf("expected strict boundary rejection for %s, got %+v", input, result)
		}
	}
}

func TestMemoryRememberToolReportsQueueFailure(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_remember"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_remember",
		Input:    toolcontract.MarshalToolInput(map[string]string{"content": "The requester prefers short reports."}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureCode() != toolcontract.FailureCodes.OperationFailed.String() {
		t.Fatalf("expected queue failure to remain a failed observation, got %+v", result)
	}
	document := decodeMemoryUpdateAccepted(t, string(result.Output.Data))
	if document.Accepted || document.Status != "failed" || document.FailureCode != "queue_unavailable" {
		t.Fatalf("expected typed queue failure, got %+v", document)
	}
}

func TestMemoryRememberToolRejectsInaccessibleActiveCircle(t *testing.T) {
	queue := &recordingMemoryUpdateQueue{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryUpdateQueue(queue)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_remember"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{Circles: []string{"member"}},
		ActiveCircleID:    "admin",
		MemoryNamespaces:  []memory.MemoryNamespace{memory.CircleNamespace("default", "member")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_remember",
		Input:    toolcontract.MarshalToolInput(map[string]string{"content": "Shared circle fact."}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected inaccessible circle error, got %s", result.ContentText())
	}
	if len(queue.jobs) != 0 {
		t.Fatalf("expected no queued jobs, got %+v", queue.jobs)
	}
}

func TestMemoryRememberToolEnqueuesCircleMemoryForActiveCircle(t *testing.T) {
	queue := &recordingMemoryUpdateQueue{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryUpdateQueue(queue)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_remember"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{Circles: []string{"member", "hr-compensation"}},
		ActiveCircleID:    "hr-compensation",
		MemoryNamespaces:  []memory.MemoryNamespace{memory.CircleNamespace("default", "hr-compensation")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_remember",
		Input:    toolcontract.MarshalToolInput(map[string]string{"content": "Compensation data belongs to HR."}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected memory_remember success, got %s", result.ContentText())
	}
	if len(queue.jobs) != 1 {
		t.Fatalf("expected one queued memory job, got %+v", queue.jobs)
	}
	if queue.jobs[0].Namespace.ScopeType != memory.ScopeTypeCircle || queue.jobs[0].Namespace.ScopeCircleID != "hr-compensation" {
		t.Fatalf("expected circle memory job, got %+v", queue.jobs[0])
	}
}

func TestMemoryRememberToolRejectsMultipleActiveCircleCandidates(t *testing.T) {
	queue := &recordingMemoryUpdateQueue{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryUpdateQueue(queue)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_remember"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:             "default",
		RequesterPersonID:       "person-1",
		Prompt:                  "@admin @hr-compensation remember this",
		ConversationChannelName: "town-square",
		PersonAccess:            policy.PersonAccess{Circles: []string{"member", "admin", "hr-compensation"}},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_remember",
		Input:    toolcontract.MarshalToolInput(map[string]string{"content": "Shared fact."}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected active circle conflict, got %s", result.ContentText())
	}
	if len(queue.jobs) != 0 {
		t.Fatalf("expected no queued jobs, got %+v", queue.jobs)
	}
}

func TestMemorySearchUsesPersonAndActiveCircleNamespaces(t *testing.T) {
	memoryService := &memory.MemoryService{}
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeUser,
		NamespaceID: memory.UserNamespace("person-1").NamespaceID,
		Content:     "Call the user master.",
		SourceKind:  memory.MemorySourceKindFact,
	})
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeCircle,
		NamespaceID: memory.CircleNamespace("default", "hr-compensation").NamespaceID,
		Content:     "Salary files stay in HR compensation.",
		SourceKind:  memory.MemorySourceKindFact,
	})
	memoryService.StoreMemoryFact(memory.MemoryFact{
		ScopeType:   memory.ScopeTypeCircle,
		NamespaceID: memory.CircleNamespace("default", "admin").NamespaceID,
		Content:     "Admin-only operational memory.",
		SourceKind:  memory.MemorySourceKindFact,
	})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_search"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess: policy.PersonAccess{
			PersonID: "person-1",
			Circles:  []string{"member", "hr-compensation", "admin"},
		},
		ActiveCircleID: "hr-compensation",
		MemoryNamespaces: []memory.MemoryNamespace{
			memory.UserNamespace("person-1"),
			memory.CircleNamespace("default", "hr-compensation"),
			memory.CircleNamespace("default", "admin"),
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_search",
		Input:    toolcontract.MarshalToolInput(map[string]string{"query": "memory"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected memory_search success, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), "master") || !strings.Contains(result.ContentText(), "Salary files") {
		t.Fatalf("expected person and active circle memory, got %s", result.ContentText())
	}
	if strings.Contains(result.ContentText(), "Admin-only") {
		t.Fatalf("expected inactive circle memory to be excluded, got %s", result.ContentText())
	}
}

func TestMemorySearchRequiresExplicitNonblankQuery(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_search"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName: "default",
		Prompt:      "Use this prompt as an implicit memory query.",
	})

	for _, input := range []json.RawMessage{
		json.RawMessage(`{}`),
		toolcontract.MarshalToolInput(map[string]string{"query": " \t "}),
	} {
		result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
			ToolName: "memory_search",
			Input:    input,
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if !result.Failed() || result.FailureCode() != toolcontract.FailureCodes.InvalidInput.String() {
			t.Fatalf("expected explicit query rejection, got %+v", result)
		}
	}
}

func TestMemorySearchProjectsCompleteGraphResult(t *testing.T) {
	validAt := time.Date(2026, time.July, 19, 4, 30, 0, 0, time.UTC)
	namespace := memory.UserNamespace("person-1")
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(staticGraphMemoryStore{facts: []memory.MemoryFact{{
		FactID:            "fact-1",
		ScopeType:         memory.ScopeTypeUser,
		NamespaceID:       namespace.NamespaceID,
		Content:           "The requester prefers concise reports.",
		Score:             0.91,
		SourceEpisodeID:   "episode-secret",
		SourceKind:        memory.MemorySourceKindFact,
		ValidAt:           validAt,
		SecurityLevelRank: 9,
		RequiredClasses:   []string{"executive-secret"},
	}}})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_search"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 10, GrantedClasses: []string{"executive-secret"}},
		MemoryNamespaces:  []memory.MemoryNamespace{namespace},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_search",
		Input:    toolcontract.MarshalToolInput(map[string]string{"query": "reports"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected complete memory search, got %+v", result)
	}
	document := decodeMemorySearchToolOutput(t, result.ContentText())
	if document.SearchStatus != "complete" || len(document.Sources) != 1 || document.Sources[0] != "graph_memory" {
		t.Fatalf("expected complete graph source, got %+v", document)
	}
	if len(document.Facts) != 1 || document.Facts[0].FactID != "fact-1" || document.Facts[0].Score == nil || *document.Facts[0].Score != 0.91 {
		t.Fatalf("expected projected graph fact, got %+v", document.Facts)
	}
	for _, privateField := range []string{"namespaceID", "securityLevelRank", "requiredClasses", "sourceEpisodeID", namespace.NamespaceID, "episode-secret", "executive-secret"} {
		if strings.Contains(result.ContentText(), privateField) {
			t.Fatalf("expected model-safe result without %q, got %s", privateField, result.ContentText())
		}
	}
}

func TestMemorySearchNormalizesEmptyGraphResult(t *testing.T) {
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(staticGraphMemoryStore{})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_search"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_search",
		Input:    toolcontract.MarshalToolInput(map[string]string{"query": "missing"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected successful empty graph result, got %+v", result)
	}
	document := decodeMemorySearchToolOutput(t, result.ContentText())
	if document.Facts == nil || len(document.Facts) != 0 || document.Sources == nil {
		t.Fatalf("expected normalized arrays, got %+v", document)
	}
	if document.SearchStatus != "complete" || len(document.Sources) != 1 || document.Sources[0] != "graph_memory" {
		t.Fatalf("expected empty complete graph result, got %+v", document)
	}
}

func TestMemorySearchReturnsRecoverableToolErrorWhenGraphitiFails(t *testing.T) {
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(failingGraphMemoryStore{errorValue: errors.New("http://127.0.0.1:7791 internal graphiti failure")})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_search"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}},
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_search",
		Input:    toolcontract.MarshalToolInput(map[string]string{"query": "Graphiti release notes"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected recoverable memory_search tool error, got %+v", result)
	}
	if result.FailureCode() != toolcontract.FailureCodes.Unavailable.String() || result.FailureStage() != "graphiti_search" {
		t.Fatalf("expected structured memory search failure, got %+v", result)
	}
	if strings.Contains(result.ContentText(), "web_search") {
		t.Fatalf("expected recovery guidance to stay out of raw tool output, got %q", result.ContentText())
	}
	if strings.Contains(result.ContentText(), "127.0.0.1") || strings.Contains(result.UserSafeFailureSummary(), "127.0.0.1") {
		t.Fatalf("expected internal Graphiti details to be hidden, got %+v", result)
	}
}

func TestMemorySearchDegradedWithPinnedFallback(t *testing.T) {
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(failingGraphMemoryStore{errorValue: errors.New("graphiti unavailable")})
	memoryService.StoreMemoryFact(memory.MemoryFact{
		FactID:      "recent-1",
		ScopeType:   memory.ScopeTypeUser,
		NamespaceID: memory.UserNamespace("person-1").NamespaceID,
		Content:     "The requester reviews release notes on Monday.",
		SourceKind:  memory.MemorySourceKindFact,
		ValidAt:     time.Date(2026, time.July, 18, 1, 0, 0, 0, time.UTC),
	})
	pinnedMemoryStore := memory.NewMarkdownStore(t.TempDir(), 1200)
	if _, errorValue := pinnedMemoryStore.MergePersonMemory(context.Background(), "person-1", "The requester prefers terse release notes."); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UsePinnedMemoryStore(pinnedMemoryStore)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_search"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_search",
		Input:    toolcontract.MarshalToolInput(map[string]string{"query": "release notes"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected degraded memory_search success, got %s", result.ContentText())
	}
	document := decodeMemorySearchToolOutput(t, result.ContentText())
	if document.SearchStatus != "degraded" {
		t.Fatalf("expected degraded search status, got %+v", document)
	}
	if len(document.Sources) != 2 || document.Sources[0] != "pinned_markdown" || document.Sources[1] != "recent_memory" {
		t.Fatalf("expected exact degraded sources, got %+v", document.Sources)
	}
	if !containsMemoryFact(document.Facts, "# Memory\n- The requester prefers terse release notes.") {
		t.Fatalf("expected pinned fallback fact, got %+v", document.Facts)
	}
	if strings.Contains(result.ContentText(), `"degraded":`) {
		t.Fatalf("expected searchStatus to be the only degraded signal, got %s", result.ContentText())
	}
}

func TestMemorySearchReturnsUnavailableWhenFallbackEmpty(t *testing.T) {
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(failingGraphMemoryStore{errorValue: errors.New("graphiti unavailable")})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UsePinnedMemoryStore(memory.NewMarkdownStore(t.TempDir(), 1200))
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_search"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_search",
		Input:    toolcontract.MarshalToolInput(map[string]string{"query": "missing"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected unavailable memory_search result, got %s", result.ContentText())
	}
	if result.FailureCode() != toolcontract.FailureCodes.Unavailable.String() {
		t.Fatalf("expected unavailable failure code, got %+v", result.Failure)
	}
}

func TestMemorySearchPinnedFallbackScopesRequesterNamespace(t *testing.T) {
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(failingGraphMemoryStore{errorValue: errors.New("graphiti unavailable")})
	pinnedMemoryStore := memory.NewMarkdownStore(t.TempDir(), 1200)
	if _, errorValue := pinnedMemoryStore.MergePersonMemory(context.Background(), "person-1", "Person one likes weekly summaries."); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := pinnedMemoryStore.MergePersonMemory(context.Background(), "person-2", "Person two has private launch plans."); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UsePinnedMemoryStore(pinnedMemoryStore)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_search"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
		MemoryNamespaces: []memory.MemoryNamespace{
			memory.UserNamespace("person-1"),
			memory.UserNamespace("person-2"),
		},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_search",
		Input:    toolcontract.MarshalToolInput(map[string]string{"query": "plans"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected scoped fallback success, got %s", result.ContentText())
	}
	if strings.Contains(result.ContentText(), "Person two") {
		t.Fatalf("expected person two memory to be excluded, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), "Person one") {
		t.Fatalf("expected person one memory, got %s", result.ContentText())
	}
}

func TestMemoryRememberToolPersistsMarkdownBeforeQueue(t *testing.T) {
	queue := &recordingMemoryUpdateQueue{}
	pinnedMemoryStore := memory.NewMarkdownStore(t.TempDir(), 1200)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryUpdateQueue(queue)
	toolCatalogBuilder.UsePinnedMemoryStore(pinnedMemoryStore)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_remember"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		MemoryNamespaces:  []memory.MemoryNamespace{memory.UserNamespace("person-1")},
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "memory_remember",
		Input:    toolcontract.MarshalToolInput(map[string]string{"content": "The user prefers markdown memory."}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected memory_remember success, got %s", result.ContentText())
	}
	document := decodeMemoryUpdateAccepted(t, result.ContentText())
	if document.Status != "persisted" || document.Durability != "durable" {
		t.Fatalf("expected persisted durable status, got %+v", document)
	}
	if len(queue.jobs) != 1 || !queue.jobs[0].SkipMarkdown {
		t.Fatalf("expected graphiti enrichment job without markdown rewrite, got %+v", queue.jobs)
	}
	memoryFacts, errorValue := pinnedMemoryStore.LoadPinnedMemory(context.Background(), "person-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(memoryFacts) != 1 || !strings.Contains(memoryFacts[0].Content, "markdown memory") {
		t.Fatalf("expected synchronous markdown memory, got %+v", memoryFacts)
	}
}

func decodeMemorySearchToolOutput(t *testing.T, content string) memorySearchToolOutput {
	t.Helper()
	document := memorySearchToolOutput{}
	if errorValue := json.Unmarshal([]byte(content), &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	return document
}

func containsMemoryFact(memoryFacts []memorySearchFact, content string) bool {
	for _, memoryFact := range memoryFacts {
		if memoryFact.Content == content {
			return true
		}
	}
	return false
}

func decodeMemoryUpdateAccepted(t *testing.T, content string) memory.MemoryUpdateAccepted {
	t.Helper()
	document := memory.MemoryUpdateAccepted{}
	if errorValue := json.Unmarshal([]byte(content), &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	return document
}
