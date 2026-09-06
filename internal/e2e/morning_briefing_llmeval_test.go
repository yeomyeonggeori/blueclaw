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

func TestMorningBriefingSettingsLive(t *testing.T) {
	if !truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")) {
		t.Skip("set BLUECLAW_E2E_LIVE=1 to run the costed morning briefing evaluation")
	}
	model := liveMemoryModel(t, os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT"), os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET"))
	artifactRoot := filepath.Join("..", "..", ".artifacts", "morning-briefing-live")
	if errorValue := os.MkdirAll(artifactRoot, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	directory, errorValue := os.MkdirTemp(artifactRoot, "session-")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	scenario := VirtualSessionScenario{
		Name: "morning_briefing_settings_live", ArtifactDirectoryPath: directory,
		LanguageModel: model, DisableScriptedModel: true, FailOnLanguageModelError: true,
		AllowedTools:           []string{"persona_read", "persona_update"},
		WritableWorkspacePaths: []string{"private/people/person-1/.internkim/user.json", ".blueclaw/state/persona-backup/people/person-1/user.json"},
		Turns: []VirtualTurn{
			{Prompt: "아침 브리핑을 매일 오전 9시 15분으로 바꾸고 지금부터 비활성화해줘.", RouterTaskShape: agentcontract.TaskShapeResearchTask, ExpectedResponse: VirtualResponseReply},
			{Prompt: "시간만 오전 10시로 바꿔줘. 비활성화 상태는 그대로 유지해.", RouterTaskShape: agentcontract.TaskShapeResearchTask, ExpectedResponse: VirtualResponseReply},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, executionError := RunVirtualSession(ctx, scenario)
	preserveLiveSessionEvidence(t, directory, result, executionError)
	if executionError != nil {
		t.Fatal(executionError)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turns, got %d", len(result.TurnResults))
	}
	for turnIndex, turnResult := range result.TurnResults {
		if turnResult.TaskStatus != "completed" {
			t.Fatalf("turn %d did not complete: %s", turnIndex+1, turnResult.TaskStatus)
		}
		if !eventsContain(turnResult.Events, "tool.persona_update.requested", "") {
			t.Fatalf("turn %d did not choose persona_update", turnIndex+1)
		}
	}
	document, errorValue := os.ReadFile(filepath.Join(result.ArtifactDirectoryPath, "workspace", "private", "people", "person-1", ".internkim", "user.json"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	user, errorValue := persona.ParseUser(document)
	if errorValue != nil || user.MorningBriefing == nil || user.MorningBriefing.Enabled || user.MorningBriefing.Time != "10:00" {
		t.Fatalf("morning briefing settings were not persisted: %+v (%v)", user.MorningBriefing, errorValue)
	}
}
