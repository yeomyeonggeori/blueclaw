package bluecollarharness

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/loop"
)

func TestDeriveTurnOptionsWiresContextWindowTokens(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.ContextWindowTokens = 128000
	seed := int64(41)
	temperature := 0.2
	runtimeConfiguration.Agent.GenerationOptions.Seed = &seed
	runtimeConfiguration.Agent.GenerationOptions.Temperature = &temperature

	options := deriveTurnOptions(runtimeConfiguration)

	if options.ContextWindowTokens != 128000 {
		t.Fatalf("expected context window tokens to be wired, got %d", options.ContextWindowTokens)
	}
	if options.GenerationOptions.Seed == nil || *options.GenerationOptions.Seed != seed {
		t.Fatalf("expected generation seed to be wired, got %+v", options.GenerationOptions)
	}
	if options.GenerationOptions.Temperature == nil || *options.GenerationOptions.Temperature != temperature {
		t.Fatalf("expected generation temperature to be wired, got %+v", options.GenerationOptions)
	}
}

func TestAPinnedTaskLevelCarriesItsProductionBudget(t *testing.T) {
	xHighOptions := turnOptionsWithOverrides(deriveTurnOptions(config.RuntimeConfiguration{}), agentcontract.TurnOptions{TaskLevel: agentcontract.TaskLevelXHigh})
	xHighProfile := loop.TaskLevelProfileForLevel(agentcontract.TaskLevelXHigh)
	if xHighOptions.TaskLevel != xHighProfile.TaskLevel ||
		xHighOptions.MaxElapsedSecond != int(xHighProfile.Duration.Seconds()) {
		t.Fatalf("expected xhigh task budget, got %+v", xHighOptions)
	}
}
