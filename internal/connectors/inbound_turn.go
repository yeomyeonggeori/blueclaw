package connectors

import (
	"context"
	"log/slog"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
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

	taskWaitResolution  inboundTaskWaitResolution
	engagedAckEmojiName string

	pendingApproval                 pendingApproval
	turnDecision                    agentcontract.TurnDecision
	hasPendingConfirmation          bool
	isApprovalContinuation          bool
	didSupersedePendingConfirmation bool

	pendingAskInteraction    AskInteraction
	hasPendingAskInteraction bool
	askTurnDecision          agentcontract.TurnDecision
	hasAskTurnDecision       bool
	didSupersedePendingAsk   bool

	activeGoal    agentcontract.ActiveGoal
	hasActiveGoal bool

	addressingLaunch inboundEngagementDecision
	priorTask        agentcontract.PriorTaskContext

	stopProgress      func()
	isProgressStarted bool
}

func (turn *inboundTurn) startProgress(stopProgress func()) {
	turn.stopProgress = stopProgress
	turn.isProgressStarted = true
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
	replyTarget, errorValue := connectorRuntime.buildReplyTarget(ctx, turn.adapter, turn.event)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, true, errorValue
	}
	turn.replyTarget = replyTarget
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

func (connectorRuntime *ConnectorRuntime) resolvePendingConfirmation(ctx context.Context, turn *inboundTurn) (ConnectorRuntimeResult, bool, error) {
	turn.personAccess = connectorRuntime.identityService.ResolvePersonAccess(turn.personID)
	turn.requesterEmail = connectorRuntime.requesterEmailForEvent(turn.personID, turn.event)
	turn.taskWaitResolution = connectorRuntime.resolveInboundTaskWait(turn.personID, turn.platform, turn.event)
	turn.engagedAckEmojiName = connectorRuntime.applyEngagedAckReaction(ctx, turn.platform, turn.adapter, turn.event,
		turn.event.Context.Addressing.BotMentioned || turn.taskWaitResolution.HasTaskWaitToken || turn.taskWaitResolution.IsAmbiguous)
	if turn.taskWaitResolution.IsAmbiguous {
		result, errorValue := connectorRuntime.handleAmbiguousTaskWait(ctx, turn.platform, turn.adapter, turn.event, turn.replyTarget, turn.personID, turn.requesterEmail, turn.personAccess, turn.taskWaitResolution, turn.engagedAckEmojiName, turn.sendReply)
		return result, true, errorValue
	}
	resolvedApproval, turnDecision, hasPendingConfirmation, errorValue := connectorRuntime.resolveConfirmationReply(ctx, turn.platform, turn.personID, turn.event, turn.taskWaitResolution)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, true, errorValue
	}
	turn.pendingApproval = resolvedApproval
	turn.turnDecision = turnDecision
	turn.hasPendingConfirmation = hasPendingConfirmation
	turn.isApprovalContinuation = hasPendingConfirmation && turnDecision.Approval != nil && agentcontract.IsApprovingSignal(*turnDecision.Approval)
	if hasPendingConfirmation {
		connectorRuntime.resolveTaskWaitToken(turn.taskWaitResolution)
	}
	if !hasPendingConfirmation || turn.isApprovalContinuation {
		return ConnectorRuntimeResult{}, false, nil
	}
	return connectorRuntime.supersedePendingConfirmation(ctx, turn)
}

func (connectorRuntime *ConnectorRuntime) supersedePendingConfirmation(ctx context.Context, turn *inboundTurn) (ConnectorRuntimeResult, bool, error) {
	if shouldStopAfterPendingConfirmation(turn.turnDecision) {
		rejection := agentcontract.ConfirmationReplyDecision{Decision: string(agentcontract.ApprovalSignalReject), Reason: turn.turnDecision.Reason}
		result, errorValue := connectorRuntime.handleRejectedConfirmation(ctx, turn.platform, turn.adapter, turn.event, turn.replyTarget, turn.pendingApproval, rejection, turn.sendReply)
		return result, true, errorValue
	}
	if turn.turnDecision.Route == agentcontract.TurnRouteAnswerQuestion {
		result, errorValue := connectorRuntime.handlePendingConfirmationQuestion(ctx, turn.platform, turn.adapter, turn.event, turn.replyTarget, turn.pendingApproval, turn.turnDecision, turn.sendReply)
		return result, true, errorValue
	}
	connectorRuntime.cancelPendingConfirmation(turn.event, turn.pendingApproval, turn.turnDecision)
	turn.didSupersedePendingConfirmation = true
	return ConnectorRuntimeResult{}, false, nil
}

