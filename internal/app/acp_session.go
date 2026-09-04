package app

import (
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/acpsession"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/bluecollar/intake"
)

func newACPSessionServer(inbound InboundOptions, kernel agentKernel, directory identityDirectory, taskLauncher *agentruntime.TaskLauncher, turnRouter intake.TurnRouter, logger *slog.Logger) *acpsession.Server {
	socketPath := strings.TrimSpace(inbound.ACPSocketPath)
	if socketPath == "" {
		return nil
	}
	permissionRelay := acpsession.NewPermissionRelay(logger)
	kernel.toolCatalog.approvalGate.UsePermissionAsker(permissionRelay)
	return acpsession.NewServer(socketPath, taskLauncher, directory.identityService, permissionRelay, turnRouter, logger)
}
