//go:build appliance && llmeval

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
)

func TestPresentationLocalMultiturnSuccessLive(t *testing.T) {
	if !truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")) {
		t.Skip("set BLUECLAW_E2E_LIVE=1 to explicitly run costed live slides virtual session")
	}
	endpoint := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT"))
	socketPath := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET"))
	if endpoint == "" && socketPath == "" {
		t.Skip("set BLUECLAW_E2E_LLM_ENDPOINT or BLUECLAW_E2E_LLM_UNIX_SOCKET to run live slides virtual session")
	}
	scenario := PresentationLocalMultiturnSuccessScenario(t.TempDir())
	if skillDirectoryPath := rootPresentationSkillPath(); skillDirectoryPath != "" {
		scenario.Skills = nil
		scenario.SkillDirectoryPaths = []string{skillDirectoryPath}
	}
	scenario.LanguageModel = llm.CapabilityLLMClient{
		CapabilityClient: capability.NewClient(capability.Configuration{
			Endpoint:       endpoint,
			UnixSocketPath: socketPath,
		}),
		ModelName:     os.Getenv("BLUECLAW_E2E_LLM_MODEL"),
		ExecutionMode: firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_LLM_EXECUTION_MODE"), "auto"),
	}

	result, errorValue := RunVirtualSession(context.Background(), scenario)
	if errorValue != nil {
		t.Fatalf("expected slides scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.shell.result", "exitCode") {
		t.Fatal("expected terminal build to succeed")
	}
}
