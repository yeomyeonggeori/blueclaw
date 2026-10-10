package agentruntime

import (
	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/acpupdate"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const overheardRunChangedNothing = "overheard_duty_run_changed_nothing"

func (taskLauncher *TaskLauncher) withOverheardRunQuietWhenNothingChanged(request TaskLaunchRequest, toolSet *toolcontract.ToolSet, turnResult agentcontract.AgentTurnResult) agentcontract.AgentTurnResult {
	if !request.AmbientDuty.IsMatch || turnResult.TaskRun.Status != agentcontract.TaskStatusCompleted {
		return turnResult
	}
	return quietWhenNothingChanged(toolSet, taskLauncher.taskRunService.ListTaskEvent(turnResult.TaskRun.TaskRunID), turnResult)
}

func quietWhenNothingChanged(toolSet *toolcontract.ToolSet, events []agentcontract.TaskEvent, turnResult agentcontract.AgentTurnResult) agentcontract.AgentTurnResult {
	if hasChangedAnything(toolSet, events) {
		return turnResult
	}
	turnResult.ReplySuppressed = true
	turnResult.ReplySuppressionReason = overheardRunChangedNothing
	return turnResult
}

func hasChangedAnything(toolSet *toolcontract.ToolSet, events []agentcontract.TaskEvent) bool {
	for _, event := range events {
		toolName, isResult := agentcontract.ToolTaskEventToolName(event.Name, agentcontract.ToolTaskEventResultSuffix)
		if isResult && isSucceededToolResult(event) && changesEnvironment(toolSet, toolName) {
			return true
		}
	}
	return false
}

func isSucceededToolResult(event agentcontract.TaskEvent) bool {
	update, isToolCall := acpupdate.ToolCallForEvent(taskstate.RawTurnEvent{Name: event.Name, Body: event.Body})
	return isToolCall && update.ToolCallUpdate != nil && update.ToolCallUpdate.Status != nil &&
		*update.ToolCallUpdate.Status != acp.ToolCallStatusFailed
}

func changesEnvironment(toolSet *toolcontract.ToolSet, toolName string) bool {
	definition, isKnown := toolSet.ToolDefinition(toolName)
	if !isKnown {
		return true
	}
	return !mcpserver.LeavesEnvironmentUnchanged(toolcontract.ToolDefinitionSideEffectClass(definition))
}
