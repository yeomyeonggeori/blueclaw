package adminapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestTaskMonitorHandlerFiltersAndLimitsTaskRunList(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	completedTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "summarize report")
	if _, errorValue := taskRunService.CompleteTaskRun(completedTaskRun.TaskRunID, "done"); errorValue != nil {
		t.Fatal(errorValue)
	}
	firstFailedTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "first failing task")
	if _, errorValue := taskRunService.FailTaskRun(firstFailedTaskRun.TaskRunID, "tool denied"); errorValue != nil {
		t.Fatal(errorValue)
	}
	secondFailedTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "second failing task")
	if _, errorValue := taskRunService.FailTaskRun(secondFailedTaskRun.TaskRunID, "network unreachable"); errorValue != nil {
		t.Fatal(errorValue)
	}
	handler := TaskMonitorHandler{TaskRunService: taskRunService}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?status=failed&limit=1", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	responseBody := responseRecorder.Body.String()
	if strings.Contains(responseBody, completedTaskRun.TaskRunID) {
		t.Fatalf("expected completed run to be filtered out, got %s", responseBody)
	}
	if strings.Count(responseBody, `"taskRunID"`) != 1 {
		t.Fatalf("expected a single task run, got %s", responseBody)
	}
}

func TestTaskMonitorHandlerListsAllTaskRunsWithoutQuery(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunService.CreateTaskRun("person-1", "conversation-1", "first task")
	taskRunService.CreateTaskRun("person-1", "conversation-1", "second task")
	handler := TaskMonitorHandler{TaskRunService: taskRunService}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/run", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if strings.Count(responseRecorder.Body.String(), `"taskRunID"`) != 2 {
		t.Fatalf("expected both task runs, got %s", responseRecorder.Body.String())
	}
}

func TestTaskMonitorHandlerOffsetSkipsNewestTaskRuns(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunService.CreateTaskRun("person-1", "conversation-1", "oldest task")
	taskRunService.CreateTaskRun("person-1", "conversation-1", "middle task")
	newestTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "newest task")
	handler := TaskMonitorHandler{TaskRunService: taskRunService}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?offset=1", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	responseBody := responseRecorder.Body.String()
	if strings.Contains(responseBody, newestTaskRun.TaskRunID) {
		t.Fatalf("expected newest task run to be skipped by offset, got %s", responseBody)
	}
	if strings.Count(responseBody, `"taskRunID"`) != 2 {
		t.Fatalf("expected two task runs after offset, got %s", responseBody)
	}
}

func TestTaskMonitorHandlerIncludeTotalReturnsCountAfterFilter(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunService.CreateTaskRun("person-1", "conversation-1", "first task")
	taskRunService.CreateTaskRun("person-1", "conversation-1", "second task")
	taskRunService.CreateTaskRun("person-1", "conversation-1", "third task")
	handler := TaskMonitorHandler{TaskRunService: taskRunService}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?limit=1&includeTotal=true", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	var response struct {
		TaskRuns   []map[string]any `json:"taskRuns"`
		TotalCount int              `json:"totalCount"`
	}
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &response); errorValue != nil {
		t.Fatalf("expected object response, got %s", responseRecorder.Body.String())
	}
	if response.TotalCount != 3 {
		t.Fatalf("expected total count 3, got %d", response.TotalCount)
	}
	if len(response.TaskRuns) != 1 {
		t.Fatalf("expected a single page item, got %d", len(response.TaskRuns))
	}
}

