package agentruntime

import (
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/intake/routedharness"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func routedTaskLauncher(harness agentcontract.Harness, taskRunService *task.TaskRunService, toolCatalogBuilder *ToolCatalogBuilder, routerLanguageModel model.LanguageModelProvider) *TaskLauncher {
	routed := routedharness.New(harness, taskRunService, routerLanguageModel, &intaketest.LanguageModelDecisionModel{LanguageModel: routerLanguageModel})
	return NewTaskLauncher(routed, taskRunService, toolCatalogBuilder)
}
