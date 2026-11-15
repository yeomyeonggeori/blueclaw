package app

import (
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/acpsession"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/runtimecontrol"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func newACPSessionServer(inbound InboundOptions, kernel agentKernel, directory identityDirectory, taskLauncher *agentruntime.TaskLauncher, connectorRuntime *connectors.ConnectorRuntime, taskRunService *task.TaskRunService, trustedClock *runtimecontrol.TrustedClock, logger *slog.Logger) *acpsession.Server {
	socketPath := strings.TrimSpace(inbound.ACPSocketPath)
	if socketPath == "" {
		return nil
	}
	sessionRoutes := acpsession.NewSessionRoutes(logger)
	kernel.toolCatalog.approvalGate.UseQuestionPoster(approvalgate.PosterRoutedBy(sessionRoutes, threadQuestionPosterFor(inbound, connectorRuntime)))
	connectorRuntime.UseHandoffCloser(sessionRoutes)
	return acpsession.NewServer(socketPath, acpsession.Collaborators{
		TaskLauncher: taskLauncher,
		Directory:    directory.identityService,
		SessionTurns: connectorRuntime,
		TaskRunStore: taskRunService,
		Clock:        trustedClock,
	}, sessionRoutes, logger)
}
