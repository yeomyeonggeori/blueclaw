//go:build appliance && llmeval

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/persona"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestUserProfileUpdateLive(t *testing.T) {
	if !truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")) {
		t.Skip("set BLUECLAW_E2E_LIVE=1 to run the costed profile evaluation")
	}
	model := liveMemoryModel(t, os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT"), os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET"))
	artifactRoot := filepath.Join("..", "..", ".artifacts", "persona-live")
	if errorValue := os.MkdirAll(artifactRoot, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	directory, errorValue := os.MkdirTemp(artifactRoot, "session-")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	scenario := VirtualSessionScenario{
		Name: "user_profile_live", ArtifactDirectoryPath: directory,
		LanguageModel: model, DisableScriptedModel: true, FailOnLanguageModelError: true,
		AllowedTools:           []string{"persona_read", "persona_update", "memory_search", "memory_remember"},
		WritableWorkspacePaths: []string{"private/people/person-1/.internkim/user.json", ".blueclaw/state/persona-backup/people/person-1/user.json"},
		Turns:                  []VirtualTurn{{Prompt: "From now on, please answer me in Korean.", RouterTaskShape: agentcontract.TaskShapeResearchTask, ExpectedResponse: VirtualResponseReply}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, executionError := RunVirtualSession(ctx, scenario)
	preserveLiveSessionEvidence(t, directory, result, executionError)
	if executionError != nil {
		t.Fatal(executionError)
	}
	if len(result.TurnResults) != 1 || result.TurnResults[0].TaskStatus != "completed" {
		t.Fatal("the profile evaluation did not complete")
	}
	if !eventsContain(result.TurnResults[0].Events, "tool.persona_update.requested", "") {
		t.Fatal("the model did not choose the profile update tool")
	}
	document, errorValue := os.ReadFile(filepath.Join(result.ArtifactDirectoryPath, "workspace", "private", "people", "person-1", ".internkim", "user.json"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	user, errorValue := persona.ParseUser(document)
	if errorValue != nil || user.Language == nil || user.Language.Default != "ko" {
		t.Fatalf("the requested language was not persisted in the profile: %v", errorValue)
	}
}
