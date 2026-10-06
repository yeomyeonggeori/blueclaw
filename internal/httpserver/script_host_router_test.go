package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
)

func TestTheRouterHandsScriptHostRoutesToTheScriptHost(t *testing.T) {
	router := NewRouter(RouterDependencies{ScriptHostHandler: agentruntime.NewScriptHost().Handler()})

	request := httptest.NewRequest(http.MethodPost, agentruntime.ScriptHostPath+"/decide", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "no running command") {
		t.Fatalf("the script host did not answer its route: %d %s", response.Code, response.Body.String())
	}
}
