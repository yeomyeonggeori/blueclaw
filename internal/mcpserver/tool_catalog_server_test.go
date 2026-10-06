package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func testToolSet(t *testing.T, invokedAs *string) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"event_add", "file_read"})
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
	register("event_add", toolcontract.ToolSideEffectStateChange, "calendar")
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
	if len(publishedTools) != 2 || publishedTools["event_add"] == nil || publishedTools["file_read"] == nil {
		t.Fatalf("expected the requester's catalog, got %+v", toolList.Tools)
	}
	if !publishedTools["file_read"].Annotations.ReadOnlyHint || publishedTools["event_add"].Annotations.ReadOnlyHint {
		t.Fatalf("expected the side effect class to reach the harness as a read-only hint, got %+v", publishedTools)
	}
	if publishedTools["event_add"].Meta[toolcontract.MetaKeyApprovalScope] != "calendar" {
		t.Fatalf("expected the approval scope to survive as metadata, got %+v", publishedTools["event_add"].Meta)
	}
}

func TestToolCatalogServerExecutesInsideTheDaemonAndReportsFailureAsAToolError(t *testing.T) {
	invokedTool := ""
	clientSession := connectedCatalogSession(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: testToolSet(t, &invokedTool)})

	callResult, errorValue := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "event_add",
		Arguments: map[string]any{"note": "내일 회의"},
	})
	if errorValue != nil {
		t.Fatalf("expected the tool call to reach the daemon: %v", errorValue)
	}
	if invokedTool != "event_add" {
		t.Fatalf("expected the daemon to run the tool, got %q", invokedTool)
	}
	if callResult.IsError {
		t.Fatalf("expected a successful call, got %+v", callResult)
	}
	textContent, isText := callResult.Content[0].(*mcp.TextContent)
	if !isText || !strings.Contains(textContent.Text, "executed event_add") {
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
		if tool.Name == "event_add" {
			t.Fatalf("expected a narrowed catalog to hide event_add, got %+v", toolList.Tools)
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
		Name:      "event_add",
		Arguments: map[string]any{"note": "내일 회의"},
	}); errorValue != nil {
		t.Fatalf("expected the tool call to reach the daemon: %v", errorValue)
	}

	if gate.invocationTaskRunID != "task-run-1" {
		t.Fatalf("expected an out-of-process call to carry its task run the same way the bundled loop does, got %q", gate.invocationTaskRunID)
	}
}

func TestAHarnessReadsBackTheApprovalFactsAGatedScopedToolPublished(t *testing.T) {
	published := toolcontract.ToolDefinition{
		ID:                   "test:event_delete",
		Name:                 "event_delete",
		Description:          "event_delete description",
		Visibility:           toolcontract.ToolVisibilityModel,
		InputSchema:          json.RawMessage(`{"type":"object","properties":{"eventHint":{"type":"string"}}}`),
		SideEffectClass:      toolcontract.ToolSideEffectDestructive,
		RequiresApproval:     true,
		ApprovalScope:        "calendar",
		ApprovalScopeSummary: "every change to the team calendar",
		ApprovalInputFields:  []string{"eventHint", "reason"},
		ResultContract:       &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
	}
	toolSet := toolcontract.NewToolSet([]string{"event_delete"})
	toolSet.AllowTestReplacement()
	handler := func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("deleted", json.RawMessage(`{}`)), nil
	}
	if errorValue := toolSet.RegisterTool(published, handler); errorValue != nil {
		t.Fatal(errorValue)
	}
	clientSession := connectedCatalogSession(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: toolSet})

	toolList, errorValue := clientSession.ListTools(context.Background(), nil)
	if errorValue != nil || len(toolList.Tools) != 1 {
		t.Fatalf("expected the gated tool to be listed: %+v, %v", toolList, errorValue)
	}
	var read toolcontract.ToolDescriptor
	toolcontract.ApplyDescriptorMeta(&read, toolList.Tools[0].Meta)

	if read.SideEffectClass != published.SideEffectClass || !read.RequiresApproval || read.ApprovalScope != published.ApprovalScope ||
		read.ApprovalScopeSummary != published.ApprovalScopeSummary || !reflect.DeepEqual(read.ApprovalInputFields, published.ApprovalInputFields) {
		t.Fatalf("the harness did not read back what the tool published: %+v", read)
	}
}

