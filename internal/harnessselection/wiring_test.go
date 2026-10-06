package harnessselection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type bearerHeader struct{ bearerToken string }

func (header bearerHeader) RoundTrip(request *http.Request) (*http.Response, error) {
	request.Header.Set("Authorization", "Bearer "+header.bearerToken)
	return http.DefaultTransport.RoundTrip(request)
}

func TestSelectedExternalHarnessPublishesTheRequesterCatalogAtTheRoutedEndpoint(t *testing.T) {
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token" })
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(catalogServer.Close)

	selectedFactory, errorValue := Select(
		config.HarnessConfiguration{Name: ExternalHarnessName, AgentCommandPath: "/usr/bin/true"},
		ToolCatalogEndpoint{URL: catalogServer.URL, Resolver: resolver},
		SandboxProcessBoundary{Runner: refusingProcessRunner{}, WorkspaceRootPath: "/workspace"},
	)
	if errorValue != nil {
		t.Fatalf("expected the configured external harness: %v", errorValue)
	}
	if harness, _ := selectedFactory(harnessdriver.Dependencies{}); harness == nil {
		t.Fatal("expected a harness")
	}

	publisher := sessionTokenPublisher{endpointURL: catalogServer.URL, resolver: resolver}
	endpointURL, sessionToken, revoke, errorValue := publisher.PublishToolCatalog(mcpserver.RequesterToolSet{
		RequesterPersonID: "person-1",
		ToolSet:           singleToolSet(t),
	})
	if errorValue != nil {
		t.Fatalf("expected a published catalog: %v", errorValue)
	}
	clientSession, errorValue := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "test"}, nil).Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   endpointURL,
		HTTPClient: &http.Client{Transport: bearerHeader{bearerToken: sessionToken}},
	}, nil)
	if errorValue != nil {
		t.Fatalf("expected an agent to reach the routed catalog: %v", errorValue)
	}
	toolList, errorValue := clientSession.ListTools(context.Background(), nil)
	if errorValue != nil {
		t.Fatalf("expected the catalog to list: %v", errorValue)
	}
	if len(toolList.Tools) != 1 || toolList.Tools[0].Name != "note_write" {
		t.Fatalf("expected the requester's catalog at the endpoint, got %+v", toolList.Tools)
	}
	_ = clientSession.Close()
	revoke()
}

func singleToolSet(t *testing.T) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"note_write"})
	toolSet.AllowTestReplacement()
	errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID:             "test:note_write",
		Name:           "note_write",
		Description:    "write a note",
		Visibility:     toolcontract.ToolVisibilityModel,
		InputSchema:    json.RawMessage(`{"type":"object","properties":{}}`),
		ResultContract: &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("written", json.RawMessage(`{}`)), nil
	})
	if errorValue != nil {
		t.Fatalf("expected the tool to register: %v", errorValue)
	}
	return toolSet
}

var _ agentcontract.Harness = (agentcontract.Harness)(nil)
