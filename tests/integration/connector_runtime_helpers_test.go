package integration

import (
	"context"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/loop"
)

type integrationLanguageModel struct{}

func (languageModel integrationLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "ok", nil
}

func (languageModel integrationLanguageModel) GenerateStructuredResponse(context.Context, llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	return llm.StructuredResponse{Content: `{"reply":"ok"}`}, nil
}

func newIntegrationConnectorRuntime(identityService *identity.IdentityService) *connectors.ConnectorRuntime {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := loop.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(integrationLanguageModel{})

	connectorRuntime := connectors.NewConnectorRuntime(identityService, agentKernel, taskRunService, taskEventService, nil)
	return connectorRuntime
}
