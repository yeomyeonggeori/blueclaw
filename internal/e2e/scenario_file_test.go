package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadScenarioFileReadsSequentialStepsAndResolvesSkills(t *testing.T) {
	temporaryDirectoryPath := t.TempDir()
	scenarioDirectoryPath := filepath.Join(temporaryDirectoryPath, "tests", "expensive")
	if errorValue := os.MkdirAll(scenarioDirectoryPath, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	scenarioPath := filepath.Join(scenarioDirectoryPath, "task.json")
	document := `{
  "name": "task-lifecycle",
  "skillDirectoryPaths": ["../../skills/flow"],
  "allowedTools": ["task_add", "task_delete"],
  "capabilityToolNames": ["task_add", "task_delete"],
  "capabilityToolDescriptors": [{"name":"task_delete","requiresApproval":true}],
  "steps": [
    {"prompt":"add task","expectedResponse":"background_action","expectedToolCalls":["task_add"]},
    {"prompt":"delete approved","expectedEvents":["approval.executed"]}
  ]
}`
	if errorValue := os.WriteFile(scenarioPath, []byte(document), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}

	scenario, errorValue := LoadScenarioFile(scenarioPath, "/tmp/artifacts/task")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if scenario.Name != "task-lifecycle" || len(scenario.Turns) != 2 {
		t.Fatalf("unexpected scenario: %+v", scenario)
	}
	expectedSkillPath := filepath.Join(temporaryDirectoryPath, "skills", "flow")
	if len(scenario.SkillDirectoryPaths) != 1 || scenario.SkillDirectoryPaths[0] != expectedSkillPath {
		t.Fatalf("expected resolved skill path %q, got %v", expectedSkillPath, scenario.SkillDirectoryPaths)
	}
	if scenario.Turns[0].ExpectedToolCalls[0] != "task_add" || scenario.Turns[1].ExpectedEvents[0] != "approval.executed" {
		t.Fatalf("unexpected sequential steps: %+v", scenario.Turns)
	}
	if scenario.Turns[0].ExpectedResponse != VirtualResponseBackgroundAction {
		t.Fatalf("expected background action response, got %q", scenario.Turns[0].ExpectedResponse)
	}
	if scenario.Turns[0].MinimumReplyLength != 0 || scenario.Turns[1].MinimumReplyLength != 1 {
		t.Fatalf("expected only reply steps to require non-empty text, got %+v", scenario.Turns)
	}
}

func TestLoadScenarioFileRejectsUnknownFieldsAndEmptySteps(t *testing.T) {
	for testName, document := range map[string]string{
		"unknown":          `{"name":"bad","steps":[{"prompt":"hi"}],"unexpected":true}`,
		"empty":            `{"name":"bad","steps":[]}`,
		"invalid response": `{"name":"bad","steps":[{"prompt":"hi","expectedResponse":"maybe"}]}`,
		"scripted actions": `{"name":"bad","steps":[{"prompt":"hi","actionResponses":["finish"]}]}`,
	} {
		t.Run(testName, func(t *testing.T) {
			scenarioPath := filepath.Join(t.TempDir(), "scenario.json")
			if errorValue := os.WriteFile(scenarioPath, []byte(document), 0o600); errorValue != nil {
				t.Fatal(errorValue)
			}
			_, errorValue := LoadScenarioFile(scenarioPath, t.TempDir())
			if errorValue == nil || (!strings.Contains(errorValue.Error(), "unknown field") && !strings.Contains(errorValue.Error(), "sequential step") && !strings.Contains(errorValue.Error(), "expectedResponse") && !strings.Contains(errorValue.Error(), "actionResponses")) {
				t.Fatalf("expected scenario validation error, got %v", errorValue)
			}
		})
	}
}
