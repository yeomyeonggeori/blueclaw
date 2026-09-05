package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/scheduler"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func (application *Application) Start() error {
	if application.startupError != nil {
		return application.startupError
	}
	if application.languageModelError != nil {
		application.runtimeLogger.Logger.Error("application.language_model_configuration_rejected", "error", application.languageModelError.Error())
		return application.serveWithoutStartingWork()
	}
	if errorValue := application.checkProtocolIdentity(); errorValue != nil {
		application.runtimeLogger.Logger.Error("application.protocol_identity_rejected", "error", errorValue.Error())
		return application.serveWithoutStartingWork()
	}
	if application.refreshSkillIndex != nil {
		go application.refreshSkillIndex(context.Background())
	}
	application.runtimeLogger.Logger.Info("application.starting", "stage", "log_retention")
	application.startLogRetentionLoop()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "memory_queue")
	application.startMemoryUpdateQueue()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "connector_runtime")
	application.startConnectorRuntime()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "connector_transports")
	application.startConnectorTransports()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "acp_session")
	if errorValue := application.startACPSessionServer(); errorValue != nil {
		return errorValue
	}
	application.runtimeLogger.Logger.Info("application.starting", "stage", "task_schedule")
	application.startTaskSchedulePoller()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "task_retention")
	application.startTaskRetentionSweeper()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "stale_tasks")
	application.startStaleTaskSweeper()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "listen")
	listener, errorValue := net.Listen("tcp", application.httpServer.Addr)
	if errorValue != nil {
		return errorValue
	}
	application.runtimeLogger.Logger.Info(
		"application.started",
		"listenAddress",
		application.httpServer.Addr,
		"connectorTransports",
		strings.Join(application.connectorTransportNames(), ","),
		"languageModelConfigured",
		application.languageModelConfigured,
		"logDirectoryPath",
		application.runtimeLogger.DirectoryPath(),
	)
	application.startInterruptedTaskAutoResume()
	return application.httpServer.Serve(listener)
}

// A contract this build does not share would have the agent call tool names
// the other side never registered, so no work may run. Exiting instead leaves
// the supervisor restarting every few seconds, which also takes down the one
// endpoint where the disagreement is visible to the host.
func (application *Application) serveWithoutStartingWork() error {
	listener, errorValue := net.Listen("tcp", application.httpServer.Addr)
	if errorValue != nil {
		return errorValue
	}
	application.runtimeLogger.Logger.Info(
		"application.serving_health_only",
		"listenAddress",
		application.httpServer.Addr,
	)
	return application.httpServer.Serve(listener)
}

// Handler exposes the built HTTP surface so a caller can exercise the runtime
// without binding a port.
func (application *Application) Handler() http.Handler {
	if application.httpServer == nil {
		return nil
	}
	return application.httpServer.Handler
}

func (application *Application) Shutdown(ctx context.Context) error {
	if application.connectorTransportCancel != nil {
		application.connectorTransportCancel()
	}
	if application.connectorRuntimeCancel != nil {
		application.connectorRuntimeCancel()
	}
	if application.acpSessionCancel != nil {
		application.acpSessionCancel()
	}
	if application.taskScheduleCancel != nil {
		application.taskScheduleCancel()
	}
	if application.taskRetentionCancel != nil {
		application.taskRetentionCancel()
	}
	if application.staleTaskCancel != nil {
		application.staleTaskCancel()
	}
	if application.interruptedTaskResumeCancel != nil {
		application.interruptedTaskResumeCancel()
	}
	if application.logRetentionCancel != nil {
		application.logRetentionCancel()
	}
	if application.memoryUpdateCancel != nil {
		application.memoryUpdateCancel()
	}
	errorValue := application.httpServer.Shutdown(ctx)
	backgroundError := application.awaitBackgroundLoops(ctx)
	terminalCloseError := application.closeTerminalSessions()
	closeErrorValue := application.runtimeLogger.Close()
	databaseCloseError := application.database.Close()
	if errorValue != nil {
		return errorValue
	}
	if backgroundError != nil {
		return backgroundError
	}
	if terminalCloseError != nil {
		return terminalCloseError
	}
	if closeErrorValue != nil {
		return closeErrorValue
	}
	return databaseCloseError
}

