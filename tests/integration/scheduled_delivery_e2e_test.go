package integration

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/scheduler"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/loop"
)

func TestScheduledTaskRunsAndDeliversThroughConnectorOutbox(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	repository := &scheduledDeliveryRepository{schedules: []task.Schedule{{
		ScheduleID:       "schedule-daily-brief",
		CreatorPersonID:  "person-1",
		Name:             "daily brief",
		Prompt:           "look at my schedule and the industry news and tell me at 7am what to do today.",
		AgentProfileName: "default",
		Platform:         "fake",
		ConversationID:   "direct-1",
		ReplyTargetID:    "reply-target-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.ScheduleKindCron,
		CronExpression:   "0 7 * * *",
		NextRunAt:        &nextRunAt,
	}}}
	adapter := &scheduledDeliveryAdapter{}
	connectorRuntime := newScheduledDeliveryConnectorRuntime(staticScheduleLanguageModel{content: scheduleFinishMessage("Two things are worth handling first today.")}, adapter, repository)
	poller := newScheduledDeliveryPoller(staticScheduleLanguageModel{content: scheduleFinishMessage("Two things are worth handling first today.")}, repository)

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)
	if errorValue != nil {
		t.Fatalf("expected due schedule to run: %v", errorValue)
	}
	if runCount != 1 {
		t.Fatalf("expected one scheduled run, got %d", runCount)
	}
	if repository.succeeded == nil || repository.succeeded.LastTaskRunID == "" {
		t.Fatalf("expected schedule to persist success, got %+v", repository.succeeded)
	}
	if repository.succeeded.NextRunAt == nil || !repository.succeeded.NextRunAt.After(runAt) {
		t.Fatalf("expected schedule to advance, got %+v", repository.succeeded)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connectorRuntime.Start(ctx)
	waitForScheduledDelivery(t, adapter, 2*time.Second)
	cancel()

	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected exactly one outbound scheduled reply, got %+v", adapter.sentReplies)
	}
	if adapter.sentReplies[0].Message != "Two things are worth handling first today." {
		t.Fatalf("expected scheduled reply body, got %+v", adapter.sentReplies)
	}
	if len(repository.sentReplies) != 1 {
		t.Fatalf("expected one marked sent reply, got %+v", repository.sentReplies)
	}
}

func newScheduledDeliveryConnectorRuntime(languageModel staticScheduleLanguageModel, adapter *scheduledDeliveryAdapter, repository *scheduledDeliveryRepository) *connectors.ConnectorRuntime {
	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{"person@example.com": "person-1"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1", SecurityLevelRank: 100, GrantedClasses: []string{"internal"}},
		},
	})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := loop.NewAgentKernel(taskRunService, task.NewTaskStepService())
	useScheduleTestLanguageModel(agentKernel, languageModel)
	connectorRuntime := connectors.NewConnectorRuntime(identityService, agentKernel, taskRunService, taskEventService, nil)
	turnRouter := intake.NewTurnRouter(languageModel, intake.NewDecisionPlanner(intaketest.LanguageModelDecisionModel{LanguageModel: languageModel}, nil, nil), agentcontract.IntakeOptions{IsEnabled: true})
	launchFailureCompleter := launchfailure.NewCompleter(taskRunService, languageModel)
	connectorRuntime.UseTurnRouter(turnRouter)
	connectorRuntime.UseLaunchFailureCompleter(launchFailureCompleter)
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	taskLauncher := agentruntime.NewTaskLauncher(agentKernel, taskRunService, toolCatalogBuilder)
	taskLauncher.UseTurnRouter(turnRouter)
	taskLauncher.UseLaunchFailureCompleter(launchFailureCompleter)
	connectorRuntime.UseTaskLauncher(taskLauncher)
	connectorRuntime.RegisterAdapter(adapter)
	connectorRuntime.UseEventRepository(repository)
	return connectorRuntime
}

