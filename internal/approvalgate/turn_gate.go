package approvalgate

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type TurnContext struct {
	RequesterPersonID string
	RequesterEmail    string
	ResponseLanguage  string
	Prompt            string
	Platform          string
	ConversationID    string
	ReplyTargetID     string
	HarnessSession    mcpserver.HarnessSession
}

type turnToolCallGate struct {
	gate        *Gate
	turnContext TurnContext
}

func (gate *Gate) TurnGate(turnContext TurnContext) toolcontract.ToolCallGate {
	return turnToolCallGate{gate: gate, turnContext: turnContext}
}

func (turnGate turnToolCallGate) ReviewToolCall(ctx context.Context, toolInvocation toolcontract.ToolInvocation, toolDefinition toolcontract.ToolDefinition) (toolcontract.ToolCallReview, error) {
	if !callNeedsApproval(toolDefinition, toolInvocation.Input) {
		return toolcontract.ToolCallReview{MayProceed: true}, nil
	}
	if repliesIntoTheConversationItWasAskedIn(toolDefinition, toolInvocation.Input) {
		return toolcontract.ToolCallReview{MayProceed: true}, nil
	}
	if toolcontract.IsDelegatedTurn(ctx) {
		return toolcontract.ToolCallReview{Result: delegatedTurnDeniedResult()}, nil
	}
	if turnGate.gate == nil {
		return toolcontract.ToolCallReview{Result: unanswerableCallResult()}, nil
	}
	outcome, errorValue := turnGate.gate.AwaitApproval(ctx, mcpserver.ApprovalRequest{
		RequesterPersonID: turnGate.turnContext.RequesterPersonID,
		RequesterEmail:    turnGate.turnContext.RequesterEmail,
		TaskRunID:         taskRunIDForCall(ctx),
		ResponseLanguage:  turnGate.turnContext.ResponseLanguage,
		Prompt:            turnGate.turnContext.Prompt,
		Platform:          turnGate.turnContext.Platform,
		ConversationID:    turnGate.turnContext.ConversationID,
		ReplyTargetID:     turnGate.turnContext.ReplyTargetID,
		ModelDraft:        toolcontract.UserFacingMessageFromContext(ctx),
		HarnessSession:    turnGate.turnContext.HarnessSession,
		ToolName:          toolDefinition.Name,
		ToolInput:         toolInvocation.Input,
		ApprovalScope:     strings.TrimSpace(toolDefinition.ApprovalScope),
		SideEffectClass:   strings.TrimSpace(toolDefinition.SideEffectClass),
	})
	if errorValue != nil {
		return toolcontract.ToolCallReview{Result: HeldCallResult(errorValue.Error())}, nil
	}
	switch outcome.Decision {
	case mcpserver.ApprovalDecisionApproved:
		return toolcontract.ToolCallReview{MayProceed: true, ApprovedCallID: outcome.ApprovedCallID}, nil
	case mcpserver.ApprovalDecisionRejected:
		return toolcontract.ToolCallReview{Result: rejectedCallResult(outcome.Notice)}, nil
	case mcpserver.ApprovalDecisionUnanswerable:
		return toolcontract.ToolCallReview{Result: unanswerableCallResult()}, nil
	case mcpserver.ApprovalDecisionUnresolvedTarget:
		return toolcontract.ToolCallReview{Result: outcome.Failure}, nil
	}
	return toolcontract.ToolCallReview{Result: HeldCallResult(outcome.Notice)}, nil
}

func taskRunIDForCall(ctx context.Context) string {
	return strings.TrimSpace(toolcontract.TaskRunIDFromContext(ctx))
}

func callNeedsApproval(toolDefinition toolcontract.ToolDefinition, toolInput json.RawMessage) bool {
	if toolDefinition.RequiresApproval {
		return true
	}
	if strings.TrimSpace(toolDefinition.Name) != toolcontract.ShellToolName {
		return false
	}
	var document struct {
		ApprovalRequired bool `json:"approvalRequired"`
	}
	return json.Unmarshal(toolInput, &document) == nil && document.ApprovalRequired
}

func repliesIntoTheConversationItWasAskedIn(toolDefinition toolcontract.ToolDefinition, toolInput json.RawMessage) bool {
	if toolcontract.ToolDefinitionSideEffectClass(toolDefinition) != toolcontract.ToolSideEffectExternalSend {
		return false
	}
	var document struct {
		TargetType string `json:"targetType"`
	}
	if len(toolInput) == 0 || json.Unmarshal(toolInput, &document) != nil {
		return false
	}
	switch strings.TrimSpace(document.TargetType) {
	case "currentThread", "currentChannel":
		return true
	}
	return false
}

func HeldCallResult(notice string) toolcontract.ToolResult {
	heldNotice := strings.TrimSpace(notice)
	if heldNotice == "" {
		heldNotice = "This call is waiting for the requester's approval and has been recorded. Do not retry it now; call it again unchanged once you are told the approval arrived, and it will run."
	}
	result := toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.InteractionRequired, "approval", heldNotice)
	result.Failure.RequiresApproval = true
	return result
}

func unanswerableCallResult() toolcontract.ToolResult {
	return refusedCallResult("This call needs the requester's approval and there is no conversation they can answer on, so it can never run. Do not wait for an approval; take another route or tell them what you could not do.")
}

func delegatedTurnDeniedResult() toolcontract.ToolResult {
	return refusedCallResult("This call needs the requester's approval, and a delegated turn has no one to ask: only the turn that was asked for the work can hold a call for approval. Do not wait for an approval; take another route, or report this back as the part you could not do.")
}

func rejectedCallResult(notice string) toolcontract.ToolResult {
	rejectedNotice := strings.TrimSpace(notice)
	if rejectedNotice == "" {
		rejectedNotice = "The requester declined this call. Do not retry it; choose another way or stop."
	}
	return refusedCallResult(rejectedNotice)
}

func refusedCallResult(notice string) toolcontract.ToolResult {
	return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.PolicyBlocked, "approval", notice)
}
