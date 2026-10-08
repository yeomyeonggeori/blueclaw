package acpsession

import (
	"context"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const defaultRunPollInterval = 500 * time.Millisecond

func sourceReferenceFor(sessionID acp.SessionId, addressing Addressing, messageContext MessageContext) string {
	if messageContext.MessageID == "" {
		return "acp:" + string(sessionID)
	}
	return connectors.PlatformInboundEvent{
		Platform:       addressing.Platform,
		ConversationID: addressing.ConversationID,
		MessageID:      messageContext.MessageID,
	}.DedupeKey()
}

func (agent *Agent) runAlreadyLaunchedForMessage(personID string, sourceReference string, messageContext MessageContext) (agentcontract.TaskRun, bool) {
	if agent.taskRunStore == nil || messageContext.MessageID == "" {
		return agentcontract.TaskRun{}, false
	}
	return connectors.FindTaskRunBySourceReference(agent.taskRunStore, personID, sourceReference)
}

func (agent *Agent) answerFromRunOfTheSameMessage(ctx context.Context, sessionID acp.SessionId, taskRun agentcontract.TaskRun) (acp.PromptResponse, error) {
	agent.logger.Info("acpsession.prompt.message_already_has_a_run", "sessionID", string(sessionID), "taskRunID", taskRun.TaskRunID)
	for isStillWorking(taskRun) {
		select {
		case <-ctx.Done():
			return acp.PromptResponse{}, ctx.Err()
		case <-time.After(agent.runPollInterval):
		}
		current, isFound := agent.taskRunStore.FindTaskRun(taskRun.TaskRunID)
		if !isFound {
			break
		}
		taskRun = current
	}
	return acp.PromptResponse{StopReason: stopReasonForTaskStatus(taskRun.Status)}, nil
}

func isStillWorking(taskRun agentcontract.TaskRun) bool {
	switch taskRun.Status {
	case agentcontract.TaskStatusPlanned, agentcontract.TaskStatusRunning:
		return true
	}
	return agentcontract.TaskRunWasInterruptedByRuntimeRestart(taskRun)
}
