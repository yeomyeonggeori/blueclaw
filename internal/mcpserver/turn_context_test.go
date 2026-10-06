package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type turnValueKey struct{}

func toolSetReadingTheTurnValue(t *testing.T, seenValue *any) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"file_read"})
	toolSet.AllowTestReplacement()
	errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID: "test:file_read", Name: "file_read", Description: "read", Visibility: toolcontract.ToolVisibilityModel,
		InputSchema:    json.RawMessage(`{"type":"object"}`),
		ResultContract: &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
	}, func(ctx context.Context, _ toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		*seenValue = ctx.Value(turnValueKey{})
		return toolcontract.ToolSuccessData("read", json.RawMessage(`{}`)), nil
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return toolSet
}

func callFileRead(t *testing.T, requesterToolSet RequesterToolSet) {
	t.Helper()
	clientSession := connectedCatalogSession(t, requesterToolSet)
	if _, errorValue := clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "file_read", Arguments: map[string]any{}}); errorValue != nil {
		t.Fatalf("expected the call to run: %v", errorValue)
	}
}

func TestAToolCallCarriesTheValuesOfTheTurnItBelongsTo(t *testing.T) {
	var seenValue any
	callFileRead(t, RequesterToolSet{
		RequesterPersonID: "person-1",
		ToolSet:           toolSetReadingTheTurnValue(t, &seenValue),
		TurnContext:       context.WithValue(context.Background(), turnValueKey{}, "the connector's event"),
	})

	if seenValue != "the connector's event" {
		t.Fatalf("a call made over the wire runs inside the turn that asked for it, got %v", seenValue)
	}
}

func TestAToolCallOfATurnNobodyNamedCarriesNothingExtra(t *testing.T) {
	var seenValue any
	callFileRead(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: toolSetReadingTheTurnValue(t, &seenValue)})

	if seenValue != nil {
		t.Fatalf("a turn that handed over no context hands over no values, got %v", seenValue)
	}
}
