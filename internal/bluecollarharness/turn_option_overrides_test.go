package bluecollarharness

import (
	"reflect"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestAHostThatPinsATurnBudgetGetsTheBudgetItPinned(t *testing.T) {
	derivedOptions := deriveTurnOptions(config.RuntimeConfiguration{})

	pinnedOptions := turnOptionsWithOverrides(derivedOptions, agentcontract.TurnOptions{
		MaxIterationCount: 2,
		MaxToolCallCount:  3,
		MaxElapsedSecond:  4,
	})

	if pinnedOptions.MaxIterationCount != 2 || pinnedOptions.MaxToolCallCount != 3 || pinnedOptions.MaxElapsedSecond != 4 {
		t.Fatalf("a scenario that pins a turn budget is why the rig needed a harness port of its own, got %+v", pinnedOptions)
	}
}

func TestAHostThatPinsNothingKeepsWhatTheConfigurationSaid(t *testing.T) {
	derivedOptions := deriveTurnOptions(config.RuntimeConfiguration{})

	if pinnedOptions := turnOptionsWithOverrides(derivedOptions, agentcontract.TurnOptions{}); !reflect.DeepEqual(pinnedOptions, derivedOptions) {
		t.Fatalf("an empty override must leave the configured budget alone, got %+v against %+v", pinnedOptions, derivedOptions)
	}
}
