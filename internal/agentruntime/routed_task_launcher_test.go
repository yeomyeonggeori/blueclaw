package agentruntime

import (
	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func routedTaskLauncher(harness agentcontract.Harness, taskRunService *task.TaskRunService, toolCatalogBuilder *ToolCatalogBuilder, routerLanguageModel model.LanguageModelProvider) *TaskLauncher {
	return routedTaskLauncherAuthoringNoticesWith(harness, taskRunService, toolCatalogBuilder, routerLanguageModel, routerLanguageModel)
}

func routedTaskLauncherAuthoringNoticesWith(harness agentcontract.Harness, taskRunService *task.TaskRunService, toolCatalogBuilder *ToolCatalogBuilder, routerLanguageModel model.LanguageModelProvider, noticeLanguageModel model.LanguageModelProvider) *TaskLauncher {
	taskLauncher := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)
	taskLauncher.UseTurnRouter(intake.NewTurnRouter(routerLanguageModel, intake.NewDecisionPlanner(intaketest.LanguageModelDecisionModel{LanguageModel: routerLanguageModel}, nil, nil), agentcontract.IntakeOptions{IsEnabled: true}))
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, noticeLanguageModel))
	return taskLauncher
}