func newScheduledDeliveryPoller(languageModel staticScheduleLanguageModel, repository *scheduledDeliveryRepository) scheduler.SchedulePoller {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := loop.NewAgentKernel(taskRunService, task.NewTaskStepService())
	useScheduleTestLanguageModel(agentKernel, languageModel)
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory_search"},
	}, nil)
	taskLauncher := agentruntime.NewTaskLauncher(agentKernel, taskRunService, toolCatalogBuilder)
	taskLauncher.UseTurnRouter(intake.NewTurnRouter(languageModel, intake.NewDecisionPlanner(intaketest.LanguageModelDecisionModel{LanguageModel: languageModel}, nil, nil), agentcontract.IntakeOptions{IsEnabled: true}))
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, languageModel))
	return scheduler.SchedulePoller{
		ScheduleRepository:   repository,
		DeliveryRepository:   repository,
		ScheduleRunner:       agentruntime.NewScheduleRunner(taskLauncher),
		PersonAccessResolver: scheduledDeliveryAccessResolver{},
		WorkspaceID:          "workspace-1",
		WorkerID:             "test-worker",
	}
}

func waitForScheduledDelivery(t *testing.T, adapter *scheduledDeliveryAdapter, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(adapter.sentReplies) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type scheduledDeliveryRepository struct {
	mutex          sync.Mutex
	schedules      []task.Schedule
	succeeded      *task.Schedule
	failed         []string
	pendingReplies []connectors.QueuedConnectorReply
	sentReplies    []string
}

func (repository *scheduledDeliveryRepository) UpsertSchedule(schedule task.Schedule) error {
	repository.schedules = append(repository.schedules, schedule)
	return nil
}

func (repository *scheduledDeliveryRepository) ListSchedules(task.ScheduleListRequest) (task.ScheduleListResult, error) {
	return task.ScheduleListResult{}, nil
}

func (repository *scheduledDeliveryRepository) ClaimDueSchedules(limit int, _ time.Duration, referenceTime time.Time, _ string) ([]task.Schedule, error) {
	claimedSchedules := []task.Schedule{}
	remainingSchedules := []task.Schedule{}
	for _, schedule := range repository.schedules {
		if len(claimedSchedules) < limit && schedule.NextRunAt != nil && !schedule.NextRunAt.After(referenceTime) {
			claimedSchedules = append(claimedSchedules, schedule)
			continue
		}
		remainingSchedules = append(remainingSchedules, schedule)
	}
	repository.schedules = remainingSchedules
	return claimedSchedules, nil
}

func (repository *scheduledDeliveryRepository) MarkScheduleSucceeded(schedule task.Schedule) error {
	repository.succeeded = &schedule
	return nil
}

func (repository *scheduledDeliveryRepository) MarkScheduleFailed(_ task.Schedule, errorMessage string, _ time.Time) error {
	repository.failed = append(repository.failed, errorMessage)
	return nil
}

func (repository *scheduledDeliveryRepository) ExpireSchedule(task.Schedule, string, time.Time) error {
	return nil
}

func (repository *scheduledDeliveryRepository) CancelSchedules(task.ScheduleCancelRequest) (task.ScheduleCancelResult, error) {
	return task.ScheduleCancelResult{}, nil
}

func (repository *scheduledDeliveryRepository) UpdateSchedule(task.ScheduleUpdateRequest) (task.ScheduleUpdateResult, error) {
	return task.ScheduleUpdateResult{}, nil
}

func (repository *scheduledDeliveryRepository) EnqueueScheduledConnectorReply(schedule task.Schedule, taskRunID string, reply connectors.OutboundReply) (string, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if schedule.ReplyTargetID == "" {
		return "", errors.New("reply target is required")
	}
	rawEventID := "schedule:" + schedule.ScheduleID + ":task:" + taskRunID
	repository.pendingReplies = append(repository.pendingReplies, connectors.QueuedConnectorReply{
		OutboxID:    rawEventID,
		RawEventID:  rawEventID,
		Platform:    schedule.Platform,
		ReplyTarget: connectors.ReplyTarget{ConversationID: schedule.ConversationID, ReplyTargetID: schedule.ReplyTargetID, DedupeKey: rawEventID},
		Reply:       reply,
	})
	return rawEventID, nil
}

func (repository *scheduledDeliveryRepository) TryInsertConnectorEvent(connectors.PlatformInboundEvent) (bool, connectors.ConnectorRuntimeResult, error) {
	return false, connectors.ConnectorRuntimeResult{}, nil
}

func (repository *scheduledDeliveryRepository) SaveConnectorResult(connectors.PlatformInboundEvent, connectors.ConnectorRuntimeResult) error {
	return nil
}

func (repository *scheduledDeliveryRepository) TryEnqueueConnectorEvent(connectors.PlatformInboundEvent) (bool, connectors.ConnectorRuntimeResult, error) {
	return false, connectors.ConnectorRuntimeResult{}, nil
}

func (repository *scheduledDeliveryRepository) ClaimPendingConnectorEvents(int, time.Duration) ([]connectors.QueuedConnectorEvent, error) {
	return nil, nil
}

func (repository *scheduledDeliveryRepository) MarkConnectorEventSucceeded(connectors.PlatformInboundEvent, connectors.ConnectorRuntimeResult) error {
	return nil
}

func (repository *scheduledDeliveryRepository) MarkConnectorEventFailed(connectors.QueuedConnectorEvent, error, time.Time) error {
	return nil
}

func (repository *scheduledDeliveryRepository) EnqueueConnectorReply(event connectors.PlatformInboundEvent, replyTarget connectors.ReplyTarget, reply connectors.OutboundReply) (string, error) {
	outboxID := event.DedupeKey()
	repository.pendingReplies = append(repository.pendingReplies, connectors.QueuedConnectorReply{
		OutboxID:    outboxID,
		RawEventID:  outboxID,
		Platform:    event.Platform,
		ReplyTarget: replyTarget,
		Reply:       reply,
	})
	return outboxID, nil
}

func (repository *scheduledDeliveryRepository) ClaimPendingConnectorReplies(limit int, _ time.Duration) ([]connectors.QueuedConnectorReply, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if limit <= 0 || len(repository.pendingReplies) == 0 {
		return nil, nil
	}
	if limit > len(repository.pendingReplies) {
		limit = len(repository.pendingReplies)
	}
	claimedReplies := append([]connectors.QueuedConnectorReply{}, repository.pendingReplies[:limit]...)
	repository.pendingReplies = repository.pendingReplies[limit:]
	return claimedReplies, nil
}

func (repository *scheduledDeliveryRepository) MarkConnectorReplySent(_ connectors.QueuedConnectorReply, dispatchID string) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.sentReplies = append(repository.sentReplies, dispatchID)
	return nil
}

