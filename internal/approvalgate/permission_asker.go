package approvalgate

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type AskStatus string

const (
	AskAnswered    AskStatus = "answered"
	AskUnreachable AskStatus = "unreachable"
	AskExpired     AskStatus = "expired"
	AskInterrupted AskStatus = "interrupted"
)

type PermissionAsker interface {
	AskPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question PermissionQuestion) (ApprovalAnswer, AskStatus)
}

type PermissionQuestion struct {
	HoldID       string
	Confirmation string
	Choices      []holdrecord.Choice
}

func (gate *Gate) UsePermissionAsker(permissionAsker PermissionAsker) {
	gate.permissionAsker = permissionAsker
}

func (gate *Gate) askedOutcome(ctx context.Context, taskRunID string, approvalRequest mcpserver.ApprovalRequest, confirmation string, resolution ApprovalTargetResolution) mcpserver.ApprovalOutcome {
	if gate.permissionAsker == nil || taskRunID == "" {
		return gate.unreachableOutcome(taskRunID, approvalRequest, "no_asker")
	}
	profileName := gate.currentAgentProfileName(taskRunID)
	hold, isHeld := gate.holdCall(taskRunID, approvalRequest, confirmation, resolution)
	if !isHeld {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnanswerable}
	}
	answer, status := gate.permissionAsker.AskPermission(ctx, approvalRequest, PermissionQuestion{HoldID: hold.ID, Confirmation: confirmation, Choices: resolution.Choices})
	switch status {
	case AskAnswered:
		return gate.answeredOutcome(ctx, taskRunID, approvalRequest, confirmation, answer)
	case AskUnreachable:
		gate.endUnansweredHold(taskRunID, profileName, status)
		return gate.unreachableOutcome(taskRunID, approvalRequest, string(status))
	case AskExpired:
		gate.endUnansweredHold(taskRunID, profileName, status)
		gate.taskRunService.AppendTaskEvent(taskRunID, TaskEventApprovalExpired, marshalEventBody(map[string]string{"toolName": strings.TrimSpace(approvalRequest.ToolName), "requesterPersonID": approvalRequest.RequesterPersonID}))
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionRejected, Notice: expiredCallNotice}
	}
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld, Notice: confirmation}
}

func (gate *Gate) answeredOutcome(ctx context.Context, taskRunID string, approvalRequest mcpserver.ApprovalRequest, confirmation string, answer ApprovalAnswer) mcpserver.ApprovalOutcome {
	settlement, errorValue := gate.SettleAnswer(ctx, approvalRequest, answer)
	switch {
	case errorValue != nil:
		slog.Warn("approvalgate.answered_run_will_not_advance", "taskRunID", taskRunID, "toolName", strings.TrimSpace(approvalRequest.ToolName), "reason", errorValue.Error())
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld, Notice: confirmation}
	case settlement.DeferredCall != nil:
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionDeferred, Deferral: settlement.DeferredCall.Result}
	case settlement.Signal == agentcontract.ApprovalSignalReject:
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionRejected}
	}
	return gate.approvedOutcome(taskRunID, approvalRequest)
}

type AnswerSettlement struct {
	Signal       agentcontract.ApprovalSignal
	DeferredCall *agentcontract.CarriedOutCall
}

func (gate *Gate) SettleAnswer(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, answer ApprovalAnswer) (AnswerSettlement, error) {
	taskRunID := strings.TrimSpace(approvalRequest.TaskRunID)
	taskEvents := gate.taskRunService.ListTaskEvent(taskRunID)
	if _, errorValue := gate.taskRunService.AdvanceTaskRun(taskRunID, gate.currentAgentProfileName(taskRunID)); errorValue != nil {
		return AnswerSettlement{}, errorValue
	}
	heldCall, _ := PendingHeldCall(taskEvents)
	choice, isChosen := answer.ChosenFrom(OfferedChoices(taskEvents))
	if isChosen && choice.DefersTheCall() {
		return AnswerSettlement{Signal: answer.Signal, DeferredCall: gate.deferHeldCall(ctx, approvalRequest, heldCall, choice)}, nil
	}
	if isChosen {
		RecordChoiceAnswer(gate.taskRunService, taskRunID, choice)
	}
	SettleSignal(gate.taskRunService, taskRunID, &answer.Signal, "acp_permission")
	return AnswerSettlement{Signal: answer.Signal}, nil
}

func (gate *Gate) deferHeldCall(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, heldCall agentcontract.HeldCall, choice holdrecord.Choice) *agentcontract.CarriedOutCall {
	carriedOutCall := DeferHeldCall(ctx, gate, heldCall, DeferralRequest{
		TaskRunID:         approvalRequest.TaskRunID,
		RequesterPersonID: approvalRequest.RequesterPersonID,
		Platform:          approvalRequest.Platform,
		ConversationID:    approvalRequest.ConversationID,
		ReplyTargetID:     approvalRequest.ReplyTargetID,
		Prompt:            approvalRequest.Prompt,
		Choice:            choice,
		ReferenceTime:     time.Now().UTC(),
	})
	return &carriedOutCall
}

func (gate *Gate) endUnansweredHold(taskRunID string, profileName string, status AskStatus) {
	rejection := agentcontract.ApprovalSignalReject
	SettleSignal(gate.taskRunService, taskRunID, &rejection, string(status))
	if _, errorValue := gate.taskRunService.AdvanceTaskRun(taskRunID, profileName); errorValue != nil {
		slog.Warn("approvalgate.unanswered_run_will_not_advance", "taskRunID", taskRunID, "status", string(status), "reason", errorValue.Error())
	}
}

func (gate *Gate) unreachableOutcome(taskRunID string, approvalRequest mcpserver.ApprovalRequest, reason string) mcpserver.ApprovalOutcome {
	slog.Warn("approvalgate.requester_cannot_be_asked", "taskRunID", taskRunID, "toolName", strings.TrimSpace(approvalRequest.ToolName), "requesterPersonID", approvalRequest.RequesterPersonID, "reason", reason)
	if taskRunID != "" {
		gate.taskRunService.AppendTaskEvent(taskRunID, TaskEventApprovalUnreachable, marshalEventBody(map[string]string{
			"toolName":          strings.TrimSpace(approvalRequest.ToolName),
			"requesterPersonID": approvalRequest.RequesterPersonID,
			"reason":            reason,
		}))
	}
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnanswerable}
}

func DeferralFailedResult(errorValue error) toolcontract.ToolResult {
	return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "approval", "The requester chose to run this call later, and it could not be scheduled, so nothing will run: "+errorValue.Error())
}

func (gate *Gate) currentAgentProfileName(taskRunID string) string {
	taskRun, isFound := gate.taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return defaultAgentProfileName
	}
	if profileName := strings.TrimSpace(taskRun.CurrentAgentProfileName); profileName != "" {
		return profileName
	}
	return defaultAgentProfileName
}

const (
	defaultAgentProfileName      = "default"
	TaskEventApprovalUnreachable = "approval.unreachable"
	TaskEventApprovalExpired     = "approval.expired"
	expiredCallNotice            = "Nobody answered the requester's approval question within 24 hours, so this call was not approved and will not run. Do not retry it; tell the requester it did not run because the question went unanswered, and what they can ask for instead."
)
