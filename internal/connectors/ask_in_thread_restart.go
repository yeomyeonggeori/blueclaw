package connectors

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func (connectorRuntime *ConnectorRuntime) reissuePendingApprovalQuestions(ctx context.Context) {
	if connectorRuntime.askingThreads == nil {
		return
	}
	for _, taskRun := range connectorRuntime.taskRunService.ListTaskRun() {
		if taskRun.Status == task.TaskStatusWaitingApproval && time.Since(taskRun.UpdatedAt) <= approvalExpiry {
			connectorRuntime.reissueApprovalQuestion(ctx, taskRun)
		}
	}
}

func (connectorRuntime *ConnectorRuntime) reissueApprovalQuestion(ctx context.Context, taskRun task.TaskRun) {
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
	heldCall, isHeld := approvalgate.PendingHeldCall(taskEvents)
	if !isHeld || approvalQuestionIsPosted(taskEvents) {
		return
	}
	turn, isReady := connectorRuntime.restartedQuestionTurn(taskRun, taskEvents)
	if !isReady {
		return
	}
	connectorRuntime.logger.Info("connector."+turn.platform+".approval.question_reissued", "taskRunID", taskRun.TaskRunID)
	connectorRuntime.deliverApprovalQuestion(ctx, turn, taskRun.TaskRunID, heldCall.Confirmation)
}

func (connectorRuntime *ConnectorRuntime) restartedQuestionTurn(taskRun task.TaskRun, taskEvents []task.TaskEvent) (*inboundTurn, bool) {
	launchContext, isFound := interruptedTaskLaunchContextFromEvents(taskRun, taskEvents)
	if !isFound {
		return nil, false
	}
	adapter, errorValue := connectorRuntime.findAdapter(launchContext.Platform)
	if errorValue != nil {
		return nil, false
	}
	event := interruptedTaskResumeEvent(taskRun, launchContext)
	return &inboundTurn{
		adapter:     adapter,
		platform:    launchContext.Platform,
		event:       event,
		replyTarget: ReplyTarget{ConversationID: event.ConversationID, ReplyTargetID: event.ReplyTargetID, DedupeKey: event.DedupeKey()},
		sendReply:   connectorRuntime.recordingDelivery(adapter.SendReply),
	}, true
}

func approvalQuestionIsPosted(taskEvents []task.TaskEvent) bool {
	isPosted := false
	for _, taskEvent := range taskEvents {
		switch taskEvent.Name {
		case agentcontract.TaskEventApprovalHoldOpened:
			isPosted = false
		case agentcontract.TaskEventConnectorReplySent, agentcontract.TaskEventConnectorReplyEnqueued:
			isPosted = isPosted || replyKindCarriesQuestion(taskEvent.Body)
		}
	}
	return isPosted
}

func replyKindCarriesQuestion(body string) bool {
	sent := struct {
		ReplyKind string `json:"replyKind"`
	}{}
	if json.Unmarshal([]byte(body), &sent) != nil {
		return false
	}
	replyKind := strings.TrimSpace(sent.ReplyKind)
	return replyKind == connectorReplyKindApprovalQuestion || replyKind == connectorReplyKindSuccess
}
