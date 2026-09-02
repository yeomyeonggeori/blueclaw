package llm_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/bluecollar/loop"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func terminalRunProbeRequest(t *testing.T, prompt string) llm.StructuredResponseRequest {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{toolcontract.ShellToolName})
	definition := toolcontract.ToolDefinition{
		Name:           toolcontract.ShellToolName,
		Description:    "Run a terminal command.",
		Visibility:     toolcontract.ToolVisibilityModel,
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`),
		ResultContract: &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
	}
	registerError := toolSet.RegisterTool(definition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("not executed", json.RawMessage(`{}`)), nil
	})
	if registerError != nil {
		t.Fatalf("expected the probe tool to register: %v", registerError)
	}
	return loop.BuildActionRequestForTurn(loop.AgentTurnRequest{Prompt: prompt, ToolSet: toolSet})
}

func assertContinuedWithTerminalRun(t *testing.T, action loop.ProbedAgentAction) {
	t.Helper()
	if action.Action != "continue" || action.ToolName != toolcontract.ShellToolName {
		t.Fatalf("expected shell continue action, got %+v", action)
	}
	var toolInput struct {
		Command string `json:"command"`
	}
	if errorValue := json.Unmarshal(action.ToolInput, &toolInput); errorValue != nil {
		t.Fatalf("expected terminal input, got %s: %v", action.ToolInput, errorValue)
	}
	if strings.TrimSpace(toolInput.Command) == "" {
		t.Fatalf("expected non-empty terminal command, got %s", action.ToolInput)
	}
}

func TestOpenRouterLiveLowTierCurrentAgentActionSchemaFromEnv(t *testing.T) {
	if os.Getenv("BLUECLAW_LIVE_LLM_TEST") != "1" {
		t.Skip("set BLUECLAW_LIVE_LLM_TEST=1 to run the low-tier action schema test")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is required for the low-tier action schema test")
	}
	request := terminalRunProbeRequest(t, "Do not finish. Choose continue, call shell, and set command to printf low-tier-schema-ok.")
	client := llm.OpenRouterClient{
		APIKey:       apiKey,
		BaseURL:      llm.DefaultOpenRouterChatCompletionsURL,
		ModelName:    llm.DefaultModelTierNames().XLow,
		AttemptCount: 1,
	}
	response, errorValue := client.GenerateStructuredResponse(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected low-tier response for current action schema: %v", errorValue)
	}
	action, errorValue := loop.ProbeAgentActionResponse(response)
	if errorValue != nil {
		t.Fatalf("expected parsable agent action, got %q: %v", response.Content, errorValue)
	}
	assertContinuedWithTerminalRun(t, action)
}
