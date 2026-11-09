package connectors

import (
	"context"
	"log/slog"
	"time"
)

const fileKeepWaitLimit = 30 * time.Second

// UseFileKeeper makes every task reply wait until the files the task wrote are
// kept off the box, so a person never reads that something was saved on a box
// that then goes dark before it was. A reply that waited the limit is sent
// anyway: the work is done and recorded, and delivery does not hang on it.
func (connectorRuntime *ConnectorRuntime) UseFileKeeper(keepFiles func(context.Context) error) {
	connectorRuntime.keepFiles = keepFiles
}

func (connectorRuntime *ConnectorRuntime) keepFilesBeforeReplying(ctx context.Context, taskRunID string) {
	if connectorRuntime.keepFiles == nil {
		return
	}
	waiting, cancel := context.WithTimeout(context.WithoutCancel(ctx), fileKeepWaitLimit)
	defer cancel()
	if errorValue := connectorRuntime.keepFiles(waiting); errorValue != nil {
		connectorRuntime.logger.Error("connector.reply.files_not_kept", slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
	}
}
