package connectors

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type selectiveMemorySearchSpy struct {
	*fakeGraphMemoryStore
	searchCount int
}

func (spy *selectiveMemorySearchSpy) SearchFacts(ctx context.Context, request memory.MemorySearchRequest) ([]memory.MemoryFact, error) {
	spy.searchCount++
	return spy.fakeGraphMemoryStore.SearchFacts(ctx, request)
}

func TestConnectorLaunchDoesNotSearchMemoryForGreeting(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "안녕하세요"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	graphStore := &selectiveMemorySearchSpy{fakeGraphMemoryStore: &fakeGraphMemoryStore{facts: []memory.MemoryFact{{
		NamespaceID: memory.UserNamespace("person-1").NamespaceID,
		Content:     "사용자는 Graphiti 메모리 설계를 선택했다.",
	}}}}
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(graphStore)
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	connectorRuntime.UseTaskLauncher(connectorRuntime.routedTaskLauncherForTest(toolCatalogBuilder))

	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-greeting")); errorValue != nil {
		t.Fatalf("expected greeting to process: %v", errorValue)
	}
	if graphStore.searchCount != 0 {
		t.Fatalf("expected no launch memory search, got %d", graphStore.searchCount)
	}
	if structuredMessagesContain(languageModel.request.Messages, "Graphiti 메모리 설계") {
		t.Fatalf("expected stored fact to stay out of greeting context")
	}
}

func TestMemorySearchRetrievesOwnNamespaceAndDeniesOtherNamespace(t *testing.T) {
	graphStore := &selectiveMemorySearchSpy{fakeGraphMemoryStore: &fakeGraphMemoryStore{facts: []memory.MemoryFact{
		{FactID: "own-fact", NamespaceID: memory.UserNamespace("person-1").NamespaceID, Content: "own fact"},
		{FactID: "other-fact", NamespaceID: memory.UserNamespace("person-2").NamespaceID, Content: "other fact"},
	}}}
	memoryService := &memory.MemoryService{}
	memoryService.UseGraphStore(graphStore)
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_search"})
	toolSet := toolCatalogBuilder.BuildToolSet(agentRuntimeMemorySearchRequest("person-1", memory.UserNamespace("person-1")))

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "memory_search", Input: toolcontract.MarshalToolInput(map[string]string{"query": "fact"})})
	if errorValue != nil || result.Failed() || !strings.Contains(result.ContentText(), "own fact") || strings.Contains(result.ContentText(), "other fact") {
		t.Fatalf("expected own namespace retrieval only, error=%v result=%+v", errorValue, result)
	}
	otherPersonToolSet := toolCatalogBuilder.BuildToolSet(agentRuntimeMemorySearchRequest("person-1", memory.UserNamespace("person-2")))
	deniedResult, errorValue := otherPersonToolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "memory_search", Input: toolcontract.MarshalToolInput(map[string]string{"query": "fact"})})
	if errorValue != nil || deniedResult.Failed() || strings.Contains(deniedResult.ContentText(), "other fact") {
		t.Fatalf("expected unauthorized namespace to stay out of results, error=%v result=%+v", errorValue, deniedResult)
	}
	if graphStore.searchCount != 2 {
		t.Fatalf("expected each explicit search to resolve only authorized namespaces, got %d calls", graphStore.searchCount)
	}
}

func agentRuntimeMemorySearchRequest(personID string, namespace memory.MemoryNamespace) agentruntime.ToolCatalogRequest {
	return agentruntime.ToolCatalogRequest{ProfileName: "default", RequesterPersonID: personID, PersonAccess: policy.PersonAccess{PersonID: personID}, MemoryNamespaces: []memory.MemoryNamespace{namespace}}
}

var _ llm.LanguageModelProvider = (*recordingLanguageModel)(nil)
