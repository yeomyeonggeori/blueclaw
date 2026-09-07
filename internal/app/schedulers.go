package app

import (
	"context"
	"log/slog"
	"net/url"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/runtimecontrol"
	"github.com/yeomyeonggeori/blueclaw/internal/scheduler"
)

func configureMorningBriefing(poller *scheduler.TaskSchedulePoller, configuration config.RuntimeConfiguration, directory identityDirectory, kernel agentKernel, logger *slog.Logger) {
	if poller == nil || directory.platformAccountLister == nil {
		return
	}
	repository, isSupported := poller.TaskScheduleRepository.(scheduler.MorningBriefingRepository)
	if !isSupported {
		return
	}
	client := newChatdClient(configuration)
	poller.MorningBriefing = &scheduler.MorningBriefing{
		Repository: repository, Accounts: directory.platformAccountLister,
		PolicyDocument:       directory.policyWatcher.CurrentPolicyDocument,
		PersonAccessResolver: directory.identityService,
		ActorFactory:         kernel.terminalService.WorkspaceActorFactory(),
		WorkspaceRootPath:    configuration.Terminal.WorkspaceRootPath, Logger: logger,
		OpenDirectMessage: func(ctx context.Context, platform string, externalUserID string) (string, string, error) {
			var response struct {
				ConversationID string `json:"conversationID"`
				ReplyTargetID  string `json:"replyTargetID"`
			}
			errorValue := client.PostJSON(ctx, "/v1/platform/"+url.PathEscape(platform)+"/dm.open", map[string]string{"externalUserID": externalUserID}, &response)
			return response.ConversationID, response.ReplyTargetID, errorValue
		},
	}
}

func newTaskSchedulePoller(runtimeConfiguration config.RuntimeConfiguration, services taskServices, identityService *identity.IdentityService, taskLauncher *agentruntime.TaskLauncher, taskIntakeController *runtimecontrol.TaskIntakeController, logger *slog.Logger) *scheduler.TaskSchedulePoller {
	if services.repositories.taskSchedule == nil || services.repositories.scheduledDelivery == nil {
		return nil
	}
	return &scheduler.TaskSchedulePoller{
		TaskScheduleRepository: services.repositories.taskSchedule,
		DeliveryRepository:     services.repositories.scheduledDelivery,
		TaskScheduleRunner:     agentruntime.NewTaskScheduleRunner(taskLauncher),
		TaskRunService:         services.taskRunService,
		PersonAccessResolver:   identityService,
		TaskIntakeGate:         taskIntakeController,
		WorkspaceID:            runtimeConfiguration.Memory.WorkspaceID,
		WorkerID:               "blueclaw-app",
		Logger:                 logger,
	}
}

func newTaskRetentionSweeper(runtimeConfiguration config.RuntimeConfiguration, services taskServices, logger *slog.Logger) *scheduler.TaskRetentionSweeper {
	return &scheduler.TaskRetentionSweeper{
		TaskRunService:      services.taskRunService,
		TaskEventService:    services.taskEventService,
		TaskStepService:     services.taskStepService,
		TaskArtifactService: services.taskArtifactService,
		Logger:              logger,
		RetentionDays:       runtimeConfiguration.Scheduler.TaskRetentionDays,
	}
}
