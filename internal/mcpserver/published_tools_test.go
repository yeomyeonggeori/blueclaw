package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func mixedToolSet(t *testing.T) *toolcontract.ToolSet {
	t.Helper()
	toolNames := []string{toolcontract.TerminalRunToolName, toolcontract.FileReadToolName, "event_add", toolcontract.AskConfirmToolName}
	toolSet := toolcontract.NewToolSet(toolNames)
	toolSet.AllowTestReplacement()
	for _, toolName := range toolNames {
		errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
			ID:             "test:" + toolName,
			Name:           toolName,
			Description:    toolName,
			Visibility:     toolcontract.ToolVisibilityModel,
			InputSchema:    json.RawMessage(`{"type":"object","properties":{}}`),
			ResultContract: &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
		}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return toolcontract.ToolSuccessData("ok", json.RawMessage(`{}`)), nil
		})
		if errorValue != nil {
			t.Fatalf("expected %s to register: %v", toolName, errorValue)
		}
	}
	return toolSet
}

func publishedToolNames(t *testing.T, audience ToolAudience) map[string]bool {
	t.Helper()
	clientSession := connectedCatalogSession(t, RequesterToolSet{RequesterPersonID: "person-1", ToolSet: mixedToolSet(t), ToolAudience: audience})
	toolList, errorValue := clientSession.ListTools(context.Background(), nil)
	if errorValue != nil {
		t.Fatalf("expected the catalog to list: %v", errorValue)
	}
	published := map[string]bool{}
	for _, tool := range toolList.Tools {
		published[tool.Name] = true
	}
	return published
}

func TestASelfEquippedHarnessIsNotHandedTheToolsItAlreadyHas(t *testing.T) {
	published := publishedToolNames(t, ToolAudienceSelfEquipped)

	for _, harnessOwned := range []string{toolcontract.TerminalRunToolName, toolcontract.FileReadToolName} {
		if published[harnessOwned] {
			t.Fatalf("expected %q to be left to the harness, it was published", harnessOwned)
		}
	}
	if !published["event_add"] {
		t.Fatal("expected a domain operation to be published, since no harness can do it")
	}
	if !published[toolcontract.AskConfirmToolName] {
		t.Fatal("expected asking the requester to be published, since it reaches them through the daemon's connector")
	}
}

func TestABareHarnessStillGetsEverything(t *testing.T) {
	published := publishedToolNames(t, ToolAudienceBare)

	for _, toolName := range []string{toolcontract.TerminalRunToolName, toolcontract.FileReadToolName, "event_add", toolcontract.AskConfirmToolName} {
		if !published[toolName] {
			t.Fatalf("expected a harness with no tools of its own to receive %q", toolName)
		}
	}
}
