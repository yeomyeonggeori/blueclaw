package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func toolSetWithTheLoopsOwnTools(t *testing.T) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"event_add"})
	toolSet.AllowTestReplacement()
	register := func(name string, providerID string, visibility string, effects []toolcontract.ResourceEffect) {
		errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
			ID:             "test:" + name,
			ProviderID:     providerID,
			Name:           name,
			Description:    name,
			Visibility:     visibility,
			InputSchema:    json.RawMessage(`{"type":"object","properties":{}}`),
			ResultContract: &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
		}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			result := toolcontract.ToolSuccessData("ran "+name, json.RawMessage(`{}`))
			result.Effects = effects
			return result, nil
		})
		if errorValue != nil {
			t.Fatalf("expected %s to register: %v", name, errorValue)
		}
	}
	register("event_add", "capabilityd", toolcontract.ToolVisibilityModel, []toolcontract.ResourceEffect{})
	register("memory_forget", "local", toolcontract.ToolVisibilityModel, nil)
	register(toolcontract.BashToolName, toolcontract.BuiltInToolProviderID, toolcontract.ToolVisibilityModel, nil)
	register(toolcontract.AskInputToolName, "local", toolcontract.ToolVisibilityInternal, nil)
	register(toolcontract.FileDeliverToolName, toolcontract.BuiltInToolProviderID, toolcontract.ToolVisibilityInternal, nil)
	return toolSet
}

func bareAudienceSession(t *testing.T) (*mcp.ClientSession, map[string]*mcp.Tool) {
	t.Helper()
	clientSession := connectedCatalogSession(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: toolSetWithTheLoopsOwnTools(t), ToolAudience: ToolAudienceBare})
	toolList, errorValue := clientSession.ListTools(context.Background(), nil)
	if errorValue != nil {
		t.Fatalf("expected the catalog to list: %v", errorValue)
	}
	tools := map[string]*mcp.Tool{}
	for _, tool := range toolList.Tools {
		tools[tool.Name] = tool
	}
	return clientSession, tools
}

func TestTheLoopIsOfferedTheKernelToolsAndTheHiddenOnesItCallsOnTheModelsBehalf(t *testing.T) {
	_, tools := bareAudienceSession(t)

	for _, toolName := range []string{"event_add", toolcontract.BashToolName, toolcontract.AskInputToolName, toolcontract.FileDeliverToolName} {
		if tools[toolName] == nil {
			t.Fatalf("expected the loop to be offered %q, got %v", toolName, tools)
		}
	}
}

func TestTheLoopIsNotOfferedAToolTheProfileDoesNotAllow(t *testing.T) {
	_, tools := bareAudienceSession(t)

	if tools["memory_forget"] != nil {
		t.Fatal("a tool outside the profile's allowed names is still outside it when the harness is the bundled agent")
	}
}

func TestAHiddenToolRunsWhenTheLoopCallsIt(t *testing.T) {
	clientSession, _ := bareAudienceSession(t)

	result, errorValue := clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: toolcontract.AskInputToolName, Arguments: map[string]any{}})

	if errorValue != nil || result.IsError {
		t.Fatalf("expected the hidden tool to run for the loop, got %+v, %v", result, errorValue)
	}
}

func TestACatalogToolCarriesItsWholeDescriptorForTheLoopToRead(t *testing.T) {
	_, tools := bareAudienceSession(t)

	encoded, _ := json.Marshal(tools[toolcontract.AskInputToolName].Meta)
	received := map[string]any{}
	_ = json.Unmarshal(encoded, &received)
	descriptor := toolcontract.ToolDescriptor{Name: toolcontract.AskInputToolName}
	toolcontract.ApplyDescriptorMeta(&descriptor, received)

	if descriptor.Visibility != toolcontract.ToolVisibilityInternal || descriptor.ResultContract == nil {
		t.Fatalf("the loop keeps a hidden tool hidden only if the catalog says it is hidden, got %+v", descriptor)
	}
}

func TestAToolResultCarriesItsEffectsForTheCompletionGate(t *testing.T) {
	toolSet := toolcontract.NewToolSet([]string{"event_add"})
	toolSet.AllowTestReplacement()
	_ = toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID: "test:event_add", Name: "event_add", Description: "event_add", Visibility: toolcontract.ToolVisibilityModel,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		ResultContract: &toolcontract.ToolResultContract{
			Schema:  json.RawMessage(`{"type":"object"}`),
			Effects: []toolcontract.ResourceEffectContract{{ObjectType: "event", Effect: "created", ResultField: "eventID", EffectIdentity: "id"}},
		},
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		result := toolcontract.ToolSuccessData("added", json.RawMessage(`{"eventID":"event-1"}`))
		result.Effects = []toolcontract.ResourceEffect{{ObjectType: "event", Effect: "created", ID: "event-1"}}
		return result, nil
	})
	clientSession := connectedCatalogSession(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: toolSet, ToolAudience: ToolAudienceBare})

	result, errorValue := clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "event_add", Arguments: map[string]any{}})
	if errorValue != nil {
		t.Fatalf("expected the call to run: %v", errorValue)
	}

	encoded, _ := json.Marshal(result.Meta)
	received := map[string]any{}
	_ = json.Unmarshal(encoded, &received)
	carried, isCarried := toolcontract.ResultOfMeta(received)
	if !isCarried || len(carried.Effects) != 1 || carried.Effects[0].ID != "event-1" {
		t.Fatalf("effects are how a completion gate knows a change happened, got %+v", carried)
	}
}