type allowingGate struct{}

func (allowingGate) ReviewToolCall(context.Context, toolcontract.ToolInvocation, toolcontract.ToolDefinition) (toolcontract.ToolCallReview, error) {
	return toolcontract.ToolCallReview{MayProceed: true}, nil
}

func publishedHostGatedFacts(t *testing.T, gate toolcontract.ToolCallGate) map[string]bool {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"event_delete", "file_read"})
	toolSet.AllowTestReplacement()
	for name, requiresApproval := range map[string]bool{"event_delete": true, "file_read": false} {
		errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
			ID: "test:" + name, Name: name, Description: name, Visibility: toolcontract.ToolVisibilityModel,
			InputSchema:      json.RawMessage(`{"type":"object"}`),
			RequiresApproval: requiresApproval,
			ResultContract:   &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
		}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return toolcontract.ToolSuccess("ok"), nil
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	if gate != nil {
		toolSet.UseToolCallGate(gate)
	}
	toolList, errorValue := connectedCatalogSession(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: toolSet}).ListTools(context.Background(), nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	facts := map[string]bool{}
	for _, tool := range toolList.Tools {
		var read toolcontract.ToolDescriptor
		toolcontract.ApplyDescriptorMeta(&read, tool.Meta)
		facts[tool.Name] = read.IsHostGated
	}
	return facts
}

func TestOnlyAToolTheHostGatesIsPublishedAsHostGated(t *testing.T) {
	if gated := publishedHostGatedFacts(t, allowingGate{}); !gated["event_delete"] || gated["file_read"] {
		t.Fatalf("the approval-requiring tool is the one the host gates, got %v", gated)
	}
	if ungated := publishedHostGatedFacts(t, nil); ungated["event_delete"] || ungated["file_read"] {
		t.Fatalf("with no gate on the tool set nothing is held for the host, got %v", ungated)
	}
}

func TestAnImageAToolReadReachesTheHarnessAsMCPImageContent(t *testing.T) {
	picture := []byte{0x89, 'P', 'N', 'G', 0x01, 0x02}
	toolSet := toolcontract.NewToolSet([]string{"image_read"})
	toolSet.AllowTestReplacement()
	errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID:              "test:image_read",
		Name:            "image_read",
		Description:     "image_read description",
		Visibility:      toolcontract.ToolVisibilityModel,
		InputSchema:     json.RawMessage(`{"type":"object"}`),
		SideEffectClass: toolcontract.ToolSideEffectRead,
		ResultContract:  &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		result := toolcontract.ToolSuccessData("a chart", json.RawMessage(`{}`))
		result.Attachments = []toolcontract.FileAttachment{
			{DevicePath: "/workspace/chart.png", ContentType: "image/png", ContentBase64: base64.StdEncoding.EncodeToString(picture)},
			{ContentType: "application/pdf", ContentBase64: "JVBERg=="},
		}
		return result, nil
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	clientSession := connectedCatalogSession(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: toolSet})

	callResult, errorValue := clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "image_read", Arguments: map[string]any{}})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if len(callResult.Content) != 2 {
		t.Fatalf("expected the text and one image, got %+v", callResult.Content)
	}
	image, isImage := callResult.Content[1].(*mcp.ImageContent)
	if !isImage || image.MIMEType != "image/png" || !bytes.Equal(image.Data, picture) {
		t.Fatalf("the picture changed on the way: %+v", callResult.Content[1])
	}
	var read toolcontract.FileAttachment
	toolcontract.ApplyAttachmentMeta(&read, image.Meta)
	if read.DevicePath != "/workspace/chart.png" {
		t.Fatalf("the picture lost the path it can be reloaded from: %+v", image.Meta)
	}
}