func (connectorRuntime *ConnectorRuntime) resolvePendingAsk(ctx context.Context, turn *inboundTurn) error {
	pendingAskInteraction, hasPendingAskInteraction := connectorRuntime.findPendingAskInteraction(turn.personID, turn.platform, turn.event, turn.taskWaitResolution)
	previousPrompt := turn.event.Prompt
	event, askTurnDecision, hasAskTurnDecision, errorValue := connectorRuntime.resolveAskReply(ctx, turn.platform, turn.personID, turn.event, turn.taskWaitResolution)
	if errorValue != nil {
		return errorValue
	}
	turn.event = event
	turn.pendingAskInteraction = pendingAskInteraction
	turn.hasPendingAskInteraction = hasPendingAskInteraction
	turn.askTurnDecision = askTurnDecision
	turn.hasAskTurnDecision = hasAskTurnDecision
	if !hasPendingAskInteraction {
		return nil
	}
	if askReplySupersedesInteraction(askTurnDecision, hasAskTurnDecision) {
		connectorRuntime.supersedePendingAskInteraction(turn.event, pendingAskInteraction, askTurnDecision)
		connectorRuntime.resolveTaskWaitToken(turn.taskWaitResolution)
		turn.pendingAskInteraction = AskInteraction{}
		turn.hasPendingAskInteraction = false
		turn.taskWaitResolution = inboundTaskWaitResolution{}
		turn.didSupersedePendingAsk = true
		return nil
	}
	if askReplyConsumesInteraction(pendingAskInteraction, previousPrompt, turn.event, askTurnDecision, hasAskTurnDecision) {
		connectorRuntime.appendAskResolvedEvent(pendingAskInteraction, turn.event, askTurnDecision)
		connectorRuntime.resolveTaskWaitToken(turn.taskWaitResolution)
	}
	return nil
}

func (connectorRuntime *ConnectorRuntime) resolveTurnActiveGoal(ctx context.Context, turn *inboundTurn) {
	turn.activeGoal, turn.hasActiveGoal = connectorRuntime.findActiveGoal(turn.personID, turn.platform, turn.event, turn.taskWaitResolution)
	if !turn.isApprovalContinuation && turn.hasActiveGoal && turn.turnDecision.Route == agentcontract.TurnRouteStartTask {
		turn.activeGoal = agentcontract.ActiveGoal{}
		turn.hasActiveGoal = false
	}
	if turn.isApprovalContinuation {
		turn.event = approvedContinuationEvent(turn.event, turn.pendingApproval)
		turn.activeGoal = pendingApprovalActiveGoal(turn.pendingApproval, turn.event.Prompt)
		turn.hasActiveGoal = true
	}
	if turn.engagedAckEmojiName == "" {
		turn.engagedAckEmojiName = connectorRuntime.applyEngagedAckReaction(ctx, turn.platform, turn.adapter, turn.event,
			turn.isApprovalContinuation || turn.hasPendingAskInteraction || turn.hasActiveGoal)
	}
}

func (connectorRuntime *ConnectorRuntime) resolveTurnAddressing(ctx context.Context, turn *inboundTurn) (ConnectorRuntimeResult, bool) {
	turn.event = connectorRuntime.withInitialVisibleContext(ctx, turn.adapter, turn.event)
	turn.addressingLaunch = connectorRuntime.resolveInboundEngagement(ctx, turn.platform, turn.event)
	if turn.addressingLaunch.ReactionEmoji != "" {
		if turn.engagedAckEmojiName != "" && turn.engagedAckEmojiName != turn.addressingLaunch.ReactionEmoji {
			connectorRuntime.clearEngagedAckReaction(ctx, turn.platform, turn.adapter, turn.event, turn.engagedAckEmojiName)
			turn.engagedAckEmojiName = ""
		}
		connectorRuntime.addAddressingReaction(ctx, turn.platform, turn.adapter, turn.event, turn.addressingLaunch.ReactionEmoji)
	}
	if !turn.addressingLaunch.ShouldLaunch {
		reason := firstNonEmptyString(turn.addressingLaunch.IgnoreReason, "addressing_react_only")
		connectorRuntime.logger.Info("connector."+turn.platform+".ingress.ignored", slog.String("messageID", turn.event.MessageID), slog.String("reason", reason))
		return ConnectorRuntimeResult{Handled: true, Platform: turn.platform, Ignored: true, Reason: reason}, true
	}
	if connectorRuntime.shouldDeferNewTaskLaunch(turn.isApprovalContinuation, turn.hasPendingAskInteraction, turn.hasActiveGoal) {
		connectorRuntime.logger.Info("connector."+turn.platform+".ingress.deferred", slog.String("messageID", turn.event.MessageID), slog.String("reason", "task_intake_quiesced"))
		return ConnectorRuntimeResult{Handled: true, Platform: turn.platform, Ignored: true, Reason: "task_intake_quiesced"}, true
	}
	return ConnectorRuntimeResult{}, false
}

