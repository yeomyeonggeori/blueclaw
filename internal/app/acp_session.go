package app

import (
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/acpsession"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/intake"
)

func newACPSessionServer(inbound InboundOptions, kernel agentKernel, directory identityDirectory, taskLauncher *agentruntime.TaskLauncher, turnRouter intake.TurnRouter, connectorRuntime *connectors.ConnectorRuntime, taskRunService *task.TaskRunService, logger *slog.Logger) *acpsession.Server {
	socketPath := strings.TrimSpace(inbound.ACPSocketPath)
	if socketPath == "" {
		return nil
	}
	permissionRelay := acpsession.NewPermissionRelay(logger)
	kernel.toolCatalog.approvalGate.UsePermissionAsker(permissionRelay)
	return acpsession.NewServer(socketPath, acpsession.Collaborators{
		TaskLauncher:   taskLauncher,
		Directory:      directory.identityService,
		TurnRouter:     turnRouter,
		EngagementGate: connectorRuntime.EngagementGate(),
		TaskRunStore:   taskRunService,
	}, permissionRelay, logger)
}
