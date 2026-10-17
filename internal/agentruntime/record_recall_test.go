//go:build !nobundledharness

package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluememo/bluememotest"
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

const (
	documentFrame   = `Data room document ""`
	leaseDocument   = `Data room document "본사 사무실 임대차계약": 월세 800만원`
	rentQuestion    = "사무실 월세 얼마 내?"
	leaseChangedAt  = "2026-10-01T00:00:00Z"
	listedLease     = `{"result":{"frame":"Data room document \"\"","facts":[{"id":"lease","content":"Data room document \"본사 사무실 임대차계약\": 월세 800만원","changedAt":"2026-10-01T00:00:00Z"}]}}`
	recordRequester = "sample@example.test"
)

func recallingThroughAnIndex(t *testing.T, standIn *recordCatalogStandIn) (*ToolCatalogBuilder, ToolCatalogRequest) {
	t.Helper()
	stores := memorytest.OpenEmbeddingWith(t, bluememotest.TableEmbedder{Vectors: map[string][]float32{
		documentFrame: bluememotest.Axes(map[int]float32{0: 1}),
		leaseDocument: bluememotest.Axes(map[int]float32{0: 1, 1: 1}),
		rentQuestion:  bluememotest.Axes(map[int]float32{0: 1, 1: 1}),
	}})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []capability.ToolDescriptor{aRecallSource("company_document_recall")})
	toolCatalogBuilder.UseMemoryStores(stores, nil)
	return toolCatalogBuilder, ToolCatalogRequest{
		RecordCatalog:  standIn,
		RequesterEmail: recordRequester,
		PersonAccess:   policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}},
	}
}

func waitForRecordIndexRefresh(t *testing.T, toolCatalogBuilder *ToolCatalogBuilder) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !toolCatalogBuilder.recordIndexRefreshes.isAnyRunning() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected the record index refresh to finish")
}

func (refreshes *recordIndexRefreshes) isAnyRunning() bool {
	refreshes.mutex.Lock()
	defer refreshes.mutex.Unlock()
	for _, refresh := range refreshes.byRequester {
		if refresh.isRunning {
			return true
		}
	}
	return false
}

func (refreshes *recordIndexRefreshes) makeDue() {
	refreshes.mutex.Lock()
	defer refreshes.mutex.Unlock()
	for _, refresh := range refreshes.byRequester {
		refresh.startedAt = time.Time{}
	}
}

func TestRecallAsksTheSourceForWhatItsIndexRanksNear(t *testing.T) {
	standIn := &recordCatalogStandIn{
		answered: mcp.ToolResult{StructuredContent: json.RawMessage(recalledDocument)},
		listed:   mcp.ToolResult{StructuredContent: json.RawMessage(listedLease)},
	}
	toolCatalogBuilder, request := recallingThroughAnIndex(t, standIn)
	ctx := context.Background()

	toolCatalogBuilder.recallFromTheRecord(ctx, request, rentQuestion, recallReachAtLaunch)
	waitForRecordIndexRefresh(t, toolCatalogBuilder)
	recall := toolCatalogBuilder.recallFromTheRecord(ctx, request, rentQuestion, recallReachAtLaunch)

	var asked recallSourceInput
	if errorValue := json.Unmarshal(standIn.calledWith, &asked); errorValue != nil || len(asked.Candidates) != 1 || asked.Candidates[0] != "lease" || asked.Query != rentQuestion {
		t.Fatalf("expected the lease passed as a candidate beside the query, got %s", standIn.calledWith)
	}
	if len(recall.Failures) != 0 || standIn.calledFor != recordRequester {
		t.Fatalf("expected a clean recall as the requester, got %v for %q", recall.Failures, standIn.calledFor)
	}
}

func TestRecordIndexListsOnlyWhatChangedSinceItsLastListing(t *testing.T) {
	standIn := &recordCatalogStandIn{
		answered: mcp.ToolResult{StructuredContent: json.RawMessage(recalledDocument)},
		listed:   mcp.ToolResult{StructuredContent: json.RawMessage(listedLease)},
	}
	toolCatalogBuilder, request := recallingThroughAnIndex(t, standIn)
	ctx := context.Background()

	toolCatalogBuilder.recallFromTheRecord(ctx, request, rentQuestion, recallReachAtLaunch)
	waitForRecordIndexRefresh(t, toolCatalogBuilder)
	toolCatalogBuilder.recallFromTheRecord(ctx, request, rentQuestion, recallReachAtLaunch)
	toolCatalogBuilder.recordIndexRefreshes.makeDue()
	toolCatalogBuilder.recallFromTheRecord(ctx, request, rentQuestion, recallReachAtLaunch)
	waitForRecordIndexRefresh(t, toolCatalogBuilder)

	listings := standIn.listings()
	refreshedFrom := listings[len(listings)-1]
	if len(listings) != 3 || listings[0].ChangedAfter != recordIndexEarliestChange || refreshedFrom.ChangedAfter != leaseChangedAt || refreshedFrom.ChangedAfterID != "lease" {
		t.Fatalf("expected the first refresh to list from the start and the next from the lease on, got %+v", listings)
	}
}

func TestRecallSaysSoWhenTheIndexCouldNotList(t *testing.T) {
	standIn := &recordCatalogStandIn{
		answered: mcp.ToolResult{StructuredContent: json.RawMessage(recalledDocument)},
		listed:   mcp.ToolResult{IsError: true, StructuredContent: json.RawMessage(`{"error":"the plane is down"}`)},
	}
	toolCatalogBuilder, request := recallingThroughAnIndex(t, standIn)
	ctx := context.Background()

	toolCatalogBuilder.recallFromTheRecord(ctx, request, rentQuestion, recallReachAtLaunch)
	waitForRecordIndexRefresh(t, toolCatalogBuilder)
	recall := toolCatalogBuilder.recallFromTheRecord(ctx, request, rentQuestion, recallReachAtLaunch)

	if len(recall.Failures) != 1 || !strings.Contains(recall.Failures[0], "company_document_recall index") || !strings.Contains(recall.Failures[0], "the plane is down") {
		t.Fatalf("expected the failed listing reported on the next recall, got %v", recall.Failures)
	}
	if len(recall.Facts) != 1 {
		t.Fatalf("expected the source still answered, got %+v", recall.Facts)
	}
}