// Cancelling a context asks a goroutine to stop. Nothing was waiting for one to have
// stopped, so Shutdown closed the database while sweepers were still writing to it.
func (application *Application) startBackgroundLoop(run func(context.Context)) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	application.backgroundLoops.Add(1)
	go func() {
		defer application.backgroundLoops.Done()
		run(ctx)
	}()
	return cancel
}

func (application *Application) awaitBackgroundLoops(ctx context.Context) error {
	stopped := make(chan struct{})
	go func() {
		application.backgroundLoops.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		return errors.New("shutdown ran out of time before its background loops stopped")
	case <-time.After(backgroundLoopStopGrace):
		return errors.New("background loops did not stop within " + backgroundLoopStopGrace.String())
	}
}

func (application *Application) closeTerminalSessions() error {
	if application.terminalService == nil {
		return nil
	}
	return application.terminalService.CloseAllSessions()
}

func (application *Application) startConnectorRuntime() {
	if application.connectorRuntime == nil || application.connectorRuntimeCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	application.connectorRuntimeCancel = cancel
	application.connectorRuntime.Start(ctx)
}

func (application *Application) startACPSessionServer() error {
	if application.acpSessionServer == nil || application.acpSessionCancel != nil {
		return nil
	}
	if errorValue := application.acpSessionServer.Listen(); errorValue != nil {
		return errorValue
	}
	ctx, cancel := context.WithCancel(context.Background())
	application.acpSessionCancel = cancel
	go application.acpSessionServer.Serve(ctx)
	return nil
}

func (application *Application) startConnectorTransports() {
	if len(application.connectorTransports) == 0 || application.connectorTransportCancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	application.connectorTransportCancel = cancel
	for _, connectorTransport := range application.connectorTransports {
		transport := connectorTransport
		application.runtimeLogger.Logger.Info(
			"connector."+transport.Platform()+".transport.registered",
			"name",
			transport.Name(),
			"platform",
			transport.Platform(),
		)
		application.backgroundLoops.Add(1)
		go func() {
			defer application.backgroundLoops.Done()
			transport.Start(ctx)
		}()
	}
}

func (application *Application) connectorTransportNames() []string {
	transportNames := make([]string, 0, len(application.connectorTransports))
	for _, connectorTransport := range application.connectorTransports {
		transportNames = append(transportNames, connectorTransport.Platform()+":"+connectorTransport.Name())
	}
	return transportNames
}

func (application *Application) startLogRetentionLoop() {
	if application.runtimeLogger == nil || application.logRetentionCancel != nil {
		return
	}

	application.logRetentionCancel = application.startBackgroundLoop(application.runtimeLogger.StartRetentionLoop)
}

func (application *Application) startTaskSchedulePoller() {
	if application.taskSchedulePoller == nil || application.taskScheduleCancel != nil {
		return
	}
	interval := time.Duration(application.taskSchedulePollIntervalSecond()) * time.Second
	application.taskScheduleCancel = application.startBackgroundLoop(func(ctx context.Context) {
		application.taskSchedulePoller.Start(ctx, interval)
	})
}

func (application *Application) startTaskRetentionSweeper() {
	if application.taskRetentionSweeper == nil || application.taskRetentionCancel != nil {
		return
	}
	interval := time.Duration(application.taskRetentionIntervalMinuteOrDefault()) * time.Minute
	application.taskRetentionCancel = application.startBackgroundLoop(func(ctx context.Context) {
		application.taskRetentionSweeper.Start(ctx, interval)
	})
}

