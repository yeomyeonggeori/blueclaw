//go:build !nobundledharness

package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const recalledDocument = `{"result":{"facts":[{"id":"document-1","content":"Data room document ABC Trading service agreement: paid monthly on the last day","validAt":"2026-03-02"}]}}`

func aRecallSource(name string) capability.ToolDescriptor {
	descriptor := aDescriptor(name, capability.AnsweredByRecord)
	descriptor.ModelVisibility = toolcontract.ToolVisibilityInternal
	descriptor.SideEffectClass = toolcontract.ToolSideEffectRead
	descriptor.InputIntentSchema = nil
	descriptor.ResultContract = nil
	descriptor.RecallSource = true
	return descriptor
}

func launchRecallingFrom(t *testing.T, standIn *recordCatalogStandIn, catalog ...capability.ToolDescriptor) (TaskLaunchResult, *task.TaskRunService) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	stores := memorytest.Open(t)
	memorytest.Remember(t, stores, memory.PersonScope("person-1"), "이샘플 handles the ABC Trading account")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, catalog)
	toolCatalogBuilder.UseMemoryStores(stores, nil)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{"default": {"memory_search"}}, nil)

	launchResult, errorValue := NewTaskLauncher(harnesstest.New(taskRunService), taskRunService, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		RequesterName:     "이샘플",
		RequesterEmail:    "sample@example.test",
		RecordCatalog:     standIn,
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Platform:          "buzz",
		Prompt:            "What are the payment terms of the ABC Trading account?",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to succeed: %v", errorValue)
	}
	return launchResult, taskRunService
}

func eventBodiesByName(taskRunService *task.TaskRunService, taskRunID string) map[string]string {
	bodiesByName := map[string]string{}
	for _, event := range taskRunService.ListTaskEvent(taskRunID) {
		bodiesByName[event.Name] = event.Body
	}
	return bodiesByName
}

func TestLaunchRecallsWhatTheRecordHoldsBesideMemoryAsTheRequester(t *testing.T) {
	standIn := &recordCatalogStandIn{answered: mcp.ToolResult{StructuredContent: json.RawMessage(recalledDocument)}}

	launchResult, taskRunService := launchRecallingFrom(t, standIn, aRecallSource("company_document_recall"), aDescriptor("task_add", capability.AnsweredByRecord))

	if standIn.calledTool != "company_document_recall" || standIn.calledFor != "sample@example.test" {
		t.Fatalf("expected the recall source asked as the requester, got %q for %q", standIn.calledTool, standIn.calledFor)
	}
	var asked recallSourceInput
	if errorValue := json.Unmarshal(standIn.calledWith, &asked); errorValue != nil || asked.Query != "What are the payment terms of the ABC Trading account?" || asked.Limit != recordRecallLimit {
		t.Fatalf("expected the prompt asked with the recall limit, got %s", standIn.calledWith)
	}
	if !containsMemoryFactContent(launchResult.MemoryFacts, "handles the ABC Trading account") {
		t.Fatalf("expected memory still recalled, got %+v", launchResult.MemoryFacts)
	}
	document := launchResult.MemoryFacts[len(launchResult.MemoryFacts)-1]
	if document.FactID != "document-1" || document.SourceKind != recordRecallSourceKind || document.ValidAt.Format("2006-01-02") != "2026-03-02" {
		t.Fatalf("expected the document recalled after memory with its day, got %+v", document)
	}
	var recallBody map[string]any
	bodies := eventBodiesByName(taskRunService, launchResult.TurnResult.TaskRun.TaskRunID)
	if errorValue := json.Unmarshal([]byte(bodies["memory.recall_injected"]), &recallBody); errorValue != nil || recallBody["recordCount"] != float64(1) {
		t.Fatalf("expected the recall event to count the record, got %v", bodies)
	}
}

func TestLaunchSaysSoWhenTheRecordRefusesToRecall(t *testing.T) {
	standIn := &recordCatalogStandIn{answered: mcp.ToolResult{IsError: true, StructuredContent: json.RawMessage(`{"error":"the plane is down"}`)}}

	launchResult, taskRunService := launchRecallingFrom(t, standIn, aRecallSource("company_document_recall"))

	if !containsMemoryFactContent(launchResult.MemoryFacts, "handles the ABC Trading account") {
		t.Fatalf("expected memory recalled when the record refuses, got %+v", launchResult.MemoryFacts)
	}
	failure := eventBodiesByName(taskRunService, launchResult.TurnResult.TaskRun.TaskRunID)["memory.record_recall_failed"]
	if !strings.Contains(failure, "company_document_recall") || !strings.Contains(failure, "the plane is down") {
		t.Fatalf("expected the refusal recorded with the source and its reason, got %q", failure)
	}
}

func TestMemorySearchAnswersRecordsThatMemoryForgetRefuses(t *testing.T) {
	standIn := &recordCatalogStandIn{answered: mcp.ToolResult{StructuredContent: json.RawMessage(recalledDocument)}}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []capability.ToolDescriptor{aRecallSource("company_document_recall")})
	toolCatalogBuilder.UseMemoryStores(memorytest.Open(t), nil)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"memory_search", "memory_forget"})
	fixture := storeToolFixture{toolSet: toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-alice",
		RequesterEmail:    "sample@example.test",
		RecordCatalog:     standIn,
		PersonAccess:      policy.PersonAccess{PersonID: "person-alice"},
	})}

	var output memorySearchToolOutput
	decodeToolResult(t, fixture.invoke(t, "memory_search", map[string]string{"query": "ABC Trading payment terms"}), &output)
	if len(output.Records) != 1 || output.Records[0].ID != "document-1" || output.Records[0].ValidAt != "2026-03-02" || output.SearchStatus != memorySearchComplete {
		t.Fatalf("expected the document answered as a record, got %+v", output)
	}
	forgotten := fixture.invoke(t, "memory_forget", map[string]any{"factIDs": []string{"document-1"}, "reason": "outdated"})
	if !forgotten.Failed() {
		t.Fatalf("expected memory_forget to refuse a record, got %s", forgotten.ContentText())
	}
}
