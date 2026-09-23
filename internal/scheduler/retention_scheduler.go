package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

const defaultRetentionDays = 14
const longestTasklessLLMCallRetentionDays = 30

type LLMCallPruner interface {
	PruneTasklessLLMCallsBefore(time.Time) (int64, error)
	DeleteUnreferencedLedgerParts() (int64, error)
}

type TaskRetentionSweeper struct {
	TaskRunService               *task.TaskRunService
	TaskEventService             *task.TaskEventService
	TaskStepService              *task.TaskStepService
	TaskArtifactService          *task.TaskArtifactService
	LLMCallPruner                LLMCallPruner
	Logger                       *slog.Logger
	RetentionDays                int
	TasklessLLMCallRetentionDays int
}

func (sweeper TaskRetentionSweeper) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		sweeper.SweepOnce(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (sweeper TaskRetentionSweeper) SweepOnce(now time.Time) int {
	retentionDays := sweeper.RetentionDays
	if retentionDays <= 0 {
		retentionDays = defaultRetentionDays
	}
	cutoff := now.AddDate(0, 0, -retentionDays)
	prunedIDs := sweeper.TaskRunService.PruneTerminalTaskRunsBefore(cutoff)
	for _, taskRunID := range prunedIDs {
		sweeper.TaskEventService.RemoveTaskRunEvents(taskRunID)
		sweeper.TaskStepService.RemoveTaskRunSteps(taskRunID)
		sweeper.TaskArtifactService.RemoveTaskRunArtifacts(taskRunID)
	}
	if len(prunedIDs) > 0 {
		sweeper.logger().Info("task_retention.swept", "count", len(prunedIDs))
	}
	sweeper.pruneLLMCalls(now)
	return len(prunedIDs)
}

func (sweeper TaskRetentionSweeper) pruneLLMCalls(now time.Time) {
	if sweeper.LLMCallPruner == nil {
		return
	}
	cutoff := now.AddDate(0, 0, -TasklessLLMCallRetentionDays(sweeper.TasklessLLMCallRetentionDays))
	prunedCallCount, errorValue := sweeper.LLMCallPruner.PruneTasklessLLMCallsBefore(cutoff)
	if errorValue != nil {
		sweeper.logger().Warn("llm_call_retention.prune_failed", "error", errorValue.Error())
		return
	}
	deletedPartCount, errorValue := sweeper.LLMCallPruner.DeleteUnreferencedLedgerParts()
	if errorValue != nil {
		sweeper.logger().Warn("llm_call_retention.part_sweep_failed", "error", errorValue.Error())
		return
	}
	if prunedCallCount > 0 || deletedPartCount > 0 {
		sweeper.logger().Info("llm_call_retention.swept", "tasklessCalls", prunedCallCount, "exchangeParts", deletedPartCount)
	}
}

func TasklessLLMCallRetentionDays(configuredDays int) int {
	if configuredDays <= 0 || configuredDays > longestTasklessLLMCallRetentionDays {
		return longestTasklessLLMCallRetentionDays
	}
	return configuredDays
}

func (sweeper TaskRetentionSweeper) logger() *slog.Logger {
	if sweeper.Logger != nil {
		return sweeper.Logger
	}
	return slog.Default()
}
