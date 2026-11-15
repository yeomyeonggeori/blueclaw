package approvalgate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	ApproveOptionKey = "approve"
	RejectOptionKey  = "reject"
)

var ErrQuestionWillBePostedLater = errors.New("the question could not be carried to the requester right now and stays recorded to be posted")

type QuestionPoster interface {
	PostQuestion(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question ParkedQuestion) error
}

type ParkedQuestion struct {
	HoldID       string
	Confirmation string
	Choices      []holdrecord.Choice
	HandoffID    string
}

func (gate *Gate) UseQuestionPoster(questionPoster QuestionPoster) {
	gate.questionPoster = questionPoster
}

func (gate *Gate) parkedOutcome(ctx context.Context, taskRunID string, approvalRequest mcpserver.ApprovalRequest, heldCall agentcontract.HeldCall, choices []holdrecord.Choice) mcpserver.ApprovalOutcome {
	if gate.questionPoster == nil || taskRunID == "" {
		return gate.unreachableOutcome(taskRunID, approvalRequest, "no_question_poster")
	}
	profileName := gate.currentAgentProfileName(taskRunID)
	hold, isHeld := gate.holdCall(taskRunID, approvalRequest, heldCall, choices)
	if !isHeld {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnanswerable}
	}
	errorValue := gate.questionPoster.PostQuestion(ctx, approvalRequest, ParkedQuestion{HoldID: hold.ID, Confirmation: heldCall.Confirmation, Choices: choices, HandoffID: heldCall.HandoffID})
	if errorValue != nil && !errors.Is(errorValue, ErrQuestionWillBePostedLater) {
		gate.endUnreachableHold(taskRunID, profileName)
		return gate.unreachableOutcome(taskRunID, approvalRequest, errorValue.Error())
	}
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld, Notice: heldCall.Confirmation}
}

func QuestionOffers(choices []holdrecord.Choice) []approvalreply.Offer {
	if len(choices) == 0 {
		return []approvalreply.Offer{
			{ID: ApproveOptionKey},
			{ID: RejectOptionKey, IsDeclining: true},
		}
	}
	offers := []approvalreply.Offer{}
	for _, replyOption := range ChoiceReplyOptions(choices) {
		offers = append(offers, approvalreply.Offer{ID: replyOption.Key, Name: replyOption.Label, IsDeclining: replyOption.Key == CancelChoiceKey})
	}
	if TakesOwnWords(choices) {
		offers = append(offers, approvalreply.Offer{ID: approvalreply.OwnWordsOptionID})
	}
	return offers
}

func IsDecliningOption(optionKey string) bool {
	return optionKey == RejectOptionKey || optionKey == CancelChoiceKey
}

func answerOfOption(optionKey string) ApprovalAnswer {
	switch optionKey {
	case RejectOptionKey, CancelChoiceKey:
		return ApprovalAnswer{Signal: agentcontract.ApprovalSignalReject}
	case ApproveOptionKey, approvalreply.OwnWordsOptionID:
		return ApprovalAnswer{Signal: agentcontract.ApprovalSignalApprove}
	}
	return ApprovalAnswer{Signal: agentcontract.ApprovalSignalApprove, ChoiceKey: optionKey}
}

func (gate *Gate) declinedCall(taskRunID string, heldCall agentcontract.HeldCall) *agentcontract.CarriedOutCall {
	return &agentcontract.CarriedOutCall{
		ToolName:  heldCall.ToolName,
		ToolInput: heldCall.ToolInput,
		HoldID:    heldCall.HoldID,
		Result:    rejectedCallResult(gate.declinedNotice(taskRunID)),
	}
}

func (gate *Gate) declinedNotice(taskRunID string) string {
	reply, isReplied := AnswerChosen(gate.taskRunService.ListTaskEvent(taskRunID), nil)
	if !isReplied || strings.TrimSpace(reply.Text) == "" {
		return ""
	}
	return fmt.Sprintf("The requester declined this call, replying: %q. Declining is their decision, not a failure. Do not retry the call; do what their reply asks, or end the task saying it was not done.", reply.Text)
}

