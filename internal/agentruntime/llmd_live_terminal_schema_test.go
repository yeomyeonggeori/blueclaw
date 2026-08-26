package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
)

func TestLLMDLiveLowCanonicalTerminalSchemaFromEnv(t *testing.T) {
	socketPath := strings.TrimSpace(os.Getenv("BLUECLAW_LLMD_LIVE_SOCKET"))
	authKey := strings.TrimSpace(os.Getenv("BLUECLAW_LLMD_LIVE_AUTH_KEY"))
	if socketPath == "" || authKey == "" {
		t.Skip("BLUECLAW_LLMD_LIVE_SOCKET and BLUECLAW_LLMD_LIVE_AUTH_KEY are required")
	}
	client := llm.NewLLMDClient(llm.LLMDClientConfiguration{
		UnixSocketPath: socketPath,
		AuthKey:        authKey,
		ModelName:      llm.ResolveModelTierNames(config.RuntimeConfiguration{}).Low,
		ExecutionMode:  "remote",
	})
	response, errorValue := client.GenerateChatCompletion(context.Background(), llm.ChatCompletionRequest{
		SchemaName: "bluecollar_agent_turn_action",
		Messages: []llm.ChatCompletionMessage{{
			Role:    "user",
			Content: "Call shell with command printf llmd-terminal-ok.",
		}},
		Tools: []llm.ChatCompletionTool{{
			Type: "function",
			Function: llm.ChatCompletionFunction{
				Name:        "shell",
				Description: "Run a command in the requester workspace.",
				Parameters:  terminalRunInputSchema,
			},
		}},
		ToolChoice:        json.RawMessage(`"required"`),
		ParallelToolCalls: false,
	})
	if errorValue != nil {
		t.Fatalf("expected low LLMD terminal tool call: %v", errorValue)
	}
	if response.FinishReason != "tool_calls" || len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one terminal tool call, got %+v", response)
	}
	toolCall := response.Message.ToolCalls[0]
	if toolCall.Function.Name != "shell" {
		t.Fatalf("expected shell, got %+v", toolCall)
	}
	var input terminalRunToolInput
	if errorValue := json.Unmarshal([]byte(toolCall.Function.Arguments), &input); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := validateTerminalRunInput(input); errorValue != nil {
		t.Fatalf("expected canonical terminal input, got %s: %v", toolCall.Function.Arguments, errorValue)
	}
	if strings.TrimSpace(input.Command) == "" {
		t.Fatalf("expected command input, got %+v", input)
	}
}
