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

func TestRouterRejectsCrossOriginMutatingRequest(t *testing.T) {
	router := newTestRouterForOriginChecks(t)

	postRequest := httptest.NewRequest(http.MethodPost, "/admin/api/quiesce", strings.NewReader(`{"enabled":true}`))
	postRequest.Header.Set("Origin", "http://evil.example")
	postResponse := httptest.NewRecorder()
	router.ServeHTTP(postResponse, postRequest)

	if postResponse.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden response, got %d: %s", postResponse.Code, postResponse.Body.String())
	}
}

func TestRouterAllowsSameOriginMutatingRequest(t *testing.T) {
	router := newTestRouterForOriginChecks(t)

	postRequest := httptest.NewRequest(http.MethodPost, "/admin/api/quiesce", strings.NewReader(`{"enabled":true}`))
	postRequest.Header.Set("Origin", "http://"+postRequest.Host)
	postResponse := httptest.NewRecorder()
	router.ServeHTTP(postResponse, postRequest)

	if postResponse.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", postResponse.Code, postResponse.Body.String())
	}
}

func TestRouterAllowsMutatingRequestWithoutOrigin(t *testing.T) {
	router := newTestRouterForOriginChecks(t)

	postRequest := httptest.NewRequest(http.MethodPost, "/admin/api/quiesce", strings.NewReader(`{"enabled":true}`))
	postResponse := httptest.NewRecorder()
	router.ServeHTTP(postResponse, postRequest)

	if postResponse.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", postResponse.Code, postResponse.Body.String())
	}
}

func newTestRouterForOriginChecks(t *testing.T) http.Handler {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	controller := runtimecontrol.NewTaskIntakeController(nil)
	return NewRouter(RouterDependencies{
		QuiesceHandler: adminapi.QuiesceHandler{
			Controller:     controller,
			TaskRunService: taskRunService,
		},
	})
}
