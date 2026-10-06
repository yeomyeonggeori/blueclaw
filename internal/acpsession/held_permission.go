package acpsession

import (
	"context"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
)

func (agent *Agent) reissueHeldPermissions(ctx context.Context, sessionID acp.SessionId, sessionContext SessionContext) {
	if agent.taskRunStore == nil {
		return
	}
	for _, taskRun := range agent.taskRunsWaitingForAnAnswer(sessionContext) {
		heldCall, isHeld := approvalgate.PendingHeldCall(agent.taskRunStore.ListTaskEvent(taskRun.TaskRunID))
		if !isHeld {
			continue
		}
		agent.reissueHeldPermission(ctx, sessionID, sessionContext, taskRun, heldCall)
	}
}

func (agent *Agent) taskRunsWaitingForAnAnswer(sessionContext SessionContext) []agentcontract.TaskRun {
	waiting := []agentcontract.TaskRun{}
	for _, taskRun := range agent.taskRunStore.ListTaskRunByPersonID(sessionContext.Requester.PersonID) {
		if taskRun.Status != agentcontract.TaskStatusWaitingApproval {
			continue
		}
		if taskRun.OriginConversationID != sessionContext.Addressing.ConversationID {
			continue
		}
		waiting = append(waiting, taskRun)
	}
	return waiting
}

func (agent *Agent) reissueHeldPermission(ctx context.Context, sessionID acp.SessionId, sessionContext SessionContext, taskRun agentcontract.TaskRun, heldCall agentcontract.HeldCall) {
	toolCallID := acp.ToolCallId(heldCall.HoldID)
	agent.logger.Info("acpsession.permission.reissued",
		"sessionID", string(sessionID),
		"taskRunID", taskRun.TaskRunID,
		"toolName", heldCall.ToolName,
		"toolCallID", string(toolCallID),
	)
	title := strings.TrimSpace(heldCall.Confirmation)
	replyTargetID := firstNonEmpty(taskRun.OriginReplyTargetID, sessionContext.Addressing.ReplyTargetID)
	choices := approvalrecord.OfferedChoices(agent.taskRunStore.ListTaskEvent(taskRun.TaskRunID))
	options := permissionOptions(choices)
	// The client answers with the person's words, and the router that reads them
	// is only offered an approval when the runtime can say which call is waiting.
	agent.permissionRelay.holdWaitingCall(toolCallID, waitingCall{
		approvalRequest: mcpserver.ApprovalRequest{
			RequesterPersonID: sessionContext.Requester.PersonID,
			TaskRunID:         taskRun.TaskRunID,
			ToolName:          heldCall.ToolName,
			ToolInput:         heldCall.ToolInput,
			ApprovalScope:     heldCall.ApprovalScope,
			Prompt:            taskRun.Prompt,
			Platform:          sessionContext.Addressing.Platform,
			ConversationID:    sessionContext.Addressing.ConversationID,
			ReplyTargetID:     replyTargetID,
		},
		confirmation: title,
		options:      options,
	})
	defer agent.permissionRelay.releaseWaitingCall(toolCallID)
	response, errorValue := agent.connection.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: sessionID,
		ToolCall:  acp.ToolCallUpdate{ToolCallId: toolCallID, Title: &title},
		Options:   options,
		Meta:      deliveryMeta(Delivery{ReplyTargetID: replyTargetID, AlreadyPosted: agent.postedQuestionMessageID(taskRun.TaskRunID) != ""}),
	})
	if errorValue != nil {
		agent.logger.Warn("acpsession.permission.reissue_unanswered", "taskRunID", taskRun.TaskRunID, "error", errorValue.Error())
		return
	}
	answer, isAnswered := approvalAnswerForOutcome(response.Outcome)
	if !isAnswered {
		return
	}
	if choice, isChosen := answer.ChosenFrom(choices); isChosen && choice.DefersTheCall() {
		agent.resumeAnsweredTaskRun(ctx, sessionID, sessionContext, taskRun, agent.deferHeldCall(ctx, sessionContext, taskRun, heldCall, choice))
		return
	}
	if choice, isChosen := answer.ChosenFrom(choices); isChosen {
		approvalrecord.RecordChoiceAnswer(agent.taskRunStore, taskRun.TaskRunID, choice)
	}
	approvalrecord.SettleSignal(agent.taskRunStore, taskRun.TaskRunID, &answer.Signal, "acp_permission_reload")
	agent.resumeAnsweredTaskRun(ctx, sessionID, sessionContext, taskRun, nil)
}

