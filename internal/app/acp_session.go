package app

import (
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/acpsession"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func newACPSessionServer(inbound InboundOptions, kernel agentKernel, directory identityDirectory, taskLauncher *agentruntime.TaskLauncher, connectorRuntime *connectors.ConnectorRuntime, taskRunService *task.TaskRunService, logger *slog.Logger) *acpsession.Server {
	socketPath := strings.TrimSpace(inbound.ACPSocketPath)
	if socketPath == "" {
		return nil
	}
	permissionRelay := acpsession.NewPermissionRelay(logger)
	kernel.toolCatalog.approvalGate.UsePermissionAsker(approvalgate.AskerRoutedBy(permissionRelay, threadPermissionAskerFor(inbound, connectorRuntime)))
	return acpsession.NewServer(socketPath, acpsession.Collaborators{
		AnswerSettler:      kernel.toolCatalog.approvalGate,
		TaskLauncher:       taskLauncher,
		Directory:          directory.identityService,
		ReplyReader:        kernel.toolCatalog.replyReader,
		AttachmentImporter: connectorRuntime,
		SessionTurns:       connectorRuntime,
		TaskRunStore:       taskRunService,
	}, permissionRelay, logger)
}