func TestTaskMonitorHandlerIncludeTotalReturnsCostSummaries(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	firstTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "first task")
	secondTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "second task")
	taskRunService.AppendTaskEvent(firstTaskRun.TaskRunID, "llm.call", `{"costUSD":0.0155,"totalTokens":100}`)
	taskRunService.AppendTaskEvent(secondTaskRun.TaskRunID, "llm.call", `{"upstreamInferenceCostUSD":0.0045,"totalTokens":50}`)
	handler := TaskMonitorHandler{TaskRunService: taskRunService, TaskEventService: taskEventService}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?includeTotal=true&includeCost=true", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	var response struct {
		TaskRuns []struct {
			TaskRunID    string  `json:"taskRunID"`
			LLMCostUSD   float64 `json:"llmCostUSD"`
			LLMCallCount int     `json:"llmCallCount"`
		} `json:"taskRuns"`
		DailyCostSummaries []struct {
			Date         string  `json:"date"`
			CostUSD      float64 `json:"costUSD"`
			TaskRunCount int     `json:"taskRunCount"`
			LLMCallCount int     `json:"llmCallCount"`
		} `json:"dailyCostSummaries"`
	}
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &response); errorValue != nil {
		t.Fatalf("expected object response, got %s", responseRecorder.Body.String())
	}
	if len(response.TaskRuns) != 2 {
		t.Fatalf("expected two task runs, got %d", len(response.TaskRuns))
	}
	if response.TaskRuns[0].LLMCallCount != 1 || response.TaskRuns[1].LLMCallCount != 1 {
		t.Fatalf("expected row call counts, got %+v", response.TaskRuns)
	}
	if len(response.DailyCostSummaries) != 1 {
		t.Fatalf("expected one daily cost summary, got %+v", response.DailyCostSummaries)
	}
	dailySummary := response.DailyCostSummaries[0]
	if dailySummary.Date == "" {
		t.Fatalf("expected date in daily summary, got %+v", dailySummary)
	}
	if dailySummary.TaskRunCount != 2 || dailySummary.LLMCallCount != 2 {
		t.Fatalf("expected task and call counts in daily summary, got %+v", dailySummary)
	}
	if dailySummary.CostUSD < 0.019999 || dailySummary.CostUSD > 0.020001 {
		t.Fatalf("expected daily cost 0.02, got %.6f", dailySummary.CostUSD)
	}
}

func TestTaskMonitorHandlerLimitsDailyCostSummaryWindow(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	firstTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "first task")
	secondTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "second task")
	thirdTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "third task")
	taskRunService.AppendTaskEvent(firstTaskRun.TaskRunID, "llm.call", `{"costUSD":0.01}`)
	taskRunService.AppendTaskEvent(secondTaskRun.TaskRunID, "llm.call", `{"costUSD":0.02}`)
	taskRunService.AppendTaskEvent(thirdTaskRun.TaskRunID, "llm.call", `{"costUSD":0.03}`)
	handler := TaskMonitorHandler{TaskRunService: taskRunService, TaskEventService: taskEventService}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?limit=1&offset=1&includeTotal=true&includeCost=true&dailyCostTaskRunLimit=1", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	var response struct {
		TaskRuns []struct {
			TaskRunID    string  `json:"taskRunID"`
			LLMCostUSD   float64 `json:"llmCostUSD"`
			LLMCallCount int     `json:"llmCallCount"`
		} `json:"taskRuns"`
		DailyCostSummaries []struct {
			TaskRunCount int `json:"taskRunCount"`
		} `json:"dailyCostSummaries"`
		DailyCostScope struct {
			TaskRunLimit      int  `json:"taskRunLimit"`
			TaskRunCount      int  `json:"taskRunCount"`
			TotalTaskRunCount int  `json:"totalTaskRunCount"`
			IsTruncated       bool `json:"isTruncated"`
		} `json:"dailyCostScope"`
	}
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &response); errorValue != nil {
		t.Fatalf("expected object response, got %s", responseRecorder.Body.String())
	}
	if len(response.TaskRuns) != 1 {
		t.Fatalf("expected one paged task run, got %+v", response.TaskRuns)
	}
	if response.TaskRuns[0].LLMCallCount != 1 || response.TaskRuns[0].LLMCostUSD <= 0 {
		t.Fatalf("expected paged row cost summary, got %+v", response.TaskRuns[0])
	}
	if len(response.DailyCostSummaries) != 1 || response.DailyCostSummaries[0].TaskRunCount != 1 {
		t.Fatalf("expected daily summary to use one task run, got %+v", response.DailyCostSummaries)
	}
	if response.DailyCostScope.TaskRunLimit != 1 || response.DailyCostScope.TaskRunCount != 1 || response.DailyCostScope.TotalTaskRunCount != 3 || !response.DailyCostScope.IsTruncated {
		t.Fatalf("expected truncated daily cost scope, got %+v", response.DailyCostScope)
	}
}

