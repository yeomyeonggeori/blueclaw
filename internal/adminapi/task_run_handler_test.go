package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
)

func TestTaskRunHandlerLaunchesAdminTask(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	harness := harnesstest.New(taskRunService)
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "admin done"}
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"admin": {"memory_search"},
	}, nil)
	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{"admin@example.com": "person-1"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1"},
		},
	})
	handler := TaskRunHandler{
		TaskLauncher:    agentruntime.NewTaskLauncher(harness, taskRunService, toolCatalogBuilder),
		IdentityService: identityService,
		WorkspaceID:     "workspace-1",
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/run/start", strings.NewReader(`{"requesterPersonID":"person-1","prompt":"run admin task","profileName":"admin"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunTask(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), "admin done") {
		t.Fatalf("expected final reply, got %s", responseRecorder.Body.String())
	}
}

func TestTaskRunHandlerLaunchIgnoresClientCancellation(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	harness := contextObservingHarness{Harness: harnesstest.New(taskRunService)}
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "admin done"}
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"admin": {"memory_search"},
	}, nil)
	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{"admin@example.com": "person-1"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1"},
		},
	})
	handler := TaskRunHandler{
		TaskLauncher:    agentruntime.NewTaskLauncher(harness, taskRunService, toolCatalogBuilder),
		IdentityService: identityService,
		WorkspaceID:     "workspace-1",
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/run/start", strings.NewReader(`{"requesterPersonID":"person-1","prompt":"run admin task","profileName":"admin"}`))
	requestContext, cancelRequest := context.WithCancel(request.Context())
	cancelRequest()
	request = request.WithContext(requestContext)
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunTask(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), "admin done") {
		t.Fatalf("expected final reply, got %s", responseRecorder.Body.String())
	}
}

