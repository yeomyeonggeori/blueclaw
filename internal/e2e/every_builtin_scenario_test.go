package e2e

import (
	"context"
	"testing"
)

func TestEveryBuiltinScenarioPassesAgainstItsScriptedModel(t *testing.T) {
	for _, scenarioName := range BuiltinScenarioNames() {
		t.Run(scenarioName, func(t *testing.T) {
			skipWhereTheHarnessCannotEndTheTurnOnAParkedRun(t, scenarioName)
			scenario, errorValue := BuiltinScenario(scenarioName, t.TempDir())
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if scenario.NeedsLiveLanguageModel() {
				t.Skip("scripts no model action, so only a live model can answer it")
			}
			if _, errorValue := RunVirtualSession(context.Background(), scenario); errorValue != nil {
				t.Fatalf("%s: %v", scenarioName, errorValue)
			}
		})
	}
}
