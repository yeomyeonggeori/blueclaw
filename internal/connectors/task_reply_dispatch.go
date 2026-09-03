package connectors

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type taskReplyDecisionKind string

const (
	taskReplyDecisionConsume            taskReplyDecisionKind = "consume"
	taskReplyDecisionSuppressCancelled  taskReplyDecisionKind = "suppress_cancelled"
	taskReplyDecisionSuppressSuperseded taskReplyDecisionKind = "suppress_superseded"
	taskReplyDecisionSuppressRequested  taskReplyDecisionKind = "suppress_requested"
	taskReplyDecisionSuppressDelivered  taskReplyDecisionKind = "suppress_already_delivered"
	taskReplyDecisionSendUserNotice     taskReplyDecisionKind = "send_user_notice"
	taskReplyDecisionSendFinal          taskReplyDecisionKind = "send_final"
)

type taskReplyDecision struct {
	Kind   taskReplyDecisionKind
	Reason string
}

func decideTaskReply(turnResult agentcontract.AgentTurnResult, isCancelledBeforeSend bool, hasAgentDeliveredReply bool) taskReplyDecision {
	if turnResult.TurnRoute == agentcontract.TurnRouteConsume {
		return taskReplyDecision{Kind: taskReplyDecisionConsume}
	}
	if strings.TrimSpace(turnResult.ReplySuppressionReason) != "" &&
		turnResult.TaskRun.Status != task.TaskStatusWaitingApproval &&
		turnResult.TaskRun.Status != task.TaskStatusWaitingUserInput {
		return taskReplyDecision{Kind: taskReplyDecisionSuppressRequested, Reason: strings.TrimSpace(turnResult.ReplySuppressionReason)}
	}
	if turnResult.TaskRun.Status == task.TaskStatusCancelled || isCancelledBeforeSend {
		return taskReplyDecision{Kind: taskReplyDecisionSuppressCancelled, Reason: "task_cancelled"}
	}
	if turnResult.TaskRun.Status == task.TaskStatusInterrupted && strings.TrimSpace(turnResult.TaskRun.FailureReason) == "superseded_by_new_message" {
		return taskReplyDecision{Kind: taskReplyDecisionSuppressSuperseded, Reason: "superseded_by_new_message"}
	}
	if turnResult.TaskRun.Status != task.TaskStatusCompleted {
		return taskReplyDecision{Kind: taskReplyDecisionSendUserNotice, Reason: "task_not_completed"}
	}
	if hasAgentDeliveredReply {
		return taskReplyDecision{Kind: taskReplyDecisionSuppressDelivered, Reason: "agent_already_replied_to_this_conversation"}
	}
	return taskReplyDecision{Kind: taskReplyDecisionSendFinal}
}

func (connectorRuntime *ConnectorRuntime) dispatchTaskReply(
	ctx context.Context,
	platform string,
	adapter PlatformAdapter,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	turnResult agentcontract.AgentTurnResult,
	engagedAckEmojiName string,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (ConnectorRuntimeResult, error) {
	taskRunID := turnResult.TaskRun.TaskRunID
	decision := decideTaskReply(turnResult, connectorRuntime.taskRunWasCancelled(taskRunID), connectorRuntime.agentAlreadyReplied(taskRunID, event.ConversationID, replyTarget.ConversationID, replyTarget.ReplyTargetID))
	switch decision.Kind {
	case taskReplyDecisionConsume:
		reason := connectorRuntime.addConsumeReaction(ctx, platform, adapter, event, taskRunID, turnResult.ReactionEmojiName)
		if reason == "consume_reacted" && engagedAckEmojiName != "" && engagedAckEmojiName != consumeReactionEmojiName(turnResult.ReactionEmojiName) {
			connectorRuntime.clearEngagedAckReaction(ctx, platform, adapter, event, engagedAckEmojiName)
		}
		if reason != "consume_reacted" && !isMultiPersonConversation(event) && strings.TrimSpace(turnResult.FinishMessage) != "" {
			result, errorValue := connectorRuntime.sendCompletedTaskReply(ctx, platform, event, taskRunID, replyTarget, turnResult, sendReply)
			if result.ReplyDispatchID != "" {
				result.Reason = "consume_fallback_sent"
			}
			return result, errorValue
		}
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: reason}, nil
	case taskReplyDecisionSuppressDelivered:
		connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventReplySuppressedDuplicate, marshalConnectorEventBody(map[string]string{
			"conversationID": event.ConversationID,
			"reason":         decision.Reason,
		}))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: decision.Reason}, nil
	case taskReplyDecisionSuppressCancelled:
		connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventTaskStopOutboxSuppressed, marshalConnectorEventBody(map[string]string{
			"messageID": event.MessageID,
			"reason":    "task was cancelled before final reply send",
		}))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: decision.Reason}, nil
	case taskReplyDecisionSuppressSuperseded, taskReplyDecisionSuppressRequested:
		connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventConnectorReplySuppressed, marshalConnectorEventBody(map[string]string{
			"messageID": event.MessageID,
			"reason":    decision.Reason,
		}))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: decision.Reason}, nil
	case taskReplyDecisionSendUserNotice:
		dispatchID, isSent := connectorRuntime.sendUserNoticeReply(ctx, platform, event, taskRunID, replyTarget, turnResult, sendReply)
		if isSent {
			connectorRuntime.clearEngagedAckReaction(ctx, platform, adapter, event, engagedAckEmojiName)
			return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: decision.Reason, ReplyDispatchID: dispatchID}, nil
		}
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: decision.Reason}, nil
	default:
		result, errorValue := connectorRuntime.sendCompletedTaskReply(ctx, platform, event, taskRunID, replyTarget, turnResult, sendReply)
		if result.ReplyDispatchID != "" {
			connectorRuntime.clearEngagedAckReaction(ctx, platform, adapter, event, engagedAckEmojiName)
		}
		return result, errorValue
	}
}

func (connectorRuntime *ConnectorRuntime) sendCompletedTaskReply(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	taskRunID string,
	replyTarget ReplyTarget,
	turnResult agentcontract.AgentTurnResult,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (ConnectorRuntimeResult, error) {
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{
		Message:         turnResult.FinishMessage,
		TaskRunID:       taskRunID,
		ReplyKind:       connectorReplyKindSuccess,
		Attachments:     turnResult.Attachments,
		RecoveryActions: recoveryActionsForEvent(turnResult.RecoveryActions, event),
	})
	if errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplyFailed, connectorReplyEventBody(event, OutboundReply{TaskRunID: taskRunID, ReplyKind: connectorReplyKindSuccess}, "", "", errorValue.Error()))
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", "messageID", event.MessageID, "taskRunID", taskRunID, "error", errorValue.Error())
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, Reason: "reply_failed"}, nil
	}
	if connectorRuntime.outboxRepository() == nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplySent, connectorReplyEventBody(event, OutboundReply{TaskRunID: taskRunID, ReplyKind: connectorReplyKindSuccess}, "", dispatchID, ""))
	}
	connectorRuntime.logger.Info("connector."+platform+".outbound.sent", "messageID", event.MessageID, "taskRunID", taskRunID, "replyDispatchID", dispatchID)
	return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: taskRunID, ReplyDispatchID: dispatchID}, nil
}

func (connectorRuntime *ConnectorRuntime) agentAlreadyReplied(taskRunID string, deliveryTargets ...string) bool {
	if connectorRuntime.taskRunService == nil || strings.TrimSpace(taskRunID) == "" {
		return false
	}
	return agentAlreadyDeliveredTo(connectorRuntime.taskRunService.ListTaskEvent(taskRunID), deliveryTargets)
}