func TestTaskRunHandlerHandsTheModelPathPresetToTheAgentAsAFact(t *testing.T) {
	handler, taskRunService := newStubbedPresetTaskRunHandler(true)
	harness := harnesstest.New(taskRunService)
	handler.TaskLauncher = agentruntime.NewTaskLauncher(harness, taskRunService, agentruntime.NewToolCatalogBuilder())
	request := httptest.NewRequest(http.MethodPost, "/admin/api/run/start", strings.NewReader(`{"requesterPersonID":"person-1","prompt":"reply exactly","taskDecisionPreset":"model_path"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunTask(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	turnRequest := harness.LastTurnRequest()
	if turnRequest.TaskLevel != agentcontract.TaskLevelXLow {
		t.Fatalf("expected the xlow diagnostic task level as a fact, got %q", turnRequest.TaskLevel)
	}
}

func TestTaskRunHandlerRejectsTaskDecisionPresetOverrides(t *testing.T) {
	overrides := []string{
		`"profileName":"default"`,
		`"pinnedToolNames":["bash"]`,
		`"pinnedSkillNames":["mail"]`,
	}
	for _, override := range overrides {
		t.Run(override, func(t *testing.T) {
			handler, taskRunService := newStubbedPresetTaskRunHandler(true)
			body := `{"requesterPersonID":"person-1","prompt":"reply exactly","taskDecisionPreset":"model_path",` + override + `}`
			request := httptest.NewRequest(http.MethodPost, "/admin/api/run/start", strings.NewReader(body))
			responseRecorder := httptest.NewRecorder()

			handler.HandleRunTask(responseRecorder, request)

			if responseRecorder.Code != http.StatusBadRequest {
				t.Fatalf("expected bad request, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
			}
			if len(taskRunService.ListTaskRun()) != 0 {
				t.Fatalf("expected no task runs, got %+v", taskRunService.ListTaskRun())
			}
		})
	}
}

func TestTaskRunHandlerRejectsDisabledTaskDecisionPresetBeforeLaunch(t *testing.T) {
	handler, taskRunService := newStubbedPresetTaskRunHandler(false)
	request := httptest.NewRequest(http.MethodPost, "/admin/api/run/start", strings.NewReader(`{"requesterPersonID":"person-1","prompt":"reply exactly","taskDecisionPreset":"model_path"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunTask(responseRecorder, request)

	if responseRecorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if len(taskRunService.ListTaskRun()) != 0 {
		t.Fatalf("expected no task runs, got %+v", taskRunService.ListTaskRun())
	}
}

func TestTaskRunHandlerRejectsUnsupportedTaskDecisionPreset(t *testing.T) {
	handler, taskRunService := newStubbedPresetTaskRunHandler(true)
	request := httptest.NewRequest(http.MethodPost, "/admin/api/run/start", strings.NewReader(`{"requesterPersonID":"person-1","prompt":"reply exactly","taskDecisionPreset":"unsafe"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleRunTask(responseRecorder, request)

	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if len(taskRunService.ListTaskRun()) != 0 {
		t.Fatalf("expected no task runs, got %+v", taskRunService.ListTaskRun())
	}
}

func TestTaskRunHandlerCancelsActiveTaskRun(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "schedule:schedule-1", "stale schedule")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	handler := TaskRunHandler{TaskRunService: taskRunService}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/run/cancel", strings.NewReader(`{"taskRunIDs":["`+taskRun.TaskRunID+`"],"reason":"cleanup"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleCancelTaskRun(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	cancelledTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected cancelled task run, got found=%v run=%+v", isFound, cancelledTaskRun)
	}
	if !strings.Contains(responseRecorder.Body.String(), `"cancelledTaskRunCount":1`) {
		t.Fatalf("expected cancel count in response, got %s", responseRecorder.Body.String())
	}
}

func TestTaskRunHandlerStopsRequesterTasksOnly(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	requesterTaskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")
	otherTaskRun := taskRunService.CreateTaskRun("person-2", "direct-2", "other task")
	for _, taskRun := range []task.TaskRun{requesterTaskRun, otherTaskRun} {
		if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	handler := TaskRunHandler{TaskRunService: taskRunService}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/run/cancel", strings.NewReader(`{"mode":"stop_all","requesterPersonID":"person-1","reason":"slash stop-all"}`))
	responseRecorder := httptest.NewRecorder()

	handler.HandleCancelTaskRun(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	cancelledTaskRun, _ := taskRunService.FindTaskRun(requesterTaskRun.TaskRunID)
	unchangedTaskRun, _ := taskRunService.FindTaskRun(otherTaskRun.TaskRunID)
	if cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("requester task status = %s, want cancelled", cancelledTaskRun.Status)
	}
	if unchangedTaskRun.Status != task.TaskStatusRunning {
		t.Fatalf("other task status = %s, want running", unchangedTaskRun.Status)
	}
	if !strings.Contains(responseRecorder.Body.String(), `"scheduleTouched":false`) {
		t.Fatalf("expected scheduleTouched false in response, got %s", responseRecorder.Body.String())
	}
}

func newStubbedPresetTaskRunHandler(isPresetAllowed bool) (TaskRunHandler, *task.TaskRunService) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	return presetTaskRunHandler(harnesstest.New(taskRunService), taskRunService, isPresetAllowed), taskRunService
}

func presetTaskRunHandler(harness agentcontract.Harness, taskRunService *task.TaskRunService, isPresetAllowed bool) TaskRunHandler {
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		modelPathDiagnosticProfileName: {"model_path.diagnostic.no_tools"},
	}, nil)
	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1"},
		},
	})
	return TaskRunHandler{
		TaskLauncher:            agentruntime.NewTaskLauncher(harness, taskRunService, toolCatalogBuilder),
		IdentityService:         identityService,
		WorkspaceID:             "workspace-1",
		TaskRunService:          taskRunService,
		AllowTaskDecisionPreset: isPresetAllowed,
	}
}

// The launch must survive a client that walked away, so this double fails the
// turn exactly when the request context it was handed is already cancelled.
type contextObservingHarness struct {
	*harnesstest.Harness
}

func (harness contextObservingHarness) RunTurn(ctx context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if errorValue := ctx.Err(); errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	return harness.Harness.RunTurn(ctx, request)
}

func taskEventsContainBody(taskEvents []task.TaskEvent, name string, bodyFragment string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name && strings.Contains(taskEvent.Body, bodyFragment) {
			return true
		}
	}
	return false
}
