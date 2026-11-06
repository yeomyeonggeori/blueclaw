package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/adminapi"
	"github.com/yeomyeonggeori/blueclaw/internal/runtimecontrol"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestRouterHandlesQuiesceEndpoint(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "running task")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	controller := runtimecontrol.NewTaskIntakeController(nil)
	router := NewRouter(RouterDependencies{
		QuiesceHandler: adminapi.QuiesceHandler{
			Controller:     controller,
			TaskRunService: taskRunService,
		},
	})

	postRequest := httptest.NewRequest(http.MethodPost, "/admin/api/quiesce", strings.NewReader(`{"enabled":true}`))
	postResponse := httptest.NewRecorder()
	router.ServeHTTP(postResponse, postRequest)

	if postResponse.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", postResponse.Code, postResponse.Body.String())
	}
	if !strings.Contains(postResponse.Body.String(), `"quiesced":true`) {
		t.Fatalf("expected quiesced response, got %s", postResponse.Body.String())
	}
	if !strings.Contains(postResponse.Body.String(), `"activeTaskCount":1`) {
		t.Fatalf("expected active task count, got %s", postResponse.Body.String())
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/admin/api/quiesce", nil)
	getResponse := httptest.NewRecorder()
	router.ServeHTTP(getResponse, getRequest)

	if getResponse.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", getResponse.Code, getResponse.Body.String())
	}
	if !strings.Contains(getResponse.Body.String(), `"quiesced":true`) {
		t.Fatalf("expected retained quiesced state, got %s", getResponse.Body.String())
	}
}
