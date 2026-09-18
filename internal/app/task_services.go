package app

import (
	"log/slog"

	"github.com/yeomyeonggeori/blueclaw/internal/adminapi"
	"github.com/yeomyeonggeori/blueclaw/internal/auth"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/scheduler"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type taskServices struct {
	auditHandler        *adminapi.AuditHandler
	taskEventService    *task.TaskEventService
	taskStepService     *task.TaskStepService
	taskArtifactService *task.TaskArtifactService
	taskRunService      *task.TaskRunService
	taskAuthService     *task.TaskAuthService
	repositories        taskRepositories
}

type taskRepositories struct {
	person                       postgres.PersonRepository
	personReferenceCanonicalizer adminapi.PersonReferenceCanonicalizer
	schedule                     task.ScheduleRepository
	scheduleSummary              adminapi.ScheduleSummaryRepository
	scheduleList                 adminapi.ScheduleListRepository
	scheduleCreatorRepair        adminapi.ScheduleCreatorRepairRepository
	connectorEventDiagnostic     adminapi.ConnectorEventDiagnosticRepository
	conversationReset            adminapi.ConversationResetRepository
	taskWaitToken                task.TaskWaitTokenRepository
	scheduledDelivery            scheduler.ScheduleDeliveryRepository
}

func newTaskServices(runtimeConfiguration config.RuntimeConfiguration, database postgres.Database, companyProvider func() agentcontract.CompanyContext, logger *slog.Logger) taskServices {
	services := taskServices{
		auditHandler:        adminapi.NewAuditHandler(),
		taskEventService:    task.NewTaskEventService(),
		taskStepService:     task.NewTaskStepService(),
		taskArtifactService: task.NewTaskArtifactService(),
	}
	services.taskRunService = task.NewTaskRunService(services.taskEventService)
	taskTemporaryDirectoryReclaimer := task.NewTaskTemporaryDirectoryReclaimer(runtimeConfiguration.Terminal.WorkspaceRootPath, logger)
	services.taskRunService.RegisterTaskRunTransitionObserver(taskTemporaryDirectoryReclaimer.Observe)
	services.repositories = newTaskRepositories(database, services, companyProvider, logger)
	magicLinkService := auth.NewMagicLinkService()
	sessionService := auth.NewSessionService()
	services.taskAuthService = task.NewTaskAuthService(magicLinkService, sessionService, services.taskRunService)
	return services
}

func newTaskRepositories(database postgres.Database, services taskServices, companyProvider func() agentcontract.CompanyContext, logger *slog.Logger) taskRepositories {
	if database.SQL == nil {
		return taskRepositories{}
	}
	personRepository := postgres.NewPersonRepository(database)
	services.taskEventService.UseRepository(postgres.NewTaskEventRepository(database))
	services.taskStepService.UseRepository(postgres.NewTaskStepRepository(database))
	services.taskArtifactService.UseRepository(postgres.NewTaskArtifactRepository(database))
	services.taskRunService.UseRepository(postgres.NewTaskRunRepository(database))
	services.taskRunService.InterruptOrphanedRuntimeTaskRuns(task.TaskInterruptReasonRuntimeRestart)
	scheduleRepository := postgres.NewScheduleRepository(database)
	task.SweepEmptyScheduleTimeZone(scheduleRepository, companyProvider().TimeZone, logger)
	return taskRepositories{
		person:                       personRepository,
		personReferenceCanonicalizer: personRepository,
		schedule:                     scheduleRepository,
		scheduleSummary:              scheduleRepository,
		scheduleList:                 scheduleRepository,
		scheduleCreatorRepair:        scheduleRepository,
		connectorEventDiagnostic:     postgres.NewRawEventRepository(database),
		conversationReset:            postgres.NewConversationResetRepository(database),
		taskWaitToken:                postgres.NewTaskWaitTokenRepository(database),
		scheduledDelivery:            postgres.NewRawEventRepository(database),
	}
}