func (connectorRuntime *ConnectorRuntime) handleBusyTurn(ctx context.Context, turn *inboundTurn) (ConnectorRuntimeResult, bool, error) {
	if turn.isApprovalContinuation || turn.hasPendingAskInteraction || turn.didSupersedePendingAsk || turn.didSupersedePendingConfirmation {
		return ConnectorRuntimeResult{}, false, nil
	}
	busyResult, errorValue := connectorRuntime.handleBusyMessageIfNeeded(ctx, turn.platform, turn.event, turn.replyTarget, turn.personID, turn.sendReply)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, true, errorValue
	}
	if busyResult.isHandled {
		return busyResult.connectorResult, true, nil
	}
	if busyResult.clearActiveGoal {
		turn.activeGoal = agentcontract.ActiveGoal{}
		turn.hasActiveGoal = false
	}
	return ConnectorRuntimeResult{}, false, nil
}

func (connectorRuntime *ConnectorRuntime) prepareTurnForLaunch(ctx context.Context, turn *inboundTurn) {
	if !turn.isProgressStarted {
		turn.startProgress(connectorRuntime.startProgressHeartbeat(ctx, turn.adapter, turn.replyTarget))
	}
	turn.event = connectorRuntime.withAttachmentMaterials(ctx, turn.adapter, turn.event, turn.personID)
	if !turn.isApprovalContinuation && !turn.hasPendingAskInteraction && !turn.hasActiveGoal {
		turn.priorTask, _ = connectorRuntime.findPriorTaskContext(turn.personID, turn.event)
	}
}

func (connectorRuntime *ConnectorRuntime) launchTurn(ctx context.Context, turn *inboundTurn) (ConnectorRuntimeResult, error) {
	connectorRuntime.logger.Info("connector."+turn.platform+".agent.started", slog.String("messageID", turn.event.MessageID))
	precomputedTurnDecision := precomputedTurnDecisionForLaunch(turn.turnDecision, turn.hasPendingConfirmation, turn.askTurnDecision, turn.hasAskTurnDecision)
	taskStartedAt := time.Now()
	conversationTurn := connectorRuntime.conversationTurnFor(turn, precomputedTurnDecision)
	narrator := connectorRuntime.startNarrating(ctx, turn.adapter, turn.replyTarget)
	defer narrator.stop()
	turn.sendReply = narrator.takeOverSending(turn.sendReply)
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, connectorRuntime.buildTaskLaunchRequest(conversationTurn))
	if errorValue != nil {
		return connectorRuntime.completeTurnLaunchFailure(ctx, turn, errorValue)
	}
	turnResult := launchResult.TurnResult
	if turn.addressingLaunch.SuppressReply {
		turnResult.ReplySuppressionReason = "ambient_duty_no_reply"
	}
	taskRunID := turnResult.TaskRun.TaskRunID
	taskDuration := time.Since(taskStartedAt)
	connectorRuntime.logger.Info("connector."+turn.platform+".agent.completed", slog.String("messageID", turn.event.MessageID), slog.String("taskRunID", taskRunID), slog.Int64("duration_ms", taskDuration.Milliseconds()))
	connectorRuntime.appendTaskExecutionDuration(taskRunID, taskDuration)
	return connectorRuntime.dispatchTaskReply(ctx, turn.platform, turn.adapter, turn.event, turn.replyTarget, turnResult, turn.engagedAckEmojiName, turn.sendReply)
}

func (connectorRuntime *ConnectorRuntime) conversationTurnFor(turn *inboundTurn, precomputedTurnDecision *agentcontract.TurnDecision) ConversationTurn {
	return ConversationTurn{
		Platform:                  turn.platform,
		Adapter:                   turn.adapter,
		Event:                     turn.event,
		ReplyTarget:               turn.replyTarget,
		RequesterPersonID:         turn.personID,
		RequesterEmail:            turn.requesterEmail,
		PersonAccess:              turn.personAccess,
		IsApprovalContinuation:    turn.isApprovalContinuation,
		ActiveGoal:                turn.activeGoal,
		HasActiveGoal:             turn.hasActiveGoal,
		PriorTask:                 turn.priorTask,
		PrecomputedTurnDecision:   precomputedTurnDecision,
		AmbientDuty:               turn.addressingLaunch.AmbientDuty,
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
