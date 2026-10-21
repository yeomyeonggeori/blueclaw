package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func aServiceTokenAt(t *testing.T) string {
	t.Helper()
	tokenPath := filepath.Join(t.TempDir(), "service-token")
	if errorValue := os.WriteFile(tokenPath, []byte(strings.Repeat("a", 64)+"\n"), 0o440); errorValue != nil {
		t.Fatal(errorValue)
	}
	return tokenPath
}

func TestTheAdminAPIAndTheConnectorDoorAnswerOnlyTheHostServices(t *testing.T) {
	reached := []string{}
	handler := withServiceToken(aServiceTokenAt(t), http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		reached = append(reached, request.URL.Path)
	}))
	for _, path := range []string{
		"/admin/api/run/approve",
		"/admin/api/policy/save",
		"/admin/api/run/start",
		"/admin/api/held-call/redeem",
		"/admin/api/workspace/file",
		"/connectors/buzz/events",
	} {
		for _, authorization := range []string{"", "Bearer ", "Bearer " + strings.Repeat("b", 64), strings.Repeat("a", 64)} {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			request.Header.Set("Authorization", authorization)
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("%s with %q answered %d, want 401", path, authorization, recorder.Code)
			}
		}
	}
	if len(reached) != 0 {
		t.Fatalf("a call without the token reached %v", reached)
	}
}

func TestAHostServiceWithTheTokenAndAHealthCheckWithoutOneGetThrough(t *testing.T) {
	reached := []string{}
	handler := withServiceToken(aServiceTokenAt(t), http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		reached = append(reached, request.URL.Path)
	}))
	authorized := httptest.NewRequest(http.MethodPost, "/admin/api/run/approve", strings.NewReader(`{}`))
	authorized.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))

	handler.ServeHTTP(httptest.NewRecorder(), authorized)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/admin/api/health", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/harness/script-host/tools/message_send", nil))

	if strings.Join(reached, " ") != "/admin/api/run/approve /admin/api/health /harness/script-host/tools/message_send" {
		t.Fatalf("reached %v", reached)
	}
}

func TestATokenThatCannotBeReadLetsNoCallerThrough(t *testing.T) {
	handler := withServiceToken(filepath.Join(t.TempDir(), "missing"), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("a call reached the admin API with no token to check it against")
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/api/run/approve", nil)
	request.Header.Set("Authorization", "Bearer ")

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("answered %d, want 401", recorder.Code)
	}
}
