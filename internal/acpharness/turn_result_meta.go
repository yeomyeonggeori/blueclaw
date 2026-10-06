package acpharness

import (
	"encoding/json"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func (harness *Harness) UseTurnResultMeta(metaKey string) {
	harness.turnResultMetaKey = metaKey
}

func (harness *Harness) carriedTurnResult(promptResponse acp.PromptResponse) (agentcontract.AgentTurnResult, bool) {
	if harness.turnResultMetaKey == "" {
		return agentcontract.AgentTurnResult{}, false
	}
	value, isPresent := promptResponse.Meta[harness.turnResultMetaKey]
	if !isPresent {
		return agentcontract.AgentTurnResult{}, false
	}
	encoded, errorValue := json.Marshal(value)
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, false
	}
	turnResult := agentcontract.AgentTurnResult{}
	return turnResult, json.Unmarshal(encoded, &turnResult) == nil
}

func (harness *Harness) settledTurnResult(request agentcontract.AgentTurnRequest, carried agentcontract.AgentTurnResult) agentcontract.AgentTurnResult {
	taskRunID := strings.TrimSpace(request.ExistingTaskRunID)
	if harness.taskRunStore != nil && taskRunID != "" && isParked(harness.taskRunStore, taskRunID) {
		return parkedTurnResult(harness.taskRunStore, taskRunID, carried)
	}
	if harness.taskRunStore != nil && taskRunID != "" {
		carried.TaskRun = settleTaskRun(harness.taskRunStore, taskRunID, carried.TaskRun)
	}
	return carried
}

func settleTaskRun(taskRunStore taskstate.TaskRunStore, taskRunID string, carried agentcontract.TaskRun) agentcontract.TaskRun {
	switch carried.Status {
	case agentcontract.TaskStatusCompleted:
		taskRunStore.CompleteTaskRun(taskRunID, carried.Result)
	case agentcontract.TaskStatusFailed:
		taskRunStore.FailTaskRun(taskRunID, carried.FailureReason)
	case agentcontract.TaskStatusBlocked, agentcontract.TaskStatusWaitingUserInput:
		taskRunStore.PauseTaskRun(taskRunID, carried.Status, carried.FailureReason)
	}
	if storedTaskRun, isFound := taskRunStore.FindTaskRun(taskRunID); isFound {
		return storedTaskRun
	}
	return carried
}

func parkedTurnResult(taskRunStore taskstate.TaskRunStore, taskRunID string, carried agentcontract.AgentTurnResult) agentcontract.AgentTurnResult {
	parkedTaskRun, _ := taskRunStore.FindTaskRun(taskRunID)
	return agentcontract.AgentTurnResult{
		TaskRun:     parkedTaskRun,
		UserNotice:  parkedTaskRun.FailureReason,
		ToolNames:   carried.ToolNames,
		Attachments: carried.Attachments,
	}
}
