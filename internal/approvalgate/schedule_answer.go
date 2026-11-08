package approvalgate

import (
	"context"
	"log/slog"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

const scheduleApprovalSource = "schedule"

type Schedules interface {
	ScheduleApprovedCall(context.Context, task.ApprovedCallScheduleRequest) (task.Schedule, error)
	ScheduleByID(scheduleID string) (task.Schedule, bool, error)
}

func (gate *Gate) UseSchedules(schedules Schedules) {
	gate.schedules = schedules
}

func (gate *Gate) isAnsweredByItsSchedule(approvalRequest mcpserver.ApprovalRequest) bool {
	scheduleID, isScheduleRun := task.ScheduleIDOfSession(approvalRequest.ConversationID)
	if !isScheduleRun || gate.schedules == nil || strings.TrimSpace(approvalRequest.TaskRunID) == "" {
		return false
	}
	schedule, isFound, errorValue := gate.schedules.ScheduleByID(scheduleID)
	if errorValue != nil {
		slog.Warn("approvalgate.schedule_unreadable", "scheduleID", scheduleID, "reason", errorValue.Error())
		return false
	}
	return isFound && schedule.IsApproved() && schedule.CreatorPersonID == strings.TrimSpace(approvalRequest.RequesterPersonID)
}

func (gate *Gate) approvedByItsSchedule(taskRunID string, approvalRequest mcpserver.ApprovalRequest, resolution ApprovalTargetResolution) mcpserver.ApprovalOutcome {
	gate.holdApprovedByItsSchedule(taskRunID, agentcontract.HeldCall{
		ToolName:          approvalRequest.ToolName,
		ToolInput:         approvalRequest.ToolInput,
		ApprovedToolInput: narrowedToolInput(approvalRequest.ToolInput, resolution.Target),
		HarnessSession:    approvalRequest.HarnessSession,
	})
	return gate.approvedOutcome(taskRunID, approvalRequest)
}

func (gate *Gate) harnessAllowedByItsSchedule(taskRunID string, heldCall agentcontract.HeldCall, options []acp.PermissionOption) (acp.RequestPermissionOutcome, bool) {
	allowOnce, isOffered := optionOfKind(options, acp.PermissionOptionKindAllowOnce)
	if !isOffered {
		return acp.RequestPermissionOutcome{}, false
	}
	gate.holdApprovedByItsSchedule(taskRunID, heldCall)
	return acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{Outcome: "selected", OptionId: allowOnce.OptionId}}, true
}

func (gate *Gate) holdApprovedByItsSchedule(taskRunID string, heldCall agentcontract.HeldCall) {
	hold := holdrecord.Open(gate.taskRunService, taskRunID, heldCall, nil)
	holdrecord.Decide(gate.taskRunService, taskRunID, hold.ID, holdrecord.DecisionApprove, scheduleApprovalSource)
}
