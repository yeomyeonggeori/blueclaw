package app

import (
	"log/slog"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/runtimecontrol"
	"github.com/yeomyeonggeori/blueclaw/internal/scheduler"
)

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
