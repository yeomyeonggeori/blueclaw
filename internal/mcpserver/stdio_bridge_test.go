package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/buildflavor"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func publishedCatalogForBridge(t *testing.T) (string, string) {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"note_write"})
	toolSet.AllowTestReplacement()
	if errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID:              "test:note_write",
		Name:            "note_write",
		Description:     "write a note",
		Visibility:      toolcontract.ToolVisibilityModel,
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
		SideEffectClass: toolcontract.ToolSideEffectStateChange,
		ResultContract:  &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("note written", json.RawMessage(`{}`)), nil
	}); errorValue != nil {
		t.Fatalf("expected the tool to register: %v", errorValue)
	}

	resolver := NewSessionTokenRequesterResolver(func() string { return "session-token-1" })
	handlerServer := httptest.NewServer(NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(handlerServer.Close)
	sessionToken, errorValue := resolver.GrantSessionToken(RequesterToolSet{RequesterPersonID: "person-1", ToolSet: toolSet})
	if errorValue != nil {
		t.Fatalf("expected the catalog to be published: %v", errorValue)
	}
	return handlerServer.URL, sessionToken
}

func buildBridgeCommand(t *testing.T) string {
	t.Helper()
	commandPath := filepath.Join(t.TempDir(), "blueclaw")
	build := exec.Command("go", append(append([]string{"build"}, buildflavor.GoFlags()...), "-o", commandPath, "../../cmd/blueclaw")...)
	if output, errorValue := build.CombinedOutput(); errorValue != nil {
		t.Fatalf("building the daemon binary: %v\n%s", errorValue, output)
	}
	return commandPath
}

func TestAnAgentReachesTheCatalogThroughTheStdioBridge(t *testing.T) {
	endpointURL, sessionToken := publishedCatalogForBridge(t)
	bridgeCommand := exec.Command(buildBridgeCommand(t), StdioBridgeCommand)
	bridgeCommand.Env = append(os.Environ(),
		CatalogEndpointEnvironmentName+"="+endpointURL,
		CatalogTokenEnvironmentName+"="+sessionToken,
	)

	agentSession, errorValue := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "test"}, nil).
		Connect(t.Context(), &mcp.CommandTransport{Command: bridgeCommand}, nil)
	if errorValue != nil {
		t.Fatalf("an agent that only takes stdio has to reach the catalog: %v", errorValue)
	}
	defer agentSession.Close()

	toolList, errorValue := agentSession.ListTools(t.Context(), nil)
	if errorValue != nil {
		t.Fatalf("listing the bridged catalog: %v", errorValue)
	}
	if len(toolList.Tools) != 1 || toolList.Tools[0].Name != "note_write" {
		t.Fatalf("expected the requester's catalog through the bridge, got %+v", toolList.Tools)
	}

	callResult, errorValue := agentSession.CallTool(t.Context(), &mcp.CallToolParams{Name: "note_write", Arguments: map[string]any{"text": "회의록"}})
	if errorValue != nil {
		t.Fatalf("calling a bridged tool: %v", errorValue)
	}
	if callResult.IsError {
		t.Fatalf("expected the call to execute inside the daemon, got %+v", callResult)
	}
}
