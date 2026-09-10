package postgres

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/adminapi"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

type retryIntegrationHarness struct {
	*harnesstest.Harness
	requests chan agentcontract.AgentTurnRequest
}

func (harness retryIntegrationHarness) RunTurn(ctx context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	harness.requests <- request
	return harness.Harness.RunTurn(ctx, request)
}

type retryIntegrationAdapter struct {
	connectors.PlatformAdapter
	deliveries chan connectors.OutboundReply
}

func (adapter retryIntegrationAdapter) Name() string { return "retry-fixture" }

func (adapter retryIntegrationAdapter) FetchHistory(context.Context, string, int) (connectors.VisibleContext, error) {
	return connectors.VisibleContext{}, nil
}

func (adapter retryIntegrationAdapter) SendReply(_ context.Context, _ connectors.ReplyTarget, reply connectors.OutboundReply) (string, error) {
	adapter.deliveries <- reply
	return "retry-fixture-delivery", nil
}

func TestTaskRetryAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, closeDatabase := morningBriefingIntegrationDatabase(t, context.Background())
	defer closeDatabase()
	if _, errorValue := database.SQL.Exec("INSERT INTO person (person_id, display_name, security_level_name, security_level_rank, created_at, updated_at) VALUES ('retry-person', 'Sample', 'member', 0, now(), now())"); errorValue != nil {
		t.Fatal(errorValue)
	}
	events := task.NewTaskEventService()
	events.UseRepository(NewTaskEventRepository(database))
	runs := task.NewTaskRunService(events)
	runs.UseRepository(NewTaskRunRepository(database))
	identities := identity.NewIdentityService(policy.PolicyProjection{PersonIDByEmail: map[string]string{"retry@example.com": "retry-person", "other@example.com": "other-person"}})
	harness := retryIntegrationHarness{Harness: harnesstest.New(runs), requests: make(chan agentcontract.AgentTurnRequest, 4)}
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "recovered"}
	adapter := retryIntegrationAdapter{deliveries: make(chan connectors.OutboundReply, 4)}
	runtime := connectors.NewConnectorRuntime(identities, harness, runs, events, nil)
	runtime.UseWorkspaceRootPath(t.TempDir())
	runtime.UseReplyGenerator(harness.Harness)
	runtime.UseTurnRouter(harness.Harness)
	runtime.UseLaunchFailureCompleter(harness.Harness)
	runtime.UseEventRepository(NewRawEventRepository(database))
	runtime.RegisterAdapter(adapter)
	source := createRetryIntegrationSource(t, runs)
	handler := adminapi.TaskMonitorHandler{TaskRunService: runs, IdentityService: identities, RetryTaskRun: runtime}
	child := requestIntegrationRetry(t, handler, source.TaskRunID, "retry@example.com", http.StatusAccepted)
	duplicate := requestIntegrationRetry(t, handler, source.TaskRunID, "retry@example.com", http.StatusAccepted)
	if child.TaskRunID == source.TaskRunID || duplicate.TaskRunID != child.TaskRunID {
		t.Fatalf("retry did not reserve one new child: source=%s child=%s duplicate=%s", source.TaskRunID, child.TaskRunID, duplicate.TaskRunID)
	}
	requestIntegrationRetry(t, handler, source.TaskRunID, "other@example.com", http.StatusNotFound)
	runtime.Start(ctx)
	select {
	case request := <-harness.requests:
		if request.PriorTask.TaskRunID != source.TaskRunID || len(request.PriorTask.RecordedAttempts) != 1 || len(request.PriorTask.RecordedAttempts[0].Effects) != 1 {
			t.Fatalf("retry lost recorded source effects: %+v", request.PriorTask)
		}
	case <-ctx.Done():
		t.Fatal("persistent retry queue did not launch the child")
	}
	select {
	case delivery := <-adapter.deliveries:
		if delivery.TaskRunID != child.TaskRunID || delivery.Message == "" {
			t.Fatalf("retry delivered the wrong reply: %+v", delivery)
		}
	case <-ctx.Done():
		t.Fatal("retry outbox did not deliver the child's completion")
	}
	assertRetryIntegrationState(t, database, runs, source, child.TaskRunID)
}

func createRetryIntegrationSource(t *testing.T, runs *task.TaskRunService) task.TaskRun {
	t.Helper()
	source := runs.CreateTaskRunWithOrigin("retry-person", task.TaskRunOrigin{ConversationID: "retry-conversation", ReplyTargetID: "retry-message", IsThread: true}, "Recover the recorded task")
	runs.AppendTaskEvent(source.TaskRunID, agentcontract.TaskEventAgentTaskLaunched, `{"platform":"retry-fixture","profileName":"default","conversationID":"retry-conversation","replyTargetID":"retry-message","isThread":true}`)
	runs.AppendTaskEvent(source.TaskRunID, "tool.record_create.result", `{"observationID":"obs-001","toolInput":{"title":"sample task"},"effects":[{"objectType":"task","effect":"created","id":"fixture-effect"}]}`)
	failed, errorValue := runs.FailTaskRun(source.TaskRunID, "fixture failure after recorded effect")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return failed
}

func requestIntegrationRetry(t *testing.T, handler adminapi.TaskMonitorHandler, taskRunID string, email string, expectedStatus int) task.TaskRun {
	t.Helper()
	body, errorValue := json.Marshal(map[string]string{"taskRunID": taskRunID, "viewerEmail": email})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	response := httptest.NewRecorder()
	handler.HandleRetryTaskRun(response, httptest.NewRequest(http.MethodPost, "/admin/api/run/retry", strings.NewReader(string(body))))
	if response.Code != expectedStatus {
		t.Fatalf("retry answered %d: %s", response.Code, response.Body.String())
	}
	var child task.TaskRun
	if expectedStatus == http.StatusAccepted {
		if errorValue := json.Unmarshal(response.Body.Bytes(), &child); errorValue != nil || child.TaskRunID == "" {
			t.Fatalf("retry did not return the child: %s", response.Body.String())
		}
	}
	return child
}

func assertRetryIntegrationState(t *testing.T, database Database, runs *task.TaskRunService, original task.TaskRun, childID string) {
	t.Helper()
	source, _ := runs.FindTaskRun(original.TaskRunID)
	child, _ := runs.FindTaskRun(childID)
	if source.Status != task.TaskStatusFailed || source.Result != original.Result || source.FailureReason != original.FailureReason || child.Status != task.TaskStatusCompleted {
		t.Fatalf("retry settled incorrectly: source=%+v child=%+v", source, child)
	}
	var queuedCount, launchedCount int
	if errorValue := database.SQL.QueryRow("SELECT count(*) FROM raw_event WHERE platform = 'retry-fixture'").Scan(&queuedCount); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := database.SQL.QueryRow("SELECT count(*) FROM task_event WHERE task_run_id = $1 AND name = 'agent.task_launched'", childID).Scan(&launchedCount); errorValue != nil {
		t.Fatal(errorValue)
	}
	if queuedCount != 1 || launchedCount != 1 {
		t.Fatalf("retry duplicated work: queued=%d launched=%d", queuedCount, launchedCount)
	}
	t.Logf("source=%s preserved; child=%s completed; queue=%d launch=%d", source.TaskRunID, childID, queuedCount, launchedCount)
}
