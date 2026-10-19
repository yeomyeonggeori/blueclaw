//go:build !nobundledharness

package e2e

import (
	"context"
	"strings"
	"testing"
)

func TestEveryBuiltinScenarioPassesAgainstItsScriptedModel(t *testing.T) {
	for _, scenarioName := range BuiltinScenarioNames() {
		t.Run(scenarioName, func(t *testing.T) {
			scenario, errorValue := BuiltinScenario(scenarioName, t.TempDir())
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if scenario.NeedsLiveLanguageModel() {
				t.Skip("scripts no model action, so only a live model can answer it")
			}
			result, errorValue := RunVirtualSession(context.Background(), scenario)
			if errorValue != nil {
				t.Fatalf("%s: %v", scenarioName, errorValue)
			}
			assertModelNeverSawTheArtifactPath(t, result)
		})
	}
}

func assertModelNeverSawTheArtifactPath(t *testing.T, result VirtualSessionResult) {
	t.Helper()
	for turnIndex, turnResult := range result.TurnResults {
		if strings.Contains(turnResult.ModelContext, result.ArtifactDirectoryPath) {
			t.Fatalf("turn %d showed the model the artifact path %s", turnIndex+1, result.ArtifactDirectoryPath)
		}
	}
}
