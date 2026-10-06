//go:build !nobundledharness

package agentruntime

import (
	"context"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/intake/routedharness"
	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type plannedTurnStub struct {
	agentcontract.Harness
}

func (stub plannedTurnStub) RunPlannedTurn(ctx context.Context, turnRequest agentcontract.AgentTurnRequest, _ turnclassification.Routing) (agentcontract.AgentTurnResult, error) {
	return stub.RunTurn(ctx, turnRequest)
}

func routedTaskLauncher(harness agentcontract.Harness, taskRunService *task.TaskRunService, toolCatalogBuilder *ToolCatalogBuilder, routerLanguageModel model.LanguageModelProvider) *TaskLauncher {
	runner, isPlannedRunner := harness.(routedharness.PlannedTurnRunner)
	if !isPlannedRunner {
		runner = plannedTurnStub{harness}
	}
	routed := routedharness.New(runner, taskRunService, routerLanguageModel, &intaketest.LanguageModelDecisionModel{LanguageModel: routerLanguageModel})
	return NewTaskLauncher(routed, taskRunService, toolCatalogBuilder)
}
