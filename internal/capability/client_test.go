package capability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func slowCapabilityServer(t *testing.T, answersAfter time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		select {
		case <-time.After(answersAfter):
			responseWriter.Header().Set("Content-Type", "application/json")
			_, _ = responseWriter.Write([]byte(`{}`))
		case <-request.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestTheClientCarriesNoBlanketTimeout(t *testing.T) {
	_, httpClient := Dial(Configuration{Endpoint: "http://capability.local", Timeout: 30 * time.Second})

	if httpClient.Timeout != 0 {
		t.Fatalf("the HTTP client bounds every call at %s, so a tool that declares longer is cut off there", httpClient.Timeout)
	}
}

func TestACallOutlivesTheDefaultWhenItsOwnLimitIsLonger(t *testing.T) {
	server := slowCapabilityServer(t, 100*time.Millisecond)
	client := NewClient(Configuration{Endpoint: server.URL, Timeout: 20 * time.Millisecond})

	if errorValue := client.PostJSONWithin(context.Background(), 2*time.Second, "/v1/tools/desktop_read/invoke", map[string]any{}, nil); errorValue != nil {
		t.Fatalf("a call given 2s was cut off by the 20ms default: %v", errorValue)
	}
}

func TestACallIsAbandonedAtItsOwnLimit(t *testing.T) {
	server := slowCapabilityServer(t, time.Second)
	client := NewClient(Configuration{Endpoint: server.URL, Timeout: 10 * time.Second})
	startedAt := time.Now()

	errorValue := client.PostJSONWithin(context.Background(), 50*time.Millisecond, "/v1/tools/desktop_read/invoke", map[string]any{}, nil)

	if !errors.Is(errorValue, context.DeadlineExceeded) {
		t.Fatalf("expected the call's own deadline, got %v", errorValue)
	}
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("the call was abandoned after %s, not at its 50ms limit", elapsed)
	}
}

func TestAnOrdinaryCallIsStillBoundedByTheDefault(t *testing.T) {
	server := slowCapabilityServer(t, time.Second)
	client := NewClient(Configuration{Endpoint: server.URL, Timeout: 50 * time.Millisecond})

	errorValue := client.PostJSON(context.Background(), "/v1/llm/chat", map[string]any{}, nil)

	if !errors.Is(errorValue, context.DeadlineExceeded) {
		t.Fatalf("expected the configured default to bound the call, got %v", errorValue)
	}
}
