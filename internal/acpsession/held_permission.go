package acpsession

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

var errNoDeferrer = errors.New("this agent was started without a scheduler for approved calls")

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
	toolCallID := acp.ToolCallId(approvalgate.HeldCallID(heldCall.ToolName, heldCall.ToolInput))
	agent.logger.Info("acpsession.permission.reissued",
		"sessionID", string(sessionID),
		"taskRunID", taskRun.TaskRunID,
		"toolName", heldCall.ToolName,
		"toolCallID", string(toolCallID),
	)
	title := strings.TrimSpace(heldCall.Confirmation)
	choices := approvalgate.OfferedChoices(agent.taskRunStore.ListTaskEvent(taskRun.TaskRunID))
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
		},
		confirmation: title,
		choices:      choices,
	})
	defer agent.permissionRelay.releaseWaitingCall(toolCallID)
	response, errorValue := agent.connection.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: sessionID,
		ToolCall:  acp.ToolCallUpdate{ToolCallId: toolCallID, Title: &title},
		Options:   permissionOptions(heldCall.ApprovalScope, choices),
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
	approvalgate.RecordRequesterDecision(agent.taskRunStore, taskRun.TaskRunID, &answer.Signal, "acp_permission_reload")
	if answer.Signal == agentcontract.ApprovalSignalApproveTask && strings.TrimSpace(heldCall.ApprovalScope) != "" {
		agent.taskRunStore.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventApprovalScopeGranted, approvalScopeGrantBody(heldCall.ApprovalScope))
	}
	agent.resumeAnsweredTaskRun(ctx, sessionID, sessionContext, taskRun, nil)
}

func (agent *Agent) deferHeldCall(ctx context.Context, sessionContext SessionContext, taskRun agentcontract.TaskRun, heldCall agentcontract.HeldCall, choice approvalgate.ApprovalChoice) []agentcontract.CarriedOutCall {
	approvedInput := heldCall.ApprovedInput()
	result := approvalgate.DeferralFailedResult(errNoDeferrer)
	if agent.approvalDeferrer != nil {
		deferral, errorValue := agent.approvalDeferrer.DeferApprovedCall(ctx, approvalgate.DeferralRequest{
			TaskRunID:         taskRun.TaskRunID,
			ToolName:          heldCall.ToolName,
			ToolInput:         approvedInput,
			RequesterPersonID: sessionContext.Requester.PersonID,
			Platform:          sessionContext.Addressing.Platform,
			ConversationID:    sessionContext.Addressing.ConversationID,
			ReplyTargetID:     firstNonEmpty(taskRun.OriginReplyTargetID, sessionContext.Addressing.ReplyTargetID),
			Prompt:            taskRun.Prompt,
			Choice:            choice,
			ReferenceTime:     time.Now().UTC(),
		})
		result = deferral
		if errorValue != nil {
			result = approvalgate.DeferralFailedResult(errorValue)
		}
	}
	return []agentcontract.CarriedOutCall{{ToolName: heldCall.ToolName, ToolInput: approvedInput, Result: result}}
}

func (agent *Agent) resumeAnsweredTaskRun(ctx context.Context, sessionID acp.SessionId, sessionContext SessionContext, taskRun agentcontract.TaskRun, settledCalls []agentcontract.CarriedOutCall) {
	requester := sessionContext.Requester
	addressing := sessionContext.Addressing
	launchResult, errorValue := agent.taskLauncher.Launch(ctx, agentruntime.TaskLaunchRequest{
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
	})
	if errorValue != nil {
		agent.logger.Warn("acpsession.permission.reissued_run_will_not_resume", "taskRunID", taskRun.TaskRunID, "error", errorValue.Error())
		return
	}
	agent.sendReply(ctx, sessionID, launchResult.TurnResult)
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

func approvalScopeGrantBody(approvalScope string) string {
	document, errorValue := json.Marshal(map[string]string{"scope": strings.TrimSpace(approvalScope)})
	if errorValue != nil {
		return ""
	}
	return string(document)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
