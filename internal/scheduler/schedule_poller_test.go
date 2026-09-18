package scheduler

import (
	"bytes"
	"context"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

func TestScheduledTaskReplyPreservesModelWording(t *testing.T) {
	turnResult := agentcontract.AgentTurnResult{
		TaskRun:       task.TaskRun{TaskRunID: "task-1", Status: task.TaskStatusCompleted},
		FinishMessage: "완료했습니다: sandbox:/mnt/data/report.pdf",
		Attachments:   []toolcontract.FileAttachment{{Filename: "report.pdf", DevicePath: "/workspace/private/people/p1/artifacts/report.pdf"}},
	}

	reply, errorValue := scheduledTaskReply(agentruntime.ScheduleRunResult{
		LaunchResult: agentruntime.TaskLaunchResult{TurnResult: turnResult},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if reply.Message != turnResult.FinishMessage || len(reply.Attachments) != 1 {
		t.Fatalf("expected model wording and typed attachments to pass through, got %+v", reply)
	}
}

func TestSchedulePollerRunsDueScheduleAndEnqueuesReply(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	repository := &pollerScheduleRepository{schedules: []task.Schedule{{
		ScheduleID:       "schedule-1",
		CreatorPersonID:  "person-1",
		Name:             "daily research",
		Prompt:           "매일 업계 뉴스를 조사해서 알려줘.",
		AgentProfileName: "default",
		Platform:         "mattermost",
		ConversationID:   "channel-1",
		ReplyTargetID:    "reply-target-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.ScheduleKindCron,
		CronExpression:   "0 7 * * *",
		NextRunAt:        &nextRunAt,
	}}}
	deliveryRepository := &pollerDeliveryRepository{}
	poller := SchedulePoller{
		ScheduleRepository:   repository,
		DeliveryRepository:   deliveryRepository,
		ScheduleRunner:       testScheduleRunner(task.TaskStatusCompleted, "오늘의 조사 결과입니다."),
		PersonAccessResolver: staticPersonAccessResolver{},
		WorkspaceID:          "workspace-1",
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 1 {
		t.Fatalf("expected one run, got %d", runCount)
	}
	if len(deliveryRepository.replies) != 1 || deliveryRepository.replies[0].Message != "오늘의 조사 결과입니다." {
		t.Fatalf("expected scheduled reply delivery, got %+v", deliveryRepository.replies)
	}
	if deliveryRepository.replies[0].TaskRunID == "" || deliveryRepository.replies[0].ReplyKind != "success" {
		t.Fatalf("expected scheduled reply metadata, got %+v", deliveryRepository.replies[0])
	}
	if repository.succeeded == nil || repository.succeeded.LastTaskRunID == "" {
		t.Fatalf("expected schedule success with task run id, got %+v", repository.succeeded)
	}
	if repository.succeeded.NextRunAt == nil || !repository.succeeded.NextRunAt.After(runAt) {
		t.Fatalf("expected schedule to advance, got %+v", repository.succeeded.NextRunAt)
	}
}

func TestSchedulePollerDoesNotClaimDueScheduleWhenQuiesced(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	claimCount := 0
	repository := &pollerScheduleRepository{
		schedules: []task.Schedule{{
			ScheduleID:       "schedule-1",
			CreatorPersonID:  "person-1",
			Prompt:           "매일 업계 뉴스를 조사해서 알려줘.",
			AgentProfileName: "default",
			Platform:         "mattermost",
			ConversationID:   "channel-1",
			ReplyTargetID:    "reply-target-1",
			TimeZone:         "Asia/Seoul",
			Kind:             task.ScheduleKindInterval,
			IntervalSecond:   60,
			NextRunAt:        &nextRunAt,
		}},
		claimCallback: func() {
			claimCount++
		},
	}
	poller := SchedulePoller{
		ScheduleRepository: repository,
		DeliveryRepository: &pollerDeliveryRepository{},
		ScheduleRunner:     testScheduleRunner(task.TaskStatusCompleted, "오늘의 조사 결과입니다."),
		TaskIntakeGate:     pollerTaskIntakeGate{isQuiesced: true},
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 0 {
		t.Fatalf("expected no runs, got %d", runCount)
	}
	if claimCount != 0 {
		t.Fatalf("quiesced scheduler must not claim schedules, got %d claims", claimCount)
	}
	if repository.succeeded != nil || len(repository.failed) != 0 {
		t.Fatalf("quiesced scheduler must not advance schedules, succeeded=%+v failed=%+v", repository.succeeded, repository.failed)
	}
}

func TestSchedulePollerDeliversMessageScheduleWithoutAgentRun(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	repository := &pollerScheduleRepository{schedules: []task.Schedule{{
		ScheduleID:      "schedule-1",
		CreatorPersonID: "person-1",
		Name:            "apology repeat",
		Prompt:          "죄송합니다.",
		ExecutionMode:   task.ScheduleExecutionModeMessage,
		Platform:        "mattermost",
		ConversationID:  "channel-1",
		ReplyTargetID:   "reply-target-1",
		TimeZone:        "Asia/Seoul",
		Kind:            task.ScheduleKindInterval,
		IntervalSecond:  60,
		MaxRunCount:     1,
		NextRunAt:       &nextRunAt,
	}}}
	deliveryRepository := &pollerDeliveryRepository{}
	poller := SchedulePoller{
		ScheduleRepository: repository,
		DeliveryRepository: deliveryRepository,
		TaskRunService:     task.NewTaskRunService(task.NewTaskEventService()),
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 1 {
		t.Fatalf("expected one run, got %d", runCount)
	}
	if len(deliveryRepository.replies) != 1 || deliveryRepository.replies[0].Message != "죄송합니다." {
		t.Fatalf("expected direct scheduled message delivery, got %+v", deliveryRepository.replies)
	}
	if deliveryRepository.replies[0].TaskRunID == "" || deliveryRepository.replies[0].ReplyKind != "success" {
		t.Fatalf("expected direct scheduled reply metadata, got %+v", deliveryRepository.replies[0])
	}
	if repository.succeeded == nil || repository.succeeded.LastTaskRunID == "" {
		t.Fatalf("expected audited schedule success, got %+v", repository.succeeded)
	}
	if repository.succeeded.NextRunAt != nil || repository.succeeded.CompletedRunCount != 1 {
		t.Fatalf("expected limited message schedule to complete, got %+v", repository.succeeded)
	}
}

func TestSchedulePollerDoesNotAdvanceWhenDeliveryFails(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	repository := &pollerScheduleRepository{schedules: []task.Schedule{{
		ScheduleID:       "schedule-1",
		CreatorPersonID:  "person-1",
		Prompt:           "매일 업계 뉴스를 조사해서 알려줘.",
		AgentProfileName: "default",
		Platform:         "mattermost",
		ConversationID:   "channel-1",
		ReplyTargetID:    "reply-target-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.ScheduleKindInterval,
		IntervalSecond:   60,
		MaxRunCount:      10,
		NextRunAt:        &nextRunAt,
	}}}
	scheduleRunner, harness, _ := scheduleRunnerWithHarness(task.TaskStatusCompleted, "오늘의 조사 결과입니다.")
	poller := SchedulePoller{
		ScheduleRepository:   repository,
		DeliveryRepository:   &pollerDeliveryRepository{errorValue: errors.New("outbox unavailable")},
		ScheduleRunner:       scheduleRunner,
		PersonAccessResolver: staticPersonAccessResolver{},
	}

	errorValue := poller.runSchedule(context.Background(), repository.schedules[0], runAt)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "outbox unavailable") {
		t.Fatalf("expected delivery error to surface, got %v", errorValue)
	}
	if repository.succeeded != nil {
		t.Fatalf("expected delivery failure not to advance schedule, got %+v", repository.succeeded)
	}
	if len(repository.failed) != 0 {
		t.Fatalf("expected direct run not to record poller failure, got %+v", repository.failed)
	}
	if harness.RunTurnCallCount() == 0 {
		t.Fatal("expected the task executor to run")
	}
}

func TestSchedulePollerAtomicSuccessAdvancesOnceAndEnqueuesOnce(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	repository := &pollerAtomicScheduleRepository{
		pollerScheduleRepository: &pollerScheduleRepository{schedules: []task.Schedule{{
			ScheduleID:       "schedule-1",
			CreatorPersonID:  "person-1",
			Prompt:           "매일 업계 뉴스를 조사해서 알려줘.",
			AgentProfileName: "default",
			Platform:         "mattermost",
			ConversationID:   "channel-1",
			ReplyTargetID:    "reply-target-1",
			TimeZone:         "Asia/Seoul",
			Kind:             task.ScheduleKindInterval,
			IntervalSecond:   60,
			MaxRunCount:      10,
			NextRunAt:        &nextRunAt,
		}}},
	}
	poller := SchedulePoller{
		ScheduleRepository:   repository,
		ScheduleRunner:       testScheduleRunner(task.TaskStatusCompleted, "오늘의 조사 결과입니다."),
		PersonAccessResolver: staticPersonAccessResolver{},
	}

	errorValue := poller.runSchedule(context.Background(), repository.schedules[0], runAt)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if repository.succeeded == nil || repository.succeeded.CompletedRunCount != 1 {
		t.Fatalf("expected schedule to advance once, got %+v", repository.succeeded)
	}
	if repository.succeeded.NextRunAt == nil || !repository.succeeded.NextRunAt.Equal(runAt.Add(time.Minute)) {
		t.Fatalf("expected next run to advance by one minute, got %+v", repository.succeeded.NextRunAt)
	}
	if len(repository.deliveryDeduplicationKeys) != 1 {
		t.Fatalf("expected one delivery enqueue, got %+v", repository.deliveryDeduplicationKeys)
	}
	expectedDeliveryDeduplicationKey := "schedule:schedule-1:occurrence:" + runAt.Format(time.RFC3339Nano)
	if repository.deliveryDeduplicationKeys[0] != expectedDeliveryDeduplicationKey {
		t.Fatalf("expected occurrence deduplication key %q, got %+v", expectedDeliveryDeduplicationKey, repository.deliveryDeduplicationKeys)
	}
}

func TestSchedulePollerRetriedOccurrenceDoesNotDoubleEnqueue(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	nextRunAt := runAt
	repository := &pollerAtomicScheduleRepository{
		pollerScheduleRepository: &pollerScheduleRepository{schedules: []task.Schedule{{
			ScheduleID:       "schedule-1",
			CreatorPersonID:  "person-1",
			Prompt:           "매일 업계 뉴스를 조사해서 알려줘.",
			AgentProfileName: "default",
			Platform:         "mattermost",
			ConversationID:   "channel-1",
			ReplyTargetID:    "reply-target-1",
			TimeZone:         "Asia/Seoul",
			Kind:             task.ScheduleKindInterval,
			IntervalSecond:   60,
			MaxRunCount:      10,
			NextRunAt:        &nextRunAt,
		}}},
	}
	poller := SchedulePoller{
		ScheduleRepository:   repository,
		ScheduleRunner:       testScheduleRunner(task.TaskStatusCompleted, "오늘의 조사 결과입니다."),
		PersonAccessResolver: staticPersonAccessResolver{},
	}
	claimedSchedule := repository.schedules[0]

	firstError := poller.runSchedule(context.Background(), claimedSchedule, runAt)
	secondError := poller.runSchedule(context.Background(), claimedSchedule, runAt)

	if firstError != nil || secondError != nil {
		t.Fatalf("expected retry to be idempotent, first=%v second=%v", firstError, secondError)
	}
	if len(repository.deliveryDeduplicationKeys) != 1 {
		t.Fatalf("expected retried occurrence not to double-enqueue, got %+v", repository.deliveryDeduplicationKeys)
	}
}

func TestSchedulePollerDoesNotRunExpiredSchedule(t *testing.T) {
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	expiredAt := runAt.Add(-time.Minute)
	repository := &pollerScheduleRepository{schedules: []task.Schedule{{
		ScheduleID:      "schedule-expired",
		CreatorPersonID: "person-1",
		Prompt:          "만료된 알림",
		ExecutionMode:   task.ScheduleExecutionModeMessage,
		Platform:        "mattermost",
		ConversationID:  "channel-1",
		ReplyTargetID:   "reply-target-1",
		TimeZone:        "Asia/Seoul",
		Kind:            task.ScheduleKindInterval,
		IntervalSecond:  60,
		NextRunAt:       &runAt,
		ExpiresAt:       &expiredAt,
	}}}
	deliveryRepository := &pollerDeliveryRepository{}
	poller := SchedulePoller{
		ScheduleRepository: repository,
		DeliveryRepository: deliveryRepository,
		TaskRunService:     task.NewTaskRunService(task.NewTaskEventService()),
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 0 || len(deliveryRepository.replies) != 0 {
		t.Fatalf("expected expired schedule not to run, count=%d replies=%+v", runCount, deliveryRepository.replies)
	}
}

func TestSchedulePollerLogsClaimErrors(t *testing.T) {
	var logBuffer bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	poller := SchedulePoller{
		ScheduleRepository: &pollerScheduleRepository{
			claimError:    errors.New("database unavailable"),
			claimCallback: cancel,
		},
		Logger: slog.New(slog.NewTextHandler(&logBuffer, nil)),
	}

	poller.Start(ctx, time.Nanosecond)

	logDocument := logBuffer.String()
	if !strings.Contains(logDocument, "task_schedule.poller.failed") || !strings.Contains(logDocument, "database unavailable") {
		t.Fatalf("expected poller claim error log, got %q", logDocument)
	}
}

func TestSchedulePollerLogsDeliveryFailures(t *testing.T) {
	var logBuffer bytes.Buffer
	runAt := time.Date(2026, 5, 6, 7, 0, 0, 0, time.UTC)
	repository := &pollerScheduleRepository{schedules: []task.Schedule{{
		ScheduleID:       "schedule-1",
		CreatorPersonID:  "person-1",
		Prompt:           "매일 업계 뉴스를 조사해서 알려줘.",
		AgentProfileName: "default",
		Platform:         "mattermost",
		ConversationID:   "channel-1",
		ReplyTargetID:    "reply-target-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.ScheduleKindCron,
		CronExpression:   "0 7 * * *",
		NextRunAt:        &runAt,
	}}}
	poller := SchedulePoller{
		ScheduleRepository: repository,
		DeliveryRepository: &pollerDeliveryRepository{errorValue: errors.New("outbox unavailable")},
		ScheduleRunner:     testScheduleRunner(task.TaskStatusCompleted, "오늘의 조사 결과입니다."),
		Logger:             slog.New(slog.NewTextHandler(&logBuffer, nil)),
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 0 {
		t.Fatalf("expected delivery failure not to count as completed run, got %d", runCount)
	}
	if repository.succeeded != nil {
		t.Fatalf("expected delivery failure not to advance schedule, got %+v", repository.succeeded)
	}
	if len(repository.failed) != 1 || !strings.Contains(repository.failed[0], "outbox unavailable") {
		t.Fatalf("expected delivery failure to mark schedule failed, got %+v", repository.failed)
	}
	logDocument := logBuffer.String()
	if !strings.Contains(logDocument, "task_schedule.reply.enqueue_failed") || !strings.Contains(logDocument, "schedule-1") || !strings.Contains(logDocument, "outbox unavailable") {
		t.Fatalf("expected delivery failure log, got %q", logDocument)
	}
}

func TestSchedulePollerRejectsScheduledInteractionWithoutWaiting(t *testing.T) {
	repository := &pollerScheduleRepository{schedules: []task.Schedule{waitingSchedule(time.Now().UTC())}}
	deliveryRepository := &pollerDeliveryRepository{}
	scheduleRunner, _, taskRunService := scheduleRunnerWithHarness(task.TaskStatusBlocked, "확인이 필요해요.")
	poller := SchedulePoller{
		ScheduleRepository:   repository,
		DeliveryRepository:   deliveryRepository,
		ScheduleRunner:       scheduleRunner,
		TaskRunService:       taskRunService,
		PersonAccessResolver: staticPersonAccessResolver{},
	}

	_, errorValue := poller.RunDue(context.Background(), *repository.schedules[0].NextRunAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(deliveryRepository.replies) != 0 {
		t.Fatalf("expected no waiting task reply, got %+v", deliveryRepository.replies)
	}
	if repository.succeeded != nil {
		t.Fatalf("expected interactive scheduled task not to advance, got %+v", repository.succeeded)
	}
	if len(repository.failed) != 0 {
		t.Fatalf("expected scheduled interaction not to retry, got failed=%+v", repository.failed)
	}
	if len(repository.expired) != 1 {
		t.Fatalf("expected the schedule to expire after a blocked run, got %+v", repository.expired)
	}
	taskRuns := taskRunService.ListTaskRun()
	if len(taskRuns) != 1 || taskRuns[0].Status != task.TaskStatusFailed {
		t.Fatalf("expected blocked scheduled task run to be failed, got %+v", taskRuns)
	}
}

func TestSchedulePollerSkipsActiveRunForSameSchedule(t *testing.T) {
	runAt := time.Now().UTC()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "schedule:schedule-waiting", "already running")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	repository := &pollerScheduleRepository{schedules: []task.Schedule{waitingSchedule(runAt)}}
	poller := SchedulePoller{
		ScheduleRepository:   repository,
		DeliveryRepository:   &pollerDeliveryRepository{},
		ScheduleRunner:       testScheduleRunner(task.TaskStatusCompleted, "should not run"),
		TaskRunService:       taskRunService,
		PersonAccessResolver: staticPersonAccessResolver{},
	}

	runCount, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runCount != 0 {
		t.Fatalf("expected active schedule run to be skipped, got %d", runCount)
	}
	if repository.succeeded != nil || len(repository.failed) != 0 {
		t.Fatalf("expected no schedule state change, succeeded=%+v failed=%+v", repository.succeeded, repository.failed)
	}
}

func TestSchedulePollerDoesNotDeliverFailedTaskReply(t *testing.T) {
	repository := &pollerScheduleRepository{schedules: []task.Schedule{waitingSchedule(time.Now().UTC())}}
	deliveryRepository := &pollerDeliveryRepository{}
	poller := SchedulePoller{
		ScheduleRepository:   repository,
		DeliveryRepository:   deliveryRepository,
		ScheduleRunner:       testScheduleRunner(task.TaskStatusFailed, "calendar unavailable"),
		PersonAccessResolver: staticPersonAccessResolver{},
	}

	_, errorValue := poller.RunDue(context.Background(), *repository.schedules[0].NextRunAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(deliveryRepository.replies) != 0 {
		t.Fatalf("expected no failed task reply, got %+v", deliveryRepository.replies)
	}
	if len(repository.failed) != 1 || !strings.Contains(repository.failed[0], "failed") {
		t.Fatalf("expected failed task status to retry with backoff, got expired=%+v failed=%+v", repository.expired, repository.failed)
	}
	if len(repository.expired) != 0 {
		t.Fatalf("expected transient task failure not to expire the schedule, got %+v", repository.expired)
	}
}

func TestSchedulePollerExpiresOneTimeMessageWithInvalidDeliveryTarget(t *testing.T) {
	runAt := time.Now().UTC().Add(-time.Minute)
	repository := &pollerScheduleRepository{schedules: []task.Schedule{{
		ScheduleID:       "schedule-once",
		CreatorPersonID:  "person-1",
		Prompt:           "알림입니다.",
		ExecutionMode:    task.ScheduleExecutionModeMessage,
		AgentProfileName: "default",
		Platform:         "mattermost",
		ConversationID:   "channel-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.ScheduleKindOnce,
		RunAt:            &runAt,
		NextRunAt:        &runAt,
	}}}
	poller := SchedulePoller{
		ScheduleRepository:   repository,
		DeliveryRepository:   &pollerDeliveryRepository{},
		TaskRunService:       task.NewTaskRunService(task.NewTaskEventService()),
		PersonAccessResolver: staticPersonAccessResolver{},
	}

	_, errorValue := poller.RunDue(context.Background(), runAt, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(repository.expired) != 1 || repository.expired[0].NextRunAt != nil {
		t.Fatalf("expected invalid one-time message schedule to expire, got %+v", repository.expired)
	}
	if len(repository.failed) != 0 {
		t.Fatalf("expected invalid one-time message schedule not to retry, got %+v", repository.failed)
	}
}

func TestSchedulePollerCancelsStaleScheduledTaskRuns(t *testing.T) {
	referenceTime := time.Now().UTC().Add(time.Hour)
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "schedule:schedule-1", "stale schedule")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	poller := SchedulePoller{
		ScheduleRepository:  &pollerScheduleRepository{},
		TaskRunService:      taskRunService,
		StaleTaskRunTimeout: time.Minute,
	}

	_, errorValue := poller.RunDue(context.Background(), referenceTime, 1)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	cancelledTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected stale scheduled task run to be cancelled, got found=%v run=%+v", isFound, cancelledTaskRun)
	}
}

type pollerScheduleRepository struct {
	schedules     []task.Schedule
	succeeded     *task.Schedule
	failed        []string
	expired       []task.Schedule
	claimError    error
	claimCallback func()
}

func (repository *pollerScheduleRepository) UpsertSchedule(schedule task.Schedule) error {
	repository.schedules = append(repository.schedules, schedule)
	return nil
}

func (repository *pollerScheduleRepository) UpdateSchedule(request task.ScheduleUpdateRequest) (task.ScheduleUpdateResult, error) {
	for index, schedule := range repository.schedules {
		if schedule.ScheduleID != request.ScheduleID || schedule.CreatorPersonID != request.RequesterPersonID || schedule.NextRunAt == nil {
			continue
		}
		updatedSchedule := schedule
		var errorValue error
		if request.UpdateSchedule != nil {
			updatedSchedule, errorValue = request.UpdateSchedule(schedule)
			if errorValue != nil {
				return task.ScheduleUpdateResult{}, errorValue
			}
		}
		repository.schedules[index] = updatedSchedule
		return task.ScheduleUpdateResult{Schedule: updatedSchedule, IsFound: true}, nil
	}
	return task.ScheduleUpdateResult{}, nil
}

func (repository *pollerScheduleRepository) ClaimDueSchedules(limit int, _ time.Duration, referenceTime time.Time, _ string) ([]task.Schedule, error) {
	if repository.claimCallback != nil {
		repository.claimCallback()
	}
	if repository.claimError != nil {
		return nil, repository.claimError
	}
	dueSchedules := []task.Schedule{}
	for _, schedule := range repository.schedules {
		if (task.Scheduler{}).IsScheduleDue(schedule, referenceTime) {
			dueSchedules = append(dueSchedules, schedule)
		}
	}
	if limit <= 0 || limit > len(dueSchedules) {
		limit = len(dueSchedules)
	}
	return append([]task.Schedule{}, dueSchedules[:limit]...), nil
}

func (repository *pollerScheduleRepository) ListSchedules(task.ScheduleListRequest) (task.ScheduleListResult, error) {
	return task.ScheduleListResult{}, nil
}

func (repository *pollerScheduleRepository) MarkScheduleSucceeded(schedule task.Schedule) error {
	repository.succeeded = &schedule
	for index, existingSchedule := range repository.schedules {
		if existingSchedule.ScheduleID == schedule.ScheduleID {
			repository.schedules[index] = schedule
			break
		}
	}
	return nil
}

func (repository *pollerScheduleRepository) MarkScheduleFailed(_ task.Schedule, errorMessage string, _ time.Time) error {
	repository.failed = append(repository.failed, errorMessage)
	return nil
}

func (repository *pollerScheduleRepository) ExpireSchedule(schedule task.Schedule, errorMessage string, referenceTime time.Time) error {
	schedule.ExpiresAt = &referenceTime
	schedule.NextRunAt = nil
	schedule.LastError = errorMessage
	repository.expired = append(repository.expired, schedule)
	return nil
}

func (repository *pollerScheduleRepository) CancelSchedules(task.ScheduleCancelRequest) (task.ScheduleCancelResult, error) {
	return task.ScheduleCancelResult{}, nil
}

type pollerDeliveryRepository struct {
	replies    []connectors.OutboundReply
	errorValue error
}

type pollerTaskIntakeGate struct {
	isQuiesced bool
}

func (gate pollerTaskIntakeGate) IsQuiesced() bool {
	return gate.isQuiesced
}

func (repository *pollerDeliveryRepository) EnqueueScheduledConnectorReply(_ task.Schedule, _ string, reply connectors.OutboundReply) (string, error) {
	if repository.errorValue != nil {
		return "", repository.errorValue
	}
	repository.replies = append(repository.replies, reply)
	return "outbox-1", nil
}

type pollerAtomicScheduleRepository struct {
	*pollerScheduleRepository
	deliveryDeduplicationKeys []string
	deliveryError             error
}

func (repository *pollerAtomicScheduleRepository) MarkScheduleSucceededAndEnqueueDelivery(schedule task.Schedule, _ string, deliveryDeduplicationKey string, _ connectors.OutboundReply) (string, error) {
	if repository.deliveryError != nil {
		return "", repository.deliveryError
	}
	if !repository.hasDeliveryDeduplicationKey(deliveryDeduplicationKey) {
		repository.deliveryDeduplicationKeys = append(repository.deliveryDeduplicationKeys, deliveryDeduplicationKey)
	}
	if errorValue := repository.MarkScheduleSucceeded(schedule); errorValue != nil {
		return "", errorValue
	}
	return deliveryDeduplicationKey, nil
}

func (repository *pollerAtomicScheduleRepository) hasDeliveryDeduplicationKey(deliveryDeduplicationKey string) bool {
	for _, existingDeliveryDeduplicationKey := range repository.deliveryDeduplicationKeys {
		if existingDeliveryDeduplicationKey == deliveryDeduplicationKey {
			return true
		}
	}
	return false
}

type staticPersonAccessResolver struct{}

func (staticPersonAccessResolver) ResolvePersonAccess(personID string) policy.PersonAccess {
	return policy.PersonAccess{PersonID: personID, SecurityLevelRank: 100, GrantedClasses: []string{"internal"}}
}

func testScheduleRunner(turnStatus task.TaskStatus, finishMessage string) agentruntime.ScheduleRunner {
	scheduleRunner, _, _ := scheduleRunnerWithHarness(turnStatus, finishMessage)
	return scheduleRunner
}

func scheduleRunnerWithHarness(turnStatus task.TaskStatus, finishMessage string) (agentruntime.ScheduleRunner, *harnesstest.Harness, *task.TaskRunService) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	harness.TurnStatus = turnStatus
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: finishMessage}
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"ask_confirm"})
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	return agentruntime.NewScheduleRunner(agentruntime.NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)), harness, taskRunService
}

func waitingSchedule(runAt time.Time) task.Schedule {
	return task.Schedule{
		ScheduleID:       "schedule-waiting",
		CreatorPersonID:  "person-1",
		Prompt:           "내 스케줄을 보고 확인이 필요한 일을 알려줘.",
		AgentProfileName: "default",
		Platform:         "mattermost",
		ConversationID:   "channel-1",
		ReplyTargetID:    "reply-target-1",
		TimeZone:         "Asia/Seoul",
		Kind:             task.ScheduleKindOnce,
		RunAt:            &runAt,
		NextRunAt:        &runAt,
	}
}

func TestRecordScheduleFailureExpiresAfterRepeatedFailures(t *testing.T) {
	schedule := waitingSchedule(time.Now().UTC())
	schedule.FailureCount = maxScheduleFailureCount - 1
	if !scheduleFailureIsTerminal(schedule, errors.New("transient"), time.Now()) {
		t.Fatal("expected the failure cap to expire the schedule")
	}
	schedule.FailureCount = 0
	if scheduleFailureIsTerminal(schedule, errors.New("transient"), time.Now()) {
		t.Fatal("expected a first transient failure to stay retryable")
	}
}
