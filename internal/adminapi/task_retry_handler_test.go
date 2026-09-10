package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type retryTaskRunFunc func(context.Context, string) (task.TaskRun, error)

func (function retryTaskRunFunc) RetryTaskRun(contextValue context.Context, taskRunID string) (task.TaskRun, error) {
	return function(contextValue, taskRunID)
}

func TestTaskMonitorHandlerRetriesVisibleFailedTaskRun(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "retry this")
	if _, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "failed"); errorValue != nil {
		t.Fatal(errorValue)
	}
	var retriedTaskRunID string
	childTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "retry this")
	handler := TaskMonitorHandler{
		TaskRunService: taskRunService,
		RetryTaskRun: retryTaskRunFunc(func(_ context.Context, taskRunID string) (task.TaskRun, error) {
			retriedTaskRunID = taskRunID
			return childTaskRun, nil
		}),
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/run/retry", strings.NewReader(`{"taskRunID":"`+taskRun.TaskRunID+`","viewerIsAdmin":true}`))
	response := httptest.NewRecorder()
	handler.HandleRetryTaskRun(response, request)
	var responseBody map[string]string
	if json.NewDecoder(response.Body).Decode(&responseBody) != nil {
		t.Fatal("retry response was not JSON")
	}
	if response.Code != http.StatusAccepted || retriedTaskRunID != taskRun.TaskRunID || responseBody["taskRunID"] != childTaskRun.TaskRunID || responseBody["status"] != string(childTaskRun.Status) {
		t.Fatalf("retry answered %d and called %q", response.Code, retriedTaskRunID)
	}
}

func TestTaskMonitorHandlerRetryVisibilityIsScopedToViewer(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	aliceTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "alice retry")
	bobTaskRun := taskRunService.CreateTaskRun("person-2", "conversation-2", "bob retry")
	for _, taskRun := range []task.TaskRun{aliceTaskRun, bobTaskRun} {
		if _, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "failed"); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	invocations := 0
	handler := TaskMonitorHandler{
		TaskRunService:  taskRunService,
		IdentityService: newTestIdentityService(),
		RetryTaskRun: retryTaskRunFunc(func(_ context.Context, taskRunID string) (task.TaskRun, error) {
			invocations++
			retriedTaskRun, _ := taskRunService.FindTaskRun(taskRunID)
			return retriedTaskRun, nil
		}),
	}
	otherViewerResponse := httptest.NewRecorder()
	handler.HandleRetryTaskRun(otherViewerResponse, httptest.NewRequest(http.MethodPost, "/admin/api/run/retry", strings.NewReader(`{"taskRunID":"`+bobTaskRun.TaskRunID+`","viewerEmail":"alice@example.com"}`)))
	if otherViewerResponse.Code != http.StatusNotFound || invocations != 0 {
		t.Fatalf("cross viewer retry answered %d and invoked runtime %d times", otherViewerResponse.Code, invocations)
	}
	ownViewerResponse := httptest.NewRecorder()
	handler.HandleRetryTaskRun(ownViewerResponse, httptest.NewRequest(http.MethodPost, "/admin/api/run/retry", strings.NewReader(`{"taskRunID":"`+aliceTaskRun.TaskRunID+`","viewerEmail":"alice@example.com"}`)))
	if ownViewerResponse.Code != http.StatusAccepted || invocations != 1 {
		t.Fatalf("own viewer retry answered %d and invoked runtime %d times", ownViewerResponse.Code, invocations)
	}
}