func (agent *Agent) deferHeldCall(ctx context.Context, sessionContext SessionContext, taskRun agentcontract.TaskRun, heldCall agentcontract.HeldCall, choice holdrecord.Choice) []agentcontract.CarriedOutCall {
	return []agentcontract.CarriedOutCall{approvalgate.DeferHeldCall(ctx, agent.approvalDeferrer, heldCall, approvalgate.DeferralRequest{
		TaskRunID:         taskRun.TaskRunID,
		RequesterPersonID: sessionContext.Requester.PersonID,
		Platform:          sessionContext.Addressing.Platform,
		ConversationID:    sessionContext.Addressing.ConversationID,
		ReplyTargetID:     firstNonEmpty(taskRun.OriginReplyTargetID, sessionContext.Addressing.ReplyTargetID),
		Prompt:            taskRun.Prompt,
		Choice:            choice,
		ReferenceTime:     time.Now().UTC(),
	})}
}

func (agent *Agent) resumeAnsweredTaskRun(ctx context.Context, sessionID acp.SessionId, sessionContext SessionContext, taskRun agentcontract.TaskRun, settledCalls []agentcontract.CarriedOutCall) {
	requester := sessionContext.Requester
	addressing := sessionContext.Addressing
	launchRequest := agentruntime.TaskLaunchRequest{
		Source:                  agentruntime.TaskLaunchSourceConnector,
		SourceReference:         "acp:reload:" + taskRun.TaskRunID,
		RequesterPersonID:       requester.PersonID,
		RequesterName:           agent.requesterName(requester),
		RequesterCallingName:    requester.CallingName,
		RequesterHandle:         requester.Handle,
		RequesterEmail:          requester.Email,
		IsApprovalContinuation:  true,
		IsRuntimeRestartResume:  true,
		ExistingTaskRunID:       taskRun.TaskRunID,
		SettledCalls:            settledCalls,
		OriginReplyTargetID:     firstNonEmpty(taskRun.OriginReplyTargetID, addressing.ReplyTargetID),
		OriginIsThread:          taskRun.OriginIsThread || addressing.IsThread,
		ProfileName:             defaultProfileName,
		Platform:                addressing.Platform,
		ConversationID:          addressing.ConversationID,
		ConversationType:        addressing.ConversationType,
		ReplyTargetID:           firstNonEmpty(taskRun.OriginReplyTargetID, addressing.ReplyTargetID),
		Prompt:                  taskRun.Prompt,
		ResponseLanguage:        addressing.ResponseLanguage,
		PrecomputedTurnDecision: carryingOnWithTheApprovedCall(addressing.ResponseLanguage),
		PersonAccess:            agent.directory.ResolvePersonAccess(requester.PersonID),
		CheckpointSender:        agent.checkpointSenderFor(sessionID),
	}
	launchResult, errorValue := agent.taskLauncher.Launch(ctx, launchRequest)
	if errorValue != nil {
		agent.logger.Warn("acpsession.permission.reissued_run_will_not_resume", "taskRunID", taskRun.TaskRunID, "error", errorValue.Error())
		return
	}
	sessionTurn := agent.sessionTurns.OpenSessionTurn(ctx, inboundEventOf(MessageContext{}, launchRequest), requester.PersonID, agent.replySenderFor(sessionID))
	agent.deliverTurnReply(ctx, sessionID, sessionTurn, launchResult.TurnResult)
}

// The task this resumes was already routed, and asking the router again would
// re-decide a turn the requester has just answered a question about.
func carryingOnWithTheApprovedCall(responseLanguage string) *agentcontract.TurnDecision {
	return &agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteContinueTask,
		Classification:   agentcontract.IntakeClassificationBoundedTask,
		TaskShape:        agentcontract.TaskShapeMaintenanceTask,
		ResponseLanguage: responseLanguage,
		Reason:           "acp_permission_reload",
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
