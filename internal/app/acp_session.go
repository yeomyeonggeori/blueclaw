package app

import (
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/acpsession"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/intake"
)

func newACPSessionServer(inbound InboundOptions, kernel agentKernel, directory identityDirectory, taskLauncher *agentruntime.TaskLauncher, decisionPlanner intake.DecisionPlanner, connectorRuntime *connectors.ConnectorRuntime, taskRunService *task.TaskRunService, llmCalls *postgres.LLMCallRepository, logger *slog.Logger) *acpsession.Server {
	socketPath := strings.TrimSpace(inbound.ACPSocketPath)
	if socketPath == "" {
		return nil
	}
	permissionRelay := acpsession.NewPermissionRelay(logger)
	kernel.toolCatalog.approvalGate.UsePermissionAsker(permissionRelay)
	return acpsession.NewServer(socketPath, acpsession.Collaborators{
		ApprovalDeferrer:   kernel.toolCatalog.approvalGate,
		TaskLauncher:       taskLauncher,
		Directory:          directory.identityService,
		ReplyReader:        kernel.toolCatalog.replyReader,
		IntakeDecider:      decisionPlanner,
		AttachmentImporter: connectorRuntime,
		SessionTurns:       connectorRuntime,
		TaskRunStore:       taskRunService,
		TasklessCalls:      acpsession.TasklessCallRecorder(newTasklessLLMCallRecorder(llmCalls, logger)),
	}, permissionRelay, logger)
}