func TestTaskMonitorHandlerRetryFailsClosedWhenViewerIdentityIsUnavailable(t *testing.T) {
	invocations := 0
	handler := TaskMonitorHandler{
		TaskRunService: task.NewTaskRunService(task.NewTaskEventService()),
		RetryTaskRun: retryTaskRunFunc(func(_ context.Context, _ string) (task.TaskRun, error) {
			invocations++
			return task.TaskRun{}, nil
		}),
	}
	taskRun := handler.TaskRunService.CreateTaskRun("person-1", "conversation-1", "retry")
	if _, errorValue := handler.TaskRunService.FailTaskRun(taskRun.TaskRunID, "failed"); errorValue != nil {
		t.Fatal(errorValue)
	}
	response := httptest.NewRecorder()
	handler.HandleRetryTaskRun(response, httptest.NewRequest(http.MethodPost, "/admin/api/run/retry", strings.NewReader(`{"taskRunID":"`+taskRun.TaskRunID+`","viewerEmail":"alice@example.com"}`)))
	if response.Code != http.StatusServiceUnavailable || invocations != 0 {
		t.Fatalf("unresolved viewer answered %d and invoked runtime %d times", response.Code, invocations)
	}
}

func TestTaskMonitorHandlerRetryMapsSourceDeletedRaceToNotFound(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "retry")
	if _, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "failed"); errorValue != nil {
		t.Fatal(errorValue)
	}
	handler := TaskMonitorHandler{
		TaskRunService: taskRunService,
		RetryTaskRun: retryTaskRunFunc(func(_ context.Context, _ string) (task.TaskRun, error) {
			return task.TaskRun{}, task.ErrTaskRunNotFound
		}),
	}
	response := httptest.NewRecorder()
	handler.HandleRetryTaskRun(response, httptest.NewRequest(http.MethodPost, "/admin/api/run/retry", strings.NewReader(`{"taskRunID":"`+taskRun.TaskRunID+`","viewerIsAdmin":true}`)))
	if response.Code != http.StatusNotFound {
		t.Fatalf("deleted source race answered %d, want 404", response.Code)
	}
}

func TestTaskMonitorHandlerRejectsMissingOrNonFailedTaskRun(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	runningTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "running")
	handler := TaskMonitorHandler{TaskRunService: taskRunService, RetryTaskRun: retryTaskRunFunc(func(_ context.Context, _ string) (task.TaskRun, error) { return task.TaskRun{}, nil })}
	for _, body := range []string{`{}`, `{"taskRunID":"` + runningTaskRun.TaskRunID + `","viewerIsAdmin":true}`} {
		response := httptest.NewRecorder()
		handler.HandleRetryTaskRun(response, httptest.NewRequest(http.MethodPost, "/admin/api/run/retry", strings.NewReader(body)))
		wantedStatus := http.StatusBadRequest
		if body != `{}` {
			wantedStatus = http.StatusConflict
		}
		if response.Code != wantedStatus {
			t.Fatalf("body %s answered %d, want %d", body, response.Code, wantedStatus)
		}
	}
}

func TestTaskMonitorHandlerMapsRetryRuntimeErrors(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		errorValue error
		status     int
	}{
		{name: "conflict", errorValue: connectors.ErrTaskRetryConflict, status: http.StatusConflict},
		{name: "unavailable", errorValue: connectors.ErrTaskRetryUnavailable, status: http.StatusServiceUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			taskRunService := task.NewTaskRunService(task.NewTaskEventService())
			taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "retry this")
			if _, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "failed"); errorValue != nil {
				t.Fatal(errorValue)
			}
			handler := TaskMonitorHandler{TaskRunService: taskRunService, RetryTaskRun: retryTaskRunFunc(func(_ context.Context, _ string) (task.TaskRun, error) { return task.TaskRun{}, testCase.errorValue })}
			request := httptest.NewRequest(http.MethodPost, "/admin/api/run/retry", strings.NewReader(`{"taskRunID":"`+taskRun.TaskRunID+`","viewerIsAdmin":true}`))
			response := httptest.NewRecorder()
			handler.HandleRetryTaskRun(response, request)
			if response.Code != testCase.status {
				t.Fatalf("retry answered %d, want %d", response.Code, testCase.status)
			}
		})
	}
}
