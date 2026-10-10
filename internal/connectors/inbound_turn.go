package connectors

import (
	"context"
	"log/slog"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type inboundTurn struct {
	adapter     PlatformAdapter
	platform    string
	event       PlatformInboundEvent
	replyTarget ReplyTarget
	sendReply   func(context.Context, ReplyTarget, OutboundReply) (string, error)

	personID       string
	personAccess   policy.PersonAccess
	requesterEmail string

	engagedAckEmojiName string

	settledCalls             []agentcontract.CarriedOutCall
	pendingAskInteraction    AskInteraction
	hasPendingAskInteraction bool
	clearsActiveGoal         bool

	activeGoal    agentcontract.ActiveGoal
	hasActiveGoal bool

	addressingLaunch inboundengagement.Decision
	priorTask        agentcontract.PriorTaskContext

	stopProgress      func()
	isProgressStarted bool
}

func (connectorRuntime *ConnectorRuntime) showTurnProgress(ctx context.Context, turn *inboundTurn) {
	if turn.isProgressStarted {
		return
	}
	turn.stopProgress = connectorRuntime.startProgressHeartbeat(ctx, turn.adapter, turn.replyTarget)
	turn.isProgressStarted = true
}

func (turn *inboundTurn) endProgress() {
	if !turn.isProgressStarted {
		return
	}
	turn.stopProgress()
}

func (connectorRuntime *ConnectorRuntime) logInboundEventReceived(turn *inboundTurn) {
	connectorRuntime.logger.Info(
		"connector."+turn.platform+".ingress.received",
		slog.String("source", turn.event.Source),
		slog.String("messageID", turn.event.MessageID),
		slog.String("conversationID", turn.event.ConversationID),
		slog.String("senderID", turn.event.SenderID),
		slog.String("replyTargetID", turn.event.ReplyTargetID),
		slog.Bool("hasMoreBefore", turn.event.Context.HasMoreBefore),
	)
}

func (connectorRuntime *ConnectorRuntime) admitInboundTurn(ctx context.Context, turn *inboundTurn) (ConnectorRuntimeResult, bool, error) {
	turn.replyTarget = replyTargetOf(turn.event)
	authorization, errorValue := connectorRuntime.authorizeSender(ctx, turn.adapter, turn.event)
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+turn.platform+".auth.failed", slog.String("messageID", turn.event.MessageID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{}, true, errorValue
	}
	turn.personID = authorization.PersonID
	if !authorization.IsAllowed {
		return connectorRuntime.refuseUnauthorizedSender(ctx, turn, authorization), true, nil
	}
	connectorRuntime.logger.Info("connector."+turn.platform+".auth.allowed", slog.String("messageID", turn.event.MessageID), slog.String("personID", turn.personID))
	if result, isHandled := connectorRuntime.suppressDuplicateSourceTaskIfNeeded(turn.platform, turn.event, turn.personID); isHandled {
		return result, true, nil
	}
	for _, message := range turn.event.PreviousMessages {
		connectorRuntime.cancelPendingSourceTask(turn.personID, message.SourceReference)
	}
	if result, isHandled := connectorRuntime.handleTaskControlIfRequested(ctx, turn.platform, turn.adapter, turn.event, turn.replyTarget, turn.personID, turn.sendReply); isHandled {
		return result, true, nil
	}
	return ConnectorRuntimeResult{}, false, nil
}

func (connectorRuntime *ConnectorRuntime) refuseUnauthorizedSender(ctx context.Context, turn *inboundTurn, authorization senderAuthorization) ConnectorRuntimeResult {
	if shouldIgnoreUninvitedAddressing(turn.event) {
		connectorRuntime.logger.Info("connector."+turn.platform+".ingress.ignored", slog.String("messageID", turn.event.MessageID), slog.String("reason", "not_addressed_to_bot"))
		return ConnectorRuntimeResult{Handled: true, Platform: turn.platform, Ignored: true, Reason: "not_addressed_to_bot"}
	}
	refusalReason := "unmatched_account"
	if authorization.DirectoryUnreachable {
		refusalReason = "directory_unreachable"
	}
	connectorRuntime.logger.Info("connector."+turn.platform+".auth.rejected",
		slog.String("messageID", turn.event.MessageID),
		slog.String("reason", refusalReason),
		slog.String("senderID", turn.event.SenderID),
		slog.String("platformAccountEmail", authorization.PlatformAccountEmail))
	dispatchID, sendError := turn.sendReply(ctx, turn.replyTarget, OutboundReply{Message: unmatchedAccountReplyFor(authorization, connectorRuntime.companyLocale()), ReplyKind: connectorReplyKindPermissionNotice})
	if sendError != nil {
		connectorRuntime.logger.Error("connector."+turn.platform+".outbound.failed", slog.String("messageID", turn.event.MessageID), slog.String("error", sendError.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: turn.platform, Reason: refusalReason}
	}
	connectorRuntime.logger.Info("connector."+turn.platform+".outbound.sent", slog.String("messageID", turn.event.MessageID), slog.String("replyDispatchID", dispatchID))
	return ConnectorRuntimeResult{Handled: true, Platform: turn.platform, Reason: refusalReason, ReplyDispatchID: dispatchID}
}

func (connectorRuntime *ConnectorRuntime) resolveOpenInteractions(ctx context.Context, turn *inboundTurn) (ConnectorRuntimeResult, bool, error) {
	turn.personAccess = connectorRuntime.identityService.ResolvePersonAccess(turn.personID)
	turn.requesterEmail = connectorRuntime.requesterEmailForEvent(turn.personID, turn.event)
	turn.engagedAckEmojiName = connectorRuntime.applyEngagedAckReaction(ctx, turn.platform, turn.adapter, turn.event,
		turn.event.Context.Addressing.BotMentioned)
	return connectorRuntime.settleOpenInteractions(ctx, turn)
}

func (connectorRuntime *ConnectorRuntime) resolveTurnActiveGoal(ctx context.Context, turn *inboundTurn) {
	turn.activeGoal, turn.hasActiveGoal = connectorRuntime.findActiveGoal(turn.personID, turn.event)
	if turn.hasActiveGoal && (turn.clearsActiveGoal) {
		turn.activeGoal = agentcontract.ActiveGoal{}
		turn.hasActiveGoal = false
	}
	if len(turn.event.PreviousMessages) > 0 {
		turn.activeGoal = agentcontract.ActiveGoal{}
		turn.hasActiveGoal = false
	}
	if turn.engagedAckEmojiName == "" {
		turn.engagedAckEmojiName = connectorRuntime.applyEngagedAckReaction(ctx, turn.platform, turn.adapter, turn.event,
			turn.hasPendingAskInteraction || turn.hasActiveGoal)
	}
}

func (connectorRuntime *ConnectorRuntime) resolveTurnAddressing(ctx context.Context, turn *inboundTurn) (ConnectorRuntimeResult, bool) {
	turn.event = connectorRuntime.withInitialVisibleContext(ctx, turn.adapter, turn.event)
	if turn.hasPendingAskInteraction {
		turn.addressingLaunch = inboundengagement.Decision{ShouldLaunch: true}
		return ConnectorRuntimeResult{}, false
	}
	turn.addressingLaunch = connectorRuntime.resolveInboundEngagement(ctx, turn.adapter, turn.platform, turn.event)
	if turn.addressingLaunch.ReactionEmoji != "" {
		if turn.engagedAckEmojiName != "" && turn.engagedAckEmojiName != turn.addressingLaunch.ReactionEmoji {
			connectorRuntime.clearEngagedAckReaction(ctx, turn.platform, turn.adapter, turn.event, turn.engagedAckEmojiName)
			turn.engagedAckEmojiName = ""
		}
		connectorRuntime.addAddressingReaction(ctx, turn.platform, turn.adapter, turn.event, turn.addressingLaunch.ReactionEmoji)
	}
	if !turn.addressingLaunch.ShouldLaunch {
		reason := firstNonEmptyString(turn.addressingLaunch.IgnoreReason, "addressing_react_only")
		ignoredAttributes := append([]any{slog.String("messageID", turn.event.MessageID), slog.String("reason", reason)}, heldGatewayDecisionAttributes(turn.event)...)
		connectorRuntime.logger.Info("connector."+turn.platform+".ingress.ignored", ignoredAttributes...)
		return ConnectorRuntimeResult{Handled: true, Platform: turn.platform, Ignored: true, Reason: reason}, true
	}
	if connectorRuntime.shouldDeferNewTaskLaunch(turn.hasPendingAskInteraction, turn.hasActiveGoal) {
		connectorRuntime.logger.Info("connector."+turn.platform+".ingress.deferred", slog.String("messageID", turn.event.MessageID), slog.String("reason", "task_intake_quiesced"))
		return ConnectorRuntimeResult{Handled: true, Platform: turn.platform, Ignored: true, Reason: "task_intake_quiesced"}, true
	}
	return ConnectorRuntimeResult{}, false
}

func (connectorRuntime *ConnectorRuntime) prepareTurnForLaunch(ctx context.Context, turn *inboundTurn) {
	connectorRuntime.showTurnProgress(ctx, turn)
	if ctx.Err() != nil {
		return
	}
	turn.event = connectorRuntime.withAttachmentMaterials(ctx, turn.adapter, turn.event, turn.personID)
	connectorRuntime.resolveTurnPriorTask(turn)
}

func (connectorRuntime *ConnectorRuntime) resolveTurnPriorTask(turn *inboundTurn) {
	if turn.hasPendingAskInteraction || turn.hasActiveGoal {
		return
	}
	var hasRevisedPriorTask bool
	turn.priorTask, hasRevisedPriorTask = connectorRuntime.revisedPriorTask(turn.personID, turn.event)
	if !hasRevisedPriorTask {
		turn.priorTask, _ = connectorRuntime.findPriorTaskContext(turn.personID, turn.event)
	}
}

func (connectorRuntime *ConnectorRuntime) launchTurn(ctx context.Context, turn *inboundTurn) (ConnectorRuntimeResult, error) {
	connectorRuntime.logger.Info("connector."+turn.platform+".agent.started", slog.String("messageID", turn.event.MessageID))
	taskStartedAt := time.Now()
	conversationTurn := connectorRuntime.conversationTurnFor(turn)
	narrator := newTurnNarrator(turn.adapter, turn.replyTarget)
	turn.sendReply = narrator.takeOverSending(turn.sendReply, connectorRuntime.recordingDelivery)
	launchRequest := connectorRuntime.buildTaskLaunchRequest(conversationTurn)
	launchRequest.ToolCallObserver = narrator.toolCallObserver(ctx)
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, launchRequest)
	if errorValue != nil {
		return connectorRuntime.completeTurnLaunchFailure(ctx, turn, errorValue)
	}
	turnResult := launchResult.TurnResult
	taskRunID := turnResult.TaskRun.TaskRunID
	taskDuration := time.Since(taskStartedAt)
	connectorRuntime.logger.Info("connector."+turn.platform+".agent.completed", slog.String("messageID", turn.event.MessageID), slog.String("taskRunID", taskRunID), slog.Int64("duration_ms", taskDuration.Milliseconds()))
	connectorRuntime.appendTaskExecutionDuration(taskRunID, taskDuration)
	return connectorRuntime.dispatchTaskReply(ctx, turn.platform, turn.adapter, turn.event, turn.replyTarget, turnResult, turn.engagedAckEmojiName, turn.sendReply)
}

func (connectorRuntime *ConnectorRuntime) conversationTurnFor(turn *inboundTurn) ConversationTurn {
	return ConversationTurn{
		Platform:                  turn.platform,
		Adapter:                   turn.adapter,
		Event:                     turn.event,
		ReplyTarget:               turn.replyTarget,
		RequesterPersonID:         turn.personID,
		RequesterEmail:            turn.requesterEmail,
		PersonAccess:              turn.personAccess,
		SettledCalls:              turn.settledCalls,
		ActiveGoal:                turn.activeGoal,
		HasActiveGoal:             turn.hasActiveGoal,
		PriorTask:                 turn.priorTask,
		PendingInput:              pendingInputOf(turn),
		AmbientDuty:               turn.addressingLaunch.AmbientDuty,
		DecidedWork:               turn.addressingLaunch.DecidedWork,
		CheckpointSender:          connectorRuntime.checkpointSenderForTurn(turn.platform, turn.event, turn.replyTarget, turn.sendReply),
		AccessibleConversationIDs: []string{turn.event.ConversationID},
		IsBlockedContinuation:     turn.activeGoal.Status == agentcontract.ActiveGoalStatusBlocked && turn.hasActiveGoal,
	}
}

func (connectorRuntime *ConnectorRuntime) completeTurnLaunchFailure(ctx context.Context, turn *inboundTurn, errorValue error) (ConnectorRuntimeResult, error) {
	connectorRuntime.logger.Error("connector."+turn.platform+".agent.failed", slog.String("messageID", turn.event.MessageID), slog.String("error", errorValue.Error()))
	failureTurnResult := connectorRuntime.launchFailureCompleter.CompleteLaunchFailure(ctx, agentcontract.AgentTurnRequest{
		RequesterPersonID: turn.personID,
		RequesterEmail:    turn.requesterEmail,
		Platform:          turn.platform,
		ConversationID:    turn.event.ConversationID,
		Prompt:            turn.event.Prompt,
		ResponseLanguage:  turn.event.Context.ResponseLanguage,
	}, "launch", "connector_launch", errorValue)
	return connectorRuntime.dispatchTaskReply(ctx, turn.platform, turn.adapter, turn.event, turn.replyTarget, failureTurnResult, turn.engagedAckEmojiName, turn.sendReply)
}
