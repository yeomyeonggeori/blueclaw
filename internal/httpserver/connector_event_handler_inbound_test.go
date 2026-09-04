package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectorEventsAreRefusedWhenTheAcpSessionAdmitsInboundInstead(t *testing.T) {
	handler := NewConnectorEventHandler(nil, false).HandleConnectorEvent()
	responseRecorder := httptest.NewRecorder()

	handler(responseRecorder, httptest.NewRequest(http.MethodPost, "/connectors/buzz/events", strings.NewReader(`{}`)))

	if responseRecorder.Code != http.StatusConflict {
		t.Fatalf("the route answered %d, expected 409; a chatd still posting here would run the turn twice", responseRecorder.Code)
	}
	if !strings.Contains(responseRecorder.Body.String(), "-inbound acp") {
		t.Fatalf("the refusal does not name the flag that caused it: %q", responseRecorder.Body.String())
	}
}
