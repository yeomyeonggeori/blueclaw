//go:build appliance && llmeval

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
)

func TestMemoryRecallWithoutConversationHistoryLive(t *testing.T) {
	if !truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")) {
		t.Skip("set BLUECLAW_E2E_LIVE=1 to explicitly run costed live memory evaluation")
	}
	endpoint := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT"))
	socketPath := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET"))
	if endpoint == "" && socketPath == "" {
		t.Skip("set BLUECLAW_E2E_LLM_ENDPOINT or BLUECLAW_E2E_LLM_UNIX_SOCKET to run live memory evaluation")
	}
	model := liveMemoryModel(t, endpoint, socketPath)
	remembered := runLiveMemoryTurn(t, model, []memory.MemoryFact{{
		FactID: "synthetic-memory-fact", ScopeType: memory.ScopeTypeUser,
		NamespaceID: memory.UserNamespace("person-1").NamespaceID,
		Content:     "The internal codename for the migration is Blue Lantern.",
		SourceKind:  memory.MemorySourceKindFact,
		ValidAt:     time.Now().UTC(),
	}}, "What is the internal codename for the migration?", "Blue Lantern")
	if !strings.Contains(remembered.FinishMessage, "Blue Lantern") {
		t.Fatalf("memory-backed answer omitted the stored fact: %q", remembered.FinishMessage)
	}
	if !eventsContain(remembered.Events, "tool.memory_search.requested", "") {
		t.Fatal("expected live model to request memory_search")
	}
	control := runLiveMemoryTurn(t, model, nil, "What is the internal codename for the migration?", "Blue Lantern")
	if strings.Contains(control.FinishMessage, "Blue Lantern") {
		t.Fatalf("empty-memory control invented the stored answer: %q", control.FinishMessage)
	}
}

func liveMemoryModel(t *testing.T, endpoint string, socketPath string) llm.LanguageModelProvider {
	t.Helper()
	if os.Getenv("BLUECLAW_E2E_LLM_PROVIDER") == "endpoint" {
		provider, errorValue := (openaicompatible.Endpoint{URL: endpoint, ModelName: os.Getenv("BLUECLAW_E2E_LLM_MODEL"), APIKey: os.Getenv("BLUECOLLAR_MODEL_API_KEY")}).Provider()
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		return provider
	}
	return llm.CapabilityLLMClient{CapabilityClient: capability.NewClient(capability.Configuration{Endpoint: endpoint, UnixSocketPath: socketPath}), ModelName: os.Getenv("BLUECLAW_E2E_LLM_MODEL"), ExecutionMode: firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_LLM_EXECUTION_MODE"), "auto")}
}

func runLiveMemoryTurn(t *testing.T, model llm.LanguageModelProvider, initialMemory []memory.MemoryFact, prompt string, forbiddenAnswer string) VirtualTurnResult {
	t.Helper()
	forbiddenReplyFragments := []string{}
	if len(initialMemory) == 0 {
		forbiddenReplyFragments = []string{forbiddenAnswer}
	}
	artifactRoot := firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_ARTIFACT_DIR"), filepath.Join("..", "..", ".artifacts", "memory-recall-live"))
	if errorValue := os.MkdirAll(artifactRoot, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	artifactDirectory, errorValue := os.MkdirTemp(artifactRoot, "session-")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Logf("memory evidence: %s", artifactDirectory)
	scenario := VirtualSessionScenario{Name: "memory_recall_live", ArtifactDirectoryPath: artifactDirectory, LanguageModel: model, DisableScriptedModel: true, FailOnLanguageModelError: true, InitialMemory: initialMemory, AllowedTools: []string{"memory_search"}, Turns: []VirtualTurn{{Prompt: prompt, RouterTaskShape: agentcontract.TaskShapeResearchTask, ExpectedResponse: VirtualResponseReply, ForbiddenReplyFragments: forbiddenReplyFragments}}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, errorValue := RunVirtualSession(ctx, scenario)
	preserveLiveSessionEvidence(t, artifactDirectory, result, errorValue)
	if errorValue != nil {
		t.Fatalf("live memory evaluation failed: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one live memory turn, got %d", len(result.TurnResults))
	}
	if result.TurnResults[0].TaskStatus != "completed" || strings.TrimSpace(result.TurnResults[0].FinishMessage) == "" {
		t.Fatalf("memory evaluation did not produce a completed answer: %+v", result.TurnResults[0])
	}
	return result.TurnResults[0]
}

func preserveLiveSessionEvidence(t *testing.T, directory string, result VirtualSessionResult, executionError error) {
	t.Helper()
	failure := ""
	if executionError != nil {
		failure = executionError.Error()
	}
	document, errorValue := json.MarshalIndent(struct {
		Result VirtualSessionResult `json:"result"`
		Error  string               `json:"error,omitempty"`
	}{Result: result, Error: failure}, "", "  ")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(directory, "result.json"), document, 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
}

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
