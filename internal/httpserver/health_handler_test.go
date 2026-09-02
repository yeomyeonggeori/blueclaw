package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/protocolidentity"
)

func TestHealthReportsProtocolIdentityDrift(t *testing.T) {
	protocolIdentityStatus := &protocolidentity.Result{
		Passed:    false,
		CheckedAt: time.Now().UTC(),
		FailureReasons: []string{
			"capabilityd: expected protocolVersion \"0.4.0\"",
		},
	}
	handler := HealthHandler{ProtocolIdentity: protocolIdentityStatus}
	responseRecorder := httptest.NewRecorder()
	handler.HandleHealth(responseRecorder, httptest.NewRequest(http.MethodGet, "/admin/api/health", nil))

	if responseRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected unhealthy status, got %d", responseRecorder.Code)
	}
	var responseDocument struct {
		ProtocolIdentity protocolidentity.Result `json:"protocolIdentity"`
		FailureReasons   []string                `json:"failureReasons"`
	}
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &responseDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if responseDocument.ProtocolIdentity.Passed {
		t.Fatalf("expected protocol identity failure, got %+v", responseDocument.ProtocolIdentity)
	}
	if !strings.Contains(strings.Join(responseDocument.FailureReasons, ","), "protocol identity is not valid") {
		t.Fatalf("expected protocol identity failure reason, got %+v", responseDocument.FailureReasons)
	}
}

func TestHealthStaysOKWhenGraphMemoryIsNotConfigured(t *testing.T) {
	handler := HealthHandler{MemoryService: &memory.MemoryService{}}
	responseRecorder := httptest.NewRecorder()
	handler.HandleHealth(responseRecorder, httptest.NewRequest(http.MethodGet, "/admin/api/health", nil))

	var responseDocument struct {
		Memory         memory.MemoryHealth `json:"memory"`
		FailureReasons []string            `json:"failureReasons"`
	}
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &responseDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if responseDocument.Memory.Configured {
		t.Fatalf("expected memory to report as not configured, got %+v", responseDocument.Memory)
	}
	if strings.Contains(strings.Join(responseDocument.FailureReasons, ","), "graphiti memory") {
		t.Fatalf("expected an unconfigured memory not to fail health, got %+v", responseDocument.FailureReasons)
	}
}

func TestHealthRefreshesProtocolIdentityAfterStartup(t *testing.T) {
	protocolVersion := "0.4.0"
	aggregateProtocolHash := "58ff1977989bacbf2db3fdce08fd57c9b52f344ca747a3322f4e60bdf6052a78"
	isDrifting := false
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		if isDrifting {
			_, _ = responseWriter.Write([]byte(`{"status":"ok","protocolVersion":"0.4.1","aggregateProtocolHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
			return
		}
		_, _ = responseWriter.Write([]byte(`{"status":"ok","protocolVersion":"` + protocolVersion + `","aggregateProtocolHash":"` + aggregateProtocolHash + `"}`))
	}))
	defer server.Close()

	identityChecker := protocolidentity.NewChecker(protocolidentity.Configuration{
		CapabilityEndpoint: server.URL,
		HTTPClient:         server.Client(),
	})
	handler := HealthHandler{
		ProtocolIdentityChecker:  &identityChecker,
		ProtocolIdentityExpected: protocolidentity.Identity{ProtocolVersion: protocolVersion, AggregateProtocolHash: aggregateProtocolHash},
	}

	firstResponse := httptest.NewRecorder()
	handler.HandleHealth(firstResponse, httptest.NewRequest(http.MethodGet, "/admin/api/health", nil))
	if !healthProtocolIdentityResult(t, firstResponse).Passed {
		t.Fatal("expected initial health protocol identity to pass")
	}

	isDrifting = true
	secondResponse := httptest.NewRecorder()
	handler.HandleHealth(secondResponse, httptest.NewRequest(http.MethodGet, "/admin/api/health", nil))
	result := healthProtocolIdentityResult(t, secondResponse)
	if result.Passed || result.Capabilityd.Status != "drift" {
		t.Fatalf("expected health to report later protocol drift, got %+v", result)
	}
}

func healthProtocolIdentityResult(t *testing.T, responseRecorder *httptest.ResponseRecorder) protocolidentity.Result {
	t.Helper()
	var responseDocument struct {
		ProtocolIdentity protocolidentity.Result `json:"protocolIdentity"`
	}
	if errorValue := json.Unmarshal(responseRecorder.Body.Bytes(), &responseDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	return responseDocument.ProtocolIdentity
}
