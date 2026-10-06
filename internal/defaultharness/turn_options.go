//go:build !nobundledharness

package defaultharness

import (
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/bluecollar/turnoptions"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

func turnOptionsOf(runtimeConfiguration config.RuntimeConfiguration) turnoptions.TurnOptions {
	recoveryBudget := runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget
	return turnoptions.TurnOptions{
		ContextWindowTokens: runtimeConfiguration.LanguageModel.ContextWindowTokens,
		GenerationOptions: model.GenerationOptions{
			Seed:        runtimeConfiguration.Agent.GenerationOptions.Seed,
			Temperature: runtimeConfiguration.Agent.GenerationOptions.Temperature,
		},
		RecoveryBudget: turnoptions.RecoveryBudget{
			CorrectedRetry: recoveryBudget.CorrectedRetry,
			AlternateRoute: recoveryBudget.AlternateRoute,
			AdjacentTool:   recoveryBudget.AdjacentTool,
			NoToolFallback: recoveryBudget.NoToolFallback,
		},
	}
}
