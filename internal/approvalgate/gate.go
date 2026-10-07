package approvalgate

import (
	"context"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/approvalcore"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

type Gate struct {
	taskRunService         taskstate.TaskRunStore
	core                   approvalcore.Core
	questionWorder         holdrecord.QuestionWorder
	approvalTargetResolver ApprovalTargetResolver
	permissionAsker        PermissionAsker
	approvedCallScheduler  ApprovedCallScheduler
}

var eventNames = approvalcore.EventNames{
	ConfirmationRequested: agentcontract.TaskEventConfirmationRequested,
	AskRequested:          agentcontract.TaskEventAskRequested,
	WordingFailed:         TaskEventApprovalWordingFailed,
}

func New(taskRunService taskstate.TaskRunStore) *Gate {
	return &Gate{taskRunService: taskRunService, core: approvalcore.New(taskRunService, eventNames)}
}

func (gate *Gate) AwaitApproval(ctx context.Context, approvalRequest mcpserver.ApprovalRequest) (mcpserver.ApprovalOutcome, error) {
	host := &toolCallHost{gate: gate, request: approvalRequest, outcome: mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnanswerable}}
	outcome := gate.core.Await(ctx, callOf(approvalRequest), host)
	if outcome.Verdict == approvalcore.Approved {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionApproved, HoldID: outcome.HoldID}, nil
	}
	return host.outcome, nil
}

func callOf(approvalRequest mcpserver.ApprovalRequest) approvalcore.Call {
	return approvalcore.Call{
		TaskRunID:        strings.TrimSpace(approvalRequest.TaskRunID),
		ToolName:         approvalRequest.ToolName,
		ToolInput:        approvalRequest.ToolInput,
		ApprovalScope:    approvalRequest.ApprovalScope,
		SideEffectClass:  approvalRequest.SideEffectClass,
		ResponseLanguage: approvalRequest.ResponseLanguage,
		HarnessSession:   approvalRequest.HarnessSession,
	}
}

type toolCallHost struct {
	gate        *Gate
	request     mcpserver.ApprovalRequest
	profileName string
	outcome     mcpserver.ApprovalOutcome
}

func (host *toolCallHost) Prepare(ctx context.Context, call approvalcore.Call) (approvalcore.Question, bool) {
	resolution, confirmation, isQuestion := host.gate.questionToAsk(host.request)
	if !isQuestion {
		resolution = host.gate.resolveApprovalTarget(ctx, host.request)
		if resolution.namesNothingThatExists() {
			host.outcome = mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnresolvedTarget, Failure: resolution.Failure}
			return approvalcore.Question{}, false
		}
		confirmation = host.gate.confirmationWording(ctx, call, host.request, resolution)
	}
	if host.gate.permissionAsker == nil {
		host.outcome = host.gate.unreachableOutcome(call.TaskRunID, host.request, "no_asker")
		return approvalcore.Question{}, false
	}
	host.profileName = host.gate.currentAgentProfileName(call.TaskRunID)
	if _, errorValue := host.gate.taskRunService.PauseTaskRun(call.TaskRunID, agentcontract.TaskStatusWaitingApproval, confirmation); errorValue != nil {
		slog.Warn("approvalgate.call_is_unanswerable", "taskRunID", call.TaskRunID, "toolName", strings.TrimSpace(call.ToolName), "reason", errorValue.Error())
		return approvalcore.Question{}, false
	}
	return approvalcore.Question{Text: confirmation, Choices: resolution.Choices, ApprovedInput: narrowedToolInput(call.ToolInput, resolution.Target)}, true
}