func TestTaskMonitorHandlerWithoutIncludeTotalReturnsBareArray(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunService.CreateTaskRun("person-1", "conversation-1", "only task")
	handler := TaskMonitorHandler{TaskRunService: taskRunService}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/run", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	var taskRuns []map[string]any
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &taskRuns); errorValue != nil {
		t.Fatalf("expected bare array response, got %s", responseRecorder.Body.String())
	}
	if len(taskRuns) != 1 {
		t.Fatalf("expected one task run, got %d", len(taskRuns))
	}
}

func newTestIdentityService() *identity.IdentityService {
	return identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{
			"alice@example.com": "person-1",
			"bob@example.com":   "person-2",
		},
		DisplayNameByPersonID: map[string]string{
			"person-1": "Alice",
			"person-2": "Bob",
		},
	})
}

func TestTaskMonitorHandlerScopesNonAdminViewerToOwnTaskRuns(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	aliceTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "alice task")
	bobTaskRun := taskRunService.CreateTaskRun("person-2", "conversation-2", "bob task")
	handler := TaskMonitorHandler{TaskRunService: taskRunService, IdentityService: newTestIdentityService()}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?viewerEmail=alice@example.com", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	responseBody := responseRecorder.Body.String()
	if !strings.Contains(responseBody, aliceTaskRun.TaskRunID) {
		t.Fatalf("expected alice task run, got %s", responseBody)
	}
	if strings.Contains(responseBody, bobTaskRun.TaskRunID) {
		t.Fatalf("expected bob task run to be scoped out, got %s", responseBody)
	}
}

func TestTaskMonitorHandlerAdminViewerSeesAllWithDisplayName(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunService.CreateTaskRun("person-1", "conversation-1", "alice task")
	taskRunService.CreateTaskRun("person-2", "conversation-2", "bob task")
	handler := TaskMonitorHandler{TaskRunService: taskRunService, IdentityService: newTestIdentityService()}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?viewerEmail=alice@example.com&viewerIsAdmin=true", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	responseBody := responseRecorder.Body.String()
	if strings.Count(responseBody, `"taskRunID"`) != 2 {
		t.Fatalf("expected both task runs for admin viewer, got %s", responseBody)
	}
	if !strings.Contains(responseBody, `"requesterDisplayName":"Alice"`) {
		t.Fatalf("expected requester display name, got %s", responseBody)
	}
}

func TestTaskMonitorHandlerUnknownViewerSeesNoTaskRuns(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunService.CreateTaskRun("person-1", "conversation-1", "alice task")
	handler := TaskMonitorHandler{TaskRunService: taskRunService, IdentityService: newTestIdentityService()}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?viewerEmail=stranger@example.com", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	if strings.Contains(responseRecorder.Body.String(), `"taskRunID"`) {
		t.Fatalf("expected no task runs for unknown viewer, got %s", responseRecorder.Body.String())
	}
}

