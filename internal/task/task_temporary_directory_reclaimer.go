package task

import (
	"log/slog"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

type TaskTemporaryDirectoryReclaimer struct {
	cleaner security.TaskTemporaryDirectoryCleaner
	logger  *slog.Logger
}

func NewTaskTemporaryDirectoryReclaimer(workspaceRootPath string, logger *slog.Logger) TaskTemporaryDirectoryReclaimer {
	return TaskTemporaryDirectoryReclaimer{
		cleaner: security.TaskTemporaryDirectoryCleaner{WorkspaceRootPath: workspaceRootPath},
		logger:  logger,
	}
}

func (reclaimer TaskTemporaryDirectoryReclaimer) Observe(taskRun TaskRun) {
	if !taskRunHasEnded(taskRun.Status) {
		return
	}
	errorValue := reclaimer.cleaner.RemoveTaskTemporaryDirectory(taskRun.RequesterPersonID, taskRun.TaskRunID)
	if errorValue == nil {
		return
	}
	reclaimer.activeLogger().Warn("task_temporary_directory.remove_failed",
		"taskRunID", taskRun.TaskRunID,
		"status", string(taskRun.Status),
		"error", errorValue)
}

func taskRunHasEnded(status TaskStatus) bool {
	return status == TaskStatusCompleted || status == TaskStatusFailed || status == TaskStatusCancelled
}

func (reclaimer TaskTemporaryDirectoryReclaimer) activeLogger() *slog.Logger {
	if reclaimer.logger != nil {
		return reclaimer.logger
	}
	return slog.Default()
}
