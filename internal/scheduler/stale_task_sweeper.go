package scheduler

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type StaleTaskNotifier interface {
	FailUnresumedInterruptedTaskRun(context.Context, task.TaskRun, string) bool
}

type StaleTaskSweeper struct {
	TaskRunService *task.TaskRunService
	Notifier       StaleTaskNotifier
	Logger         *slog.Logger
}

func (sweeper StaleTaskSweeper) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		sweeper.SweepOnce(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (sweeper StaleTaskSweeper) SweepOnce(ctx context.Context, now time.Time) int {
	sweptCount := 0
	for _, taskRun := range sweeper.TaskRunService.SelectStaleUnattendedTaskRuns(now) {
		if ctx.Err() != nil {
			return sweptCount
		}
		reason := task.StaleUnattendedTaskRunReason(taskRun, now)
		sweeper.TaskRunService.AppendTaskEvent(taskRun.TaskRunID, taskstate.TaskEventTaskStaleExpired, reason)
		if sweeper.expireStaleTaskRun(ctx, taskRun, reason) {
			sweptCount++
		}
	}
	if sweptCount > 0 {
		sweeper.logger().Info("stale_task.swept", "count", sweptCount)
	}
	return sweptCount
}

func (sweeper StaleTaskSweeper) expireStaleTaskRun(ctx context.Context, taskRun task.TaskRun, reason string) bool {
	if sweeper.shouldNotifyUser(taskRun) {
		return sweeper.Notifier.FailUnresumedInterruptedTaskRun(ctx, taskRun, staleTaskUserFacingReason(reason))
	}
	_, errorValue := sweeper.TaskRunService.FailTaskRun(taskRun.TaskRunID, reason)
	return errorValue == nil
}

func (sweeper StaleTaskSweeper) shouldNotifyUser(taskRun task.TaskRun) bool {
	if sweeper.Notifier == nil {
		return false
	}
	if taskRun.Status != task.TaskStatusBlocked {
		return true
	}
	return strings.TrimSpace(taskRun.Result) == ""
}

func staleTaskUserFacingReason(reason string) string {
	if reason == "waiting_expired" {
		return "the task waited for a user response or approval for several days and has now expired without an answer"
	}
	return "the task stalled with no way to continue on its own and has now been closed after a day without progress"
}

func (sweeper StaleTaskSweeper) logger() *slog.Logger {
	if sweeper.Logger != nil {
		return sweeper.Logger
	}
	return slog.Default()
}