func (repository *scheduledDeliveryRepository) MarkConnectorReplyFailed(queuedReply connectors.QueuedConnectorReply, _ error, _ time.Time) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.pendingReplies = append(repository.pendingReplies, queuedReply)
	return nil
}

type scheduledDeliveryAdapter struct {
	sentReplies []connectors.OutboundReply
}

func (adapter *scheduledDeliveryAdapter) Name() string {
	return "fake"
}

func (adapter *scheduledDeliveryAdapter) ParseHTTPEvent(context.Context, *http.Request) (connectors.HTTPParseResult, error) {
	return connectors.HTTPParseResult{}, nil
}

func (adapter *scheduledDeliveryAdapter) ParseRealtimeEvent(context.Context, []byte, string) (connectors.PlatformInboundEvent, bool, error) {
	return connectors.PlatformInboundEvent{}, false, nil
}

func (adapter *scheduledDeliveryAdapter) ResolveIdentity(context.Context, string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{Platform: "fake", ExternalUserID: "user-1", Email: "person@example.com"}, nil
}

func (adapter *scheduledDeliveryAdapter) SendReply(_ context.Context, _ connectors.ReplyTarget, reply connectors.OutboundReply) (string, error) {
	adapter.sentReplies = append(adapter.sentReplies, reply)
	return "dispatch-" + strconv.Itoa(len(adapter.sentReplies)), nil
}

func (adapter *scheduledDeliveryAdapter) StartProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter *scheduledDeliveryAdapter) StopProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter *scheduledDeliveryAdapter) FetchHistory(context.Context, string, int) (connectors.VisibleContext, error) {
	return connectors.VisibleContext{}, nil
}

type scheduledDeliveryAccessResolver struct{}

func (scheduledDeliveryAccessResolver) ResolvePersonAccess(personID string) policy.PersonAccess {
	return policy.PersonAccess{PersonID: personID, SecurityLevelRank: 100, GrantedClasses: []string{"internal"}}
}
