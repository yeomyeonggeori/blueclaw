package agentruntime

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func (taskLauncher *TaskLauncher) recordTurnInput(taskRunID string, request agentcontract.AgentTurnRequest) {
	taskRunService := taskLauncher.toolCatalogBuilder.taskRunService
	if strings.TrimSpace(taskRunID) == "" || taskRunService == nil {
		return
	}
	document, errorValue := json.Marshal(request)
	if errorValue != nil {
		slog.Warn("task.turn_input_unrecorded", "taskRunID", taskRunID, "error", errorValue.Error())
		return
	}
	taskRunService.AppendPartedTaskEvent(taskRunID, agentcontract.TaskEventTaskTurnInput, document)
}