var ErrAnswerSettledElsewhere = errors.New("another reader of this answer already resumed the run")

func answerTakenElsewhere(errorValue error) error {
	var illegalTransition taskstate.ErrIllegalTransition
	if errors.As(errorValue, &illegalTransition) {
		return ErrAnswerSettledElsewhere
	}
	return errorValue
}

type AnswerSettlement struct {
	Signal      agentcontract.ApprovalSignal
	SettledCall *agentcontract.CarriedOutCall
}

func (settlement AnswerSettlement) SettledCalls() []agentcontract.CarriedOutCall {
	if settlement.SettledCall == nil {
		return nil
	}
	return []agentcontract.CarriedOutCall{*settlement.SettledCall}
}

func (gate *Gate) SettleAnswer(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, optionKey string, source string) (AnswerSettlement, error) {
	taskRunID := strings.TrimSpace(approvalRequest.TaskRunID)
	taskEvents := gate.taskRunService.ListTaskEvent(taskRunID)
	if _, errorValue := gate.taskRunService.AdvanceAnsweredTaskRun(taskRunID, gate.currentAgentProfileName(taskRunID)); errorValue != nil {
		return AnswerSettlement{}, answerTakenElsewhere(errorValue)
	}
	answer := answerOfOption(strings.TrimSpace(optionKey))
	heldCall, _ := PendingHeldCall(taskEvents)
	choice, isChosen := answer.ChosenFrom(OfferedChoices(taskEvents))
	if isChosen && choice.DefersTheCall() {
		return AnswerSettlement{Signal: answer.Signal, SettledCall: gate.deferHeldCall(ctx, approvalRequest, heldCall, choice)}, nil
	}
	if isChosen {
		RecordChoiceAnswer(gate.taskRunService, taskRunID, choice)
	}
	SettleSignal(gate.taskRunService, taskRunID, &answer.Signal, source)
	if answer.Signal == agentcontract.ApprovalSignalReject {
		return AnswerSettlement{Signal: answer.Signal, SettledCall: gate.declinedCall(taskRunID, heldCall)}, nil
	}
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

const (
	movedOnSource     = "moved_on"
	unreachableSource = "unreachable"
	movedOnNotice     = "The requester replied to the question about this call without choosing any of its options, so the call was not run. Their reply is the message this turn answers. Do not run the call unless that reply asks for it; if it does, call it again and they are asked again."
)

func MoveOnFromPendingHold(taskRunStore taskstate.TaskRunStore, taskRunID string) (agentcontract.CarriedOutCall, bool) {
	pendingHold, isPending := holdrecord.LatestHold(holdrecord.Holds(taskRunStore.ListTaskEvent(taskRunID)), holdrecord.StatePending)
	if !isPending {
		return agentcontract.CarriedOutCall{}, false
	}
	holdrecord.Decide(taskRunStore, taskRunID, pendingHold.ID, holdrecord.DecisionReject, movedOnSource)
	return agentcontract.CarriedOutCall{
		ToolName:  pendingHold.Call.ToolName,
		ToolInput: pendingHold.Call.ToolInput,
		HoldID:    pendingHold.ID,
		Result:    refusedCallResult(movedOnNotice),
	}, true
}

func (gate *Gate) endUnreachableHold(taskRunID string, profileName string) {
	rejection := agentcontract.ApprovalSignalReject
	SettleSignal(gate.taskRunService, taskRunID, &rejection, unreachableSource)
	if _, errorValue := gate.taskRunService.AdvanceTaskRun(taskRunID, profileName); errorValue != nil {
		slog.Warn("approvalgate.unreachable_run_will_not_advance", "taskRunID", taskRunID, "reason", errorValue.Error())
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
)