func (application *Application) startStaleTaskSweeper() {
	if application.taskRunService == nil || application.staleTaskCancel != nil {
		return
	}
	sweeper := scheduler.StaleTaskSweeper{
		TaskRunService: application.taskRunService,
		Notifier:       application.interruptedTaskResumer,
		Logger:         application.runtimeLogger.Logger,
	}
	application.staleTaskCancel = application.startBackgroundLoop(func(ctx context.Context) {
		sweeper.Start(ctx, 30*time.Minute)
	})
}

func (application *Application) startInterruptedTaskAutoResume() {
	if application.taskRunService == nil || application.interruptedTaskResumer == nil || application.interruptedTaskResumeCancel != nil {
		return
	}
	resumeStartedAt := time.Now()
	application.interruptedTaskResumeCancel = application.startBackgroundLoop(func(ctx context.Context) {
		application.resumeInterruptedTaskRuns(ctx, resumeStartedAt)
	})
}

func (application *Application) resumeInterruptedTaskRuns(ctx context.Context, now time.Time) {
	selection := application.taskRunService.SelectInterruptedTaskRunsForAutoResume(now, 5)
	for _, taskRun := range selection.SkippedTaskRuns {
		application.taskRunService.MarkInterruptedTaskRunAutoResumeSkipped(taskRun.TaskRunID, "per_boot_limit_exceeded")
	}
	for index, taskRun := range selection.SelectedTaskRuns {
		if ctx.Err() != nil {
			return
		}
		if index > 0 && !application.waitBeforeInterruptedTaskResume(ctx) {
			return
		}
		if !application.interruptedTaskResumer.CanResumeInterruptedTaskRun(taskRun) {
			application.taskRunService.MarkInterruptedTaskRunAutoResumeSkipped(taskRun.TaskRunID, "resume_context_unavailable")
			continue
		}
		if !application.taskRunService.ClaimInterruptedTaskRunAutoResume(taskRun.TaskRunID, "runtime_restart") {
			continue
		}
		if _, errorValue := application.interruptedTaskResumer.ResumeInterruptedTaskRun(ctx, taskRun); errorValue != nil {
			application.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventTaskAutoResumeLaunchFailed, errorValue.Error())
		}
	}
	application.failUnresumedInterruptedTaskRuns(ctx)
}

func (application *Application) failUnresumedInterruptedTaskRuns(ctx context.Context) {
	for _, taskRun := range application.taskRunService.ListTaskRun() {
		if ctx.Err() != nil {
			return
		}
		if !task.TaskRunWasInterruptedByRuntimeRestart(taskRun) {
			continue
		}
		application.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventTaskAutoResumeAbandoned, taskRun.FailureReason)
		application.interruptedTaskResumer.FailUnresumedInterruptedTaskRun(ctx, taskRun, "the task was interrupted by a runtime restart and could not be resumed")
	}
}

func (application *Application) waitBeforeInterruptedTaskResume(ctx context.Context) bool {
	delay := application.interruptedTaskResumeDelay
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (application *Application) taskRetentionIntervalMinuteOrDefault() int {
	if application.taskRetentionIntervalMinute > 0 {
		return application.taskRetentionIntervalMinute
	}
	return 60
}

func (application *Application) startMemoryUpdateQueue() {
	if application.memoryUpdateQueue == nil || application.memoryUpdateCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	application.memoryUpdateCancel = cancel
	application.memoryUpdateQueue.Start(ctx)
}

func (application *Application) taskSchedulePollIntervalSecond() int {
	if application.taskSchedulePollSecond > 0 {
		return application.taskSchedulePollSecond
	}
	return 30
}

func deriveListenAddress(baseURL string) string {
	if baseURL == "" {
		return "127.0.0.1:8080"
	}

	parsedURL, errorValue := url.Parse(baseURL)
	if errorValue != nil || parsedURL.Host == "" {
		return baseURL
	}

	return parsedURL.Host
}
