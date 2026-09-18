package connectors

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type inboundTaskWaitResolution struct {
	TaskWaitToken      task.TaskWaitToken
	HasTaskWaitToken   bool
	IsAmbiguous        bool
	AmbiguousTaskWaits []task.TaskWaitToken
	Reason             string
}

func (connectorRuntime *ConnectorRuntime) resolveInboundTaskWait(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	if connectorRuntime.taskWaitTokenRepository == nil {
		return inboundTaskWaitResolution{}
	}
	if resolution := connectorRuntime.findInboundTaskWaitByPayload(personID, event); resolution.HasTaskWaitToken {
		return resolution
	}
	if resolution := connectorRuntime.findInboundTaskWaitByReplyTarget(personID, platform, event); resolution.HasTaskWaitToken {
		return resolution
	}
	if resolution := connectorRuntime.findInboundTaskWaitByThreadRoot(personID, platform, event); resolution.HasTaskWaitToken {
		return resolution
	}
	if resolution := connectorRuntime.findInboundTaskWaitByDispatchID(personID, platform, event); resolution.HasTaskWaitToken {
		return resolution
	}
	return connectorRuntime.findSingleInboundTaskWait(personID, platform, event)
}

func (connectorRuntime *ConnectorRuntime) findInboundTaskWaitByPayload(personID string, event PlatformInboundEvent) inboundTaskWaitResolution {
	if waitID := firstNonEmptyString(legacyString(event.LegacyFields, "waitID"), legacyString(event.LegacyFields, "wait_id")); waitID != "" {
		return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
			return connectorRuntime.taskWaitTokenRepository.FindOpenByWaitID(waitID)
		}, personID, "payload_wait_id")
	}
	taskRunID := legacyString(event.LegacyFields, "taskRunID")
	interactionID := legacyString(event.LegacyFields, "interactionID")
	if taskRunID == "" || interactionID == "" {
		return inboundTaskWaitResolution{}
	}
	return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
		return connectorRuntime.taskWaitTokenRepository.FindOpenByPersonTaskRunAndInteraction(personID, taskRunID, interactionID)
	}, personID, "payload_task_interaction")
}

func (connectorRuntime *ConnectorRuntime) findInboundTaskWaitByReplyTarget(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	replyTargetID := strings.TrimSpace(event.ReplyTargetID)
	if replyTargetID == "" {
		return inboundTaskWaitResolution{}
	}
	return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
		return connectorRuntime.taskWaitTokenRepository.FindOpenByPersonConversationAndReplyTarget(personID, platform, event.ConversationID, replyTargetID)
	}, personID, "reply_target_id")
}

func (connectorRuntime *ConnectorRuntime) findInboundTaskWaitByThreadRoot(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	threadRootID := eventThreadRootID(event)
	if threadRootID == "" {
		return inboundTaskWaitResolution{}
	}
	return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
		return connectorRuntime.taskWaitTokenRepository.FindOpenByPersonConversationAndThreadRoot(personID, platform, event.ConversationID, threadRootID)
	}, personID, "thread_root_id")
}

func (connectorRuntime *ConnectorRuntime) findInboundTaskWaitByDispatchID(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	dispatchID := firstNonEmptyString(legacyString(event.LegacyFields, "dispatchID"), legacyString(event.LegacyFields, "postID"))
	if dispatchID == "" {
		return inboundTaskWaitResolution{}
	}
	return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
		return connectorRuntime.taskWaitTokenRepository.FindOpenByPersonConversationAndDispatchID(personID, platform, event.ConversationID, dispatchID)
	}, personID, "dispatch_id")
}

func (connectorRuntime *ConnectorRuntime) findSingleInboundTaskWait(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	taskWaitTokens, errorValue := connectorRuntime.taskWaitTokenRepository.FindOpenByPersonAndConversation(personID, platform, event.ConversationID)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".wait.lookup_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return inboundTaskWaitResolution{}
	}
	switch len(taskWaitTokens) {
	case 0:
		return inboundTaskWaitResolution{}
	case 1:
		return inboundTaskWaitResolution{TaskWaitToken: taskWaitTokens[0], HasTaskWaitToken: true, Reason: "single_open_wait"}
	default:
		return inboundTaskWaitResolution{IsAmbiguous: true, AmbiguousTaskWaits: taskWaitTokens, Reason: "multiple_open_waits"}
	}
}