func TestTaskMonitorHandlerDeletesScopedTerminalTaskRun(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	aliceTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "alice task")
	if _, errorValue := taskRunService.CompleteTaskRun(aliceTaskRun.TaskRunID, "done"); errorValue != nil {
		t.Fatal(errorValue)
	}
	bobTaskRun := taskRunService.CreateTaskRun("person-2", "conversation-2", "bob task")
	if _, errorValue := taskRunService.CompleteTaskRun(bobTaskRun.TaskRunID, "done"); errorValue != nil {
		t.Fatal(errorValue)
	}
	handler := TaskMonitorHandler{TaskRunService: taskRunService, IdentityService: newTestIdentityService()}
	body := `{"taskRunID":"` + bobTaskRun.TaskRunID + `","viewerEmail":"alice@example.com"}`
	request := httptest.NewRequest(http.MethodPost, "/admin/api/run/delete", strings.NewReader(body))
	responseRecorder := httptest.NewRecorder()

	handler.HandleDeleteTaskRun(responseRecorder, request)

	if responseRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected scoped delete to return 404, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if _, isFound := taskRunService.FindTaskRun(bobTaskRun.TaskRunID); !isFound {
		t.Fatal("bob task run should remain")
	}

	body = `{"taskRunID":"` + aliceTaskRun.TaskRunID + `","viewerEmail":"alice@example.com"}`
	request = httptest.NewRequest(http.MethodPost, "/admin/api/run/delete", strings.NewReader(body))
	responseRecorder = httptest.NewRecorder()

	handler.HandleDeleteTaskRun(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected delete to succeed, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if _, isFound := taskRunService.FindTaskRun(aliceTaskRun.TaskRunID); isFound {
		t.Fatal("alice task run should be deleted")
	}
}

func TestTaskMonitorHandlerRejectsRunningTaskRunDelete(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "running task")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	handler := TaskMonitorHandler{TaskRunService: taskRunService, IdentityService: newTestIdentityService()}
	body := `{"taskRunID":"` + taskRun.TaskRunID + `","viewerEmail":"alice@example.com"}`
	request := httptest.NewRequest(http.MethodPost, "/admin/api/run/delete", strings.NewReader(body))
	responseRecorder := httptest.NewRecorder()

	handler.HandleDeleteTaskRun(responseRecorder, request)

	if responseRecorder.Code != http.StatusConflict {
		t.Fatalf("expected conflict, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if _, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID); !isFound {
		t.Fatal("running task run should remain")
	}
}

func parkedTaskRunAwaitingApproval(t *testing.T, taskRunService *task.TaskRunService, prompt string) task.TaskRun {
	t.Helper()
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", prompt)
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "ask.requested", `{"kind":"ask_confirm","message":"삭제할까요?"}`)
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, task.TaskStatusWaitingApproval, "삭제할까요?"); errorValue != nil {
		t.Fatalf("expected the run to park: %v", errorValue)
	}
	return taskRun
}

func TestARunWaitingWithAQuestionNobodySentIsReportedAsUndelivered(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	askedTaskRun := parkedTaskRunAwaitingApproval(t, taskRunService, "asked task")
	taskRunService.AppendTaskEvent(askedTaskRun.TaskRunID, "connector.reply.sent", `{"replyKind":"user_notice","dispatchID":"dispatch-1"}`)
	unaskedTaskRun := parkedTaskRunAwaitingApproval(t, taskRunService, "unasked task")
	handler := TaskMonitorHandler{TaskRunService: taskRunService, TaskEventService: taskEventService}
	request := httptest.NewRequest(http.MethodGet, "/admin/api/task?status=waiting_approval", nil)
	responseRecorder := httptest.NewRecorder()

	handler.HandleListTaskRun(responseRecorder, request)

	listedTaskRuns := []struct {
		TaskRunID              string `json:"taskRunID"`
		HasUndeliveredQuestion bool   `json:"hasUndeliveredQuestion"`
	}{}
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &listedTaskRuns); errorValue != nil {
		t.Fatalf("expected a task run list: %v (%s)", errorValue, responseRecorder.Body.String())
	}
	undeliveredByTaskRunID := map[string]bool{}
	for _, listedTaskRun := range listedTaskRuns {
		undeliveredByTaskRunID[listedTaskRun.TaskRunID] = listedTaskRun.HasUndeliveredQuestion
	}
	if !undeliveredByTaskRunID[unaskedTaskRun.TaskRunID] {
		t.Fatalf("expected a run whose question never went out to say so, got %s", responseRecorder.Body.String())
	}
	if undeliveredByTaskRunID[askedTaskRun.TaskRunID] {
		t.Fatalf("expected a run whose question was delivered not to be flagged, got %s", responseRecorder.Body.String())
	}
}
