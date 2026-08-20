package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func testToolSet(t *testing.T, invokedAs *string) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"calendar_add", "file_read"})
	toolSet.AllowTestReplacement()
	register := func(name string, sideEffectClass string, approvalScope string) {
		errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
			ID:              "test:" + name,
			Name:            name,
			Description:     name + " description",
			Visibility:      toolcontract.ToolVisibilityModel,
			InputSchema:     json.RawMessage(`{"type":"object","properties":{"note":{"type":"string"}}}`),
			SideEffectClass: sideEffectClass,
			ApprovalScope:   approvalScope,
			ResultContract:  &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		}, func(ctx context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			*invokedAs = invocation.ToolName
			return toolcontract.ToolSuccessData("executed "+invocation.ToolName, json.RawMessage(`{}`)), nil
		})
		if errorValue != nil {
			t.Fatalf("expected %s to register: %v", name, errorValue)
		}
	}
	register("calendar_add", toolcontract.ToolSideEffectStateChange, "calendar")
	register("file_read", toolcontract.ToolSideEffectRead, "")
	return toolSet
}

func connectedCatalogSession(t *testing.T, requesterToolSet RequesterToolSet) *mcp.ClientSession {
	t.Helper()
	server, errorValue := NewToolCatalogServer(requesterToolSet, "test")
	if errorValue != nil {
		t.Fatalf("expected a tool catalog server: %v", errorValue)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() {
		_ = server.Run(context.Background(), serverTransport)
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-harness", Version: "test"}, nil)
	clientSession, errorValue := client.Connect(context.Background(), clientTransport, nil)
	if errorValue != nil {
		t.Fatalf("expected the harness to connect: %v", errorValue)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func TestToolCatalogServerRefusesAToolSetWithNoRequester(t *testing.T) {
	invokedTool := ""
	if _, errorValue := NewToolCatalogServer(RequesterToolSet{ToolSet: testToolSet(t, &invokedTool)}, "test"); errorValue == nil {
		t.Fatal("expected an unattributed tool set to be refused, because the POSIX actor comes from the requester")
	}
	if _, errorValue := NewToolCatalogServer(RequesterToolSet{RequesterPersonID: "person-1"}, "test"); errorValue == nil {
		t.Fatal("expected a missing tool set to be refused")
	}
}

func TestToolCatalogServerPublishesTheRequesterToolSetWithItsDescriptorAxes(t *testing.T) {
	invokedTool := ""
	clientSession := connectedCatalogSession(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: testToolSet(t, &invokedTool)})

	toolList, errorValue := clientSession.ListTools(context.Background(), nil)
	if errorValue != nil {
		t.Fatalf("expected the harness to list tools: %v", errorValue)
	}
	publishedTools := map[string]*mcp.Tool{}
	for _, tool := range toolList.Tools {
		publishedTools[tool.Name] = tool
	}
	if len(publishedTools) != 2 || publishedTools["calendar_add"] == nil || publishedTools["file_read"] == nil {
		t.Fatalf("expected the requester's catalog, got %+v", toolList.Tools)
	}
	if !publishedTools["file_read"].Annotations.ReadOnlyHint || publishedTools["calendar_add"].Annotations.ReadOnlyHint {
		t.Fatalf("expected the side effect class to reach the harness as a read-only hint, got %+v", publishedTools)
	}
	if publishedTools["calendar_add"].Meta["blueclaw/approvalScope"] != "calendar" {
		t.Fatalf("expected the approval scope to survive as metadata, got %+v", publishedTools["calendar_add"].Meta)
	}
}

func TestToolCatalogServerExecutesInsideTheDaemonAndReportsFailureAsAToolError(t *testing.T) {
	invokedTool := ""
	clientSession := connectedCatalogSession(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: testToolSet(t, &invokedTool)})

	callResult, errorValue := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "calendar_add",
		Arguments: map[string]any{"note": "내일 회의"},
	})
	if errorValue != nil {
		t.Fatalf("expected the tool call to reach the daemon: %v", errorValue)
	}
	if invokedTool != "calendar_add" {
		t.Fatalf("expected the daemon to run the tool, got %q", invokedTool)
	}
	if callResult.IsError {
		t.Fatalf("expected a successful call, got %+v", callResult)
	}
	textContent, isText := callResult.Content[0].(*mcp.TextContent)
	if !isText || !strings.Contains(textContent.Text, "executed calendar_add") {
		t.Fatalf("expected the tool output to reach the harness, got %+v", callResult.Content)
	}
}

func TestToolCatalogServerDoesNotPublishToolsTheRequesterMayNotUse(t *testing.T) {
	invokedTool := ""
	toolSet := testToolSet(t, &invokedTool)
	clientSession := connectedCatalogSession(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: toolSet.WithAllowedToolNames([]string{"file_read"})})

	toolList, errorValue := clientSession.ListTools(context.Background(), nil)
	if errorValue != nil {
		t.Fatalf("expected the harness to list tools: %v", errorValue)
	}
	for _, tool := range toolList.Tools {
		if tool.Name == "calendar_add" {
			t.Fatalf("expected a narrowed catalog to hide calendar_add, got %+v", toolList.Tools)
		}
	}
}

type taskRunRecordingGate struct {
	invocationTaskRunID string
}

func (gate *taskRunRecordingGate) ReviewToolCall(ctx context.Context, _ toolcontract.ToolInvocation, _ toolcontract.ToolDefinition) (toolcontract.ToolCallReview, error) {
	gate.invocationTaskRunID = toolcontract.TaskRunIDFromContext(ctx)
	return toolcontract.ToolCallReview{MayProceed: true}, nil
}

func TestToolCatalogServerTellsTheGateWhichTaskRunTheCallBelongsTo(t *testing.T) {
	invokedTool := ""
	toolSet := testToolSet(t, &invokedTool)
	gate := &taskRunRecordingGate{}
	toolSet.UseToolCallGate(gate)
	clientSession := connectedCatalogSession(t, RequesterToolSet{RequesterPersonID: "person-1", TaskRunID: "task-run-1", ToolSet: toolSet})

	if _, errorValue := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "calendar_add",
		Arguments: map[string]any{"note": "내일 회의"},
	}); errorValue != nil {
		t.Fatalf("expected the tool call to reach the daemon: %v", errorValue)
	}

	if gate.invocationTaskRunID != "task-run-1" {
		t.Fatalf("expected an out-of-process call to carry its task run the same way the bundled loop does, got %q", gate.invocationTaskRunID)
	}
}