func (connectorRuntime *ConnectorRuntime) findOpenTaskWait(find func() (task.TaskWaitToken, bool, error), personID string, reason string) inboundTaskWaitResolution {
	taskWaitToken, isFound, errorValue := find()
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.wait.lookup_failed", slog.String("reason", reason), slog.String("error", errorValue.Error()))
		return inboundTaskWaitResolution{}
	}
	if !isFound || taskWaitToken.PersonID != strings.TrimSpace(personID) {
		return inboundTaskWaitResolution{}
	}
	return inboundTaskWaitResolution{TaskWaitToken: taskWaitToken, HasTaskWaitToken: true, Reason: reason}
}

func eventThreadRootID(event PlatformInboundEvent) string {
	if eventIsThreadReply(event) {
		return strings.TrimSpace(event.ReplyTargetID)
	}
	return ""
}

func (connectorRuntime *ConnectorRuntime) resolveTaskWaitToken(taskWaitResolution inboundTaskWaitResolution) {
	if connectorRuntime.taskWaitTokenRepository == nil || !taskWaitResolution.HasTaskWaitToken {
		return
	}
	if errorValue := connectorRuntime.taskWaitTokenRepository.ResolveTaskWait(taskWaitResolution.TaskWaitToken.WaitID, time.Now().UTC()); errorValue != nil {
		connectorRuntime.logger.Warn("connector.wait.resolve_failed", slog.String("waitID", taskWaitResolution.TaskWaitToken.WaitID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) resolveOpenTaskWaitsForTaskRun(personID string, platform string, conversationID string, taskRunID string) {
	if connectorRuntime.taskWaitTokenRepository == nil {
		return
	}
	taskWaitTokens, errorValue := connectorRuntime.taskWaitTokenRepository.FindOpenByPersonAndConversation(personID, platform, conversationID)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.wait.lookup_failed", slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return
	}
	for _, taskWaitToken := range taskWaitTokens {
		if taskWaitToken.TaskRunID != strings.TrimSpace(taskRunID) {
			continue
		}
		if errorValue := connectorRuntime.taskWaitTokenRepository.ResolveTaskWait(taskWaitToken.WaitID, time.Now().UTC()); errorValue != nil {
			connectorRuntime.logger.Warn("connector.wait.resolve_failed", slog.String("waitID", taskWaitToken.WaitID), slog.String("error", errorValue.Error()))
		}
	}
}

func (connectorRuntime *ConnectorRuntime) handleAmbiguousTaskWait(
	ctx context.Context,
	platform string,
	adapter PlatformAdapter,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	personID string,
	requesterEmail string,
	personAccess policy.PersonAccess,
	taskWaitResolution inboundTaskWaitResolution,
	engagedAckEmojiName string,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (ConnectorRuntimeResult, error) {
	turnDecision := ambiguousTaskWaitTurnDecision(taskWaitResolution.AmbiguousTaskWaits, responseLanguageForEvent(event))
	conversationTurn := ConversationTurn{
		Platform:                  platform,
		Adapter:                   adapter,
		Event:                     event,
		ReplyTarget:               replyTarget,
		RequesterPersonID:         personID,
		RequesterEmail:            requesterEmail,
		PersonAccess:              personAccess,
		PrecomputedTurnDecision:   &turnDecision,
		CheckpointSender:          connectorRuntime.checkpointSenderForTurn(platform, event, replyTarget, sendReply),
		AccessibleConversationIDs: []string{event.ConversationID},
	}
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, connectorRuntime.buildTaskLaunchRequest(conversationTurn))
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	return connectorRuntime.dispatchTaskReply(ctx, platform, adapter, event, replyTarget, launchResult.TurnResult, engagedAckEmojiName, sendReply)
}

func ambiguousTaskWaitTurnDecision(taskWaitTokens []task.TaskWaitToken, responseLanguage string) agentcontract.TurnDecision {
	return agentcontract.TurnDecision{
		Route:                  agentcontract.TurnRouteClarify,
		Classification:         agentcontract.IntakeClassificationNeedsConfirmation,
		TaskShape:              agentcontract.TaskShapeApprovalGatedTask,
		TaskLevel:              agentcontract.TaskLevelLow,
		ResponseLanguage:       responseLanguage,
		Reason:                 "ambiguous_wait_resolution",
		ClarificationOptions:   taskWaitClarificationOptions(taskWaitTokens),
		ExpectedResults:        []agentcontract.ExpectedResult{{ID: "wait-disambiguation", Type: "message", Description: "ask_choice", Required: true, AcceptanceHints: []string{"ask_choice"}}},
		RequestedOutputFormats: nil,
	}
}

func taskWaitClarificationOptions(taskWaitTokens []task.TaskWaitToken) []agentcontract.ClarificationOption {
	options := []agentcontract.ClarificationOption{}
	for index, taskWaitToken := range taskWaitTokens {
		taskRunLabel := strings.TrimSpace(taskWaitToken.TaskRunID)
		if len(taskRunLabel) > 8 {
			taskRunLabel = taskRunLabel[:8]
		}
		options = append(options, agentcontract.ClarificationOption{
			Key:   string(rune('A' + index)),
			Label: taskRunLabel,
			Value: taskWaitToken.WaitID,
		})
	}
	return options
}

func (connectorRuntime *ConnectorRuntime) recordTaskWaitTokenForReply(platform string, event PlatformInboundEvent, replyTarget ReplyTarget, reply OutboundReply, dispatchID string) {
	if connectorRuntime.taskWaitTokenRepository == nil {
		return
	}
	taskRunID := strings.TrimSpace(reply.TaskRunID)
	if taskRunID == "" || reply.ReplyKind != connectorReplyKindUserNotice {
		return
	}
	taskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if !isFound || !taskRunCanContinueGoal(taskRun, connectorRuntime.taskRunService.ListTaskEvent(taskRunID)) {
		return
	}
	taskWaitToken := connectorRuntime.taskWaitTokenForReply(platform, event, replyTarget, reply, dispatchID, taskRun)
	if taskWaitToken.Kind == "" {
		return
	}
	if errorValue := connectorRuntime.taskWaitTokenRepository.InsertTaskWaitToken(taskWaitToken); errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventTaskWaitPersistFailed, connectorReplyEventBody(event, reply, "", dispatchID, errorValue.Error()))
		connectorRuntime.logger.Warn("connector."+platform+".wait.persist_failed", slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) taskWaitTokenForReply(platform string, event PlatformInboundEvent, replyTarget ReplyTarget, reply OutboundReply, dispatchID string, taskRun task.TaskRun) task.TaskWaitToken {
	now := time.Now().UTC()
	interactionID := replyInteractionID(reply, connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID))
	return task.TaskWaitToken{
		WaitID:         taskWaitID(taskRun.TaskRunID, interactionID, dispatchID),
		TaskRunID:      taskRun.TaskRunID,
		PersonID:       taskRun.RequesterPersonID,
		Platform:       strings.TrimSpace(platform),
		ConversationID: firstNonEmptyString(event.ConversationID, replyTarget.ConversationID, taskRun.OriginConversationID),
		ReplyTargetID:  firstNonEmptyString(dispatchID, replyTarget.ReplyTargetID, taskRun.OriginReplyTargetID),
		ThreadRootID:   firstNonEmptyString(eventThreadRootID(event), replyTarget.ReplyTargetID, taskRun.OriginReplyTargetID),
		DispatchID:     strings.TrimSpace(dispatchID),
		InteractionID:  interactionID,
		Kind:           taskWaitKind(reply, taskRun),
		State:          "open",
		ExpiresAt:      now.Add(24 * time.Hour),
		CreatedAt:      now,
	}
}

func taskWaitID(taskRunID string, interactionID string, dispatchID string) string {
	return strings.Join([]string{
		"wait",
		strings.TrimSpace(taskRunID),
		firstNonEmptyString(interactionID, "interaction"),
		firstNonEmptyString(dispatchID, "dispatch"),
	}, ":")
}

func replyInteractionID(reply OutboundReply, taskEvents []task.TaskEvent) string {
	if reply.Interaction != nil {
		return strings.TrimSpace(reply.Interaction.InteractionID)
	}
	return latestAskInteractionID(taskEvents)
}

func taskWaitKind(reply OutboundReply, taskRun task.TaskRun) string {
	if taskRun.Status == task.TaskStatusWaitingApproval {
		return "approval"
	}
	if reply.Interaction == nil {
		if taskRun.Status == task.TaskStatusWaitingUserInput {
			return "input"
		}
		return ""
	}
	switch normalizedAskInteractionKind(reply.Interaction.Kind) {
	case "ask_confirm":
		return "approval"
	case "ask_input":
		return "input"
	default:
		return ""
	}
}
