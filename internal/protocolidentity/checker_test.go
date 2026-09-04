package protocolidentity

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testProtocolVersion = "0.4.0"
const testAggregateProtocolHash = "58ff1977989bacbf2db3fdce08fd57c9b52f344ca747a3322f4e60bdf6052a78"

func TestCheckerRequiresExactIdentityFromCapabilityd(t *testing.T) {
	identity := Identity{ProtocolVersion: testProtocolVersion, AggregateProtocolHash: testAggregateProtocolHash}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/v1/capabilities" {
			_, _ = responseWriter.Write([]byte(`{"protocolVersion":"0.4.0","aggregateProtocolHash":"58ff1977989bacbf2db3fdce08fd57c9b52f344ca747a3322f4e60bdf6052a78"}`))
			return
		}
		_, _ = responseWriter.Write([]byte(`{"status":"ok","protocolVersion":"0.4.0","aggregateProtocolHash":"58ff1977989bacbf2db3fdce08fd57c9b52f344ca747a3322f4e60bdf6052a78"}`))
	}))
	defer server.Close()

	result := NewChecker(Configuration{CapabilityEndpoint: server.URL}).Check(context.Background(), identity)

	if !result.Passed || !result.Capabilityd.Passed {
		t.Fatalf("expected protocol identity check to pass, got %+v", result)
	}
	if result.Capabilityd.Status != "ok" {
		t.Fatalf("expected endpoint status to be ok, got %+v", result)
	}
}

func TestCheckerReportsDriftWithoutDescriptorHashing(t *testing.T) {
	identity := Identity{ProtocolVersion: testProtocolVersion, AggregateProtocolHash: testAggregateProtocolHash}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"status":"ok","protocolVersion":"0.4.1","aggregateProtocolHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","deviceCapabilities":[{"name":"different"}]}`))
	}))
	defer server.Close()

	result := NewChecker(Configuration{CapabilityEndpoint: server.URL}).Check(context.Background(), identity)

	if result.Passed || result.Capabilityd.Status != "drift" {
		t.Fatalf("expected the endpoint identity to drift, got %+v", result)
	}
	if !strings.Contains(result.Capabilityd.Error, "protocolVersion") {
		t.Fatalf("expected structured drift detail, got %+v", result.Capabilityd)
	}
}

func TestCheckerReportsUnavailableEndpoint(t *testing.T) {
	identity := Identity{ProtocolVersion: testProtocolVersion, AggregateProtocolHash: testAggregateProtocolHash}
	configuration := Configuration{CapabilityEndpoint: "http://127.0.0.1:1"}
	result := NewChecker(configuration).Check(context.Background(), identity)

	if result.Passed || result.Capabilityd.Status != "unavailable" {
		t.Fatalf("expected unavailable endpoint status, got %+v", result)
	}
}

func TestCheckerSkipsProcessesThisDeploymentDoesNotRun(t *testing.T) {
	identity := Identity{ProtocolVersion: testProtocolVersion, AggregateProtocolHash: testAggregateProtocolHash}
	result := NewChecker(Configuration{}).Check(context.Background(), identity)

	if !result.Passed {
		t.Fatalf("expected a standalone deployment to pass with nothing configured, got %+v", result)
	}
	if result.Capabilityd.Status != "not_configured" {
		t.Fatalf("expected an unconfigured endpoint to be reported as such, got %+v", result)
	}
}

func TestCheckerUsesConfiguredCapabilityUnixTransport(t *testing.T) {
	identity := Identity{ProtocolVersion: testProtocolVersion, AggregateProtocolHash: testAggregateProtocolHash}
	socketDirectoryPath, errorValue := os.MkdirTemp("/tmp", "bc-")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectoryPath) })
	socketPath := filepath.Join(socketDirectoryPath, "capability.sock")
	listener, errorValue := net.Listen("unix", socketPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	capabilityServer := &http.Server{Handler: http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/capabilities" {
			responseWriter.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = responseWriter.Write([]byte(`{"protocolVersion":"0.4.0","aggregateProtocolHash":"58ff1977989bacbf2db3fdce08fd57c9b52f344ca747a3322f4e60bdf6052a78"}`))
	})}
	go capabilityServer.Serve(listener)
	defer capabilityServer.Close()

	capabilityHTTPClient := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}}

	result := NewChecker(Configuration{
		CapabilityEndpoint:   "http://internkim-capability",
		CapabilityHTTPClient: capabilityHTTPClient,
	}).Check(context.Background(), identity)

	if !result.Passed || !result.Capabilityd.Passed {
		t.Fatalf("expected Unix capability transport to pass, got %+v", result)
	}
}

func TestValidateIdentityRejectsMissingAndInvalidValues(t *testing.T) {
	testCases := []Identity{
		{},
		{ProtocolVersion: testProtocolVersion},
		{ProtocolVersion: testProtocolVersion, AggregateProtocolHash: strings.Repeat("A", 64)},
		{ProtocolVersion: testProtocolVersion, AggregateProtocolHash: "short"},
		{ProtocolVersion: " " + testProtocolVersion, AggregateProtocolHash: testAggregateProtocolHash},
		{ProtocolVersion: testProtocolVersion, AggregateProtocolHash: testAggregateProtocolHash + " "},
	}
	for _, identity := range testCases {
		if errorValue := ValidateIdentity(identity); errorValue == nil {
			t.Fatalf("expected identity to be rejected: %+v", identity)
		}
	}
}

func TestCheckPassesWithoutAskingWhenNothingIsPinned(t *testing.T) {
	checker := NewChecker(Configuration{
		CapabilityEndpoint:   "http://internkim-capability",
		CapabilityHTTPClient: refusingHTTPClient{},
	})

	result := checker.Check(context.Background(), Identity{})

	if !result.Passed {
		t.Fatalf("expected an unpinned identity to pass, got %+v", result)
	}
	if result.Capabilityd.Status != "not_pinned" {
		t.Fatalf("expected the endpoint to be reported unpinned, got %+v", result.Capabilityd)
	}
}

type refusingHTTPClient struct{}

func (refusingHTTPClient) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("the checker asked capabilityd although nothing was pinned")
}
