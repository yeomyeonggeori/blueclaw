package approvalgate

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	TaskEventApprovalDeferred = "approval.deferred"

	deferredEffect    = "created"
	deferredObjectKey = "schedule"
)

var errChoiceStartsAtUnreadable = errors.New("an approval choice's startsAt is not an RFC 3339 instant")

type ApprovalAnswer struct {
	Signal    agentcontract.ApprovalSignal
	ChoiceKey string
}

func (answer ApprovalAnswer) ChosenFrom(choices []holdrecord.Choice) (holdrecord.Choice, bool) {
	return ChoiceByKey(choices, answer.ChoiceKey)
}

type ApprovedCallScheduler interface {
	ScheduleApprovedCall(context.Context, task.ApprovedCallScheduleRequest) (task.Schedule, error)
}

func (gate *Gate) UseApprovedCallScheduler(scheduler ApprovedCallScheduler) {
	gate.approvedCallScheduler = scheduler
}

type DeferralRequest struct {
	TaskRunID         string
	ToolName          string
	ToolInput         json.RawMessage
	RequesterPersonID string
	Platform          string
	ConversationID    string
	ReplyTargetID     string
	Prompt            string
	Choice            holdrecord.Choice
	ReferenceTime     time.Time
}

type deferredCallRecord struct {
	ToolName   string          `json:"toolName"`
	ToolInput  json.RawMessage `json:"toolInput,omitempty"`
	ScheduleID string          `json:"scheduleID"`
	StartsAt   string          `json:"startsAt"`
}

func (gate *Gate) DeferApprovedCall(ctx context.Context, request DeferralRequest) (toolcontract.ToolResult, error) {
	if gate.approvedCallScheduler == nil {
		return toolcontract.ToolResult{}, errors.New("this agent has no scheduler to hold an approved call for later")
	}
	startsAt, errorValue := time.Parse(time.RFC3339, strings.TrimSpace(request.Choice.StartsAt))
	if errorValue != nil {
		return toolcontract.ToolResult{}, errChoiceStartsAtUnreadable
	}
	schedule, errorValue := gate.approvedCallScheduler.ScheduleApprovedCall(ctx, task.ApprovedCallScheduleRequest{
		Call: task.ScheduleApprovedCall{
			ToolName:         request.ToolName,
			ToolInput:        request.ToolInput,
			ApproverPersonID: request.RequesterPersonID,
			ApprovedAt:       request.ReferenceTime,
			OriginTaskRunID:  request.TaskRunID,
		},
		StartsAt:      startsAt,
		Prompt:        cmp.Or(strings.TrimSpace(request.Prompt), strings.TrimSpace(request.ToolName)),
		Delivery:      task.ScheduleDeliveryBinding{Platform: request.Platform, ConversationID: request.ConversationID, ReplyTargetID: request.ReplyTargetID},
		ReferenceTime: request.ReferenceTime,
	})
	if errorValue != nil {
		return toolcontract.ToolResult{}, errorValue
	}
	record := deferredCallRecord{
		ToolName:   strings.TrimSpace(request.ToolName),
		ToolInput:  request.ToolInput,
		ScheduleID: schedule.ScheduleID,
		StartsAt:   startsAt.Format(time.RFC3339),
	}
	SettleLatest(gate.taskRunService, request.TaskRunID, holdrecord.DecisionDefer, "approval_choice")
	gate.core.Record(request.TaskRunID, TaskEventApprovalDeferred, record)
	return deferredCallResult(record), nil
}

func deferredCallResult(record deferredCallRecord) toolcontract.ToolResult {
	data, _ := json.Marshal(map[string]any{
		"status":     "scheduled",
		"scheduleID": record.ScheduleID,
		"startsAt":   record.StartsAt,
		"toolName":   record.ToolName,
		"toolInput":  record.ToolInput,
	})
	result := toolcontract.ToolSuccessData(
		"The requester approved "+record.ToolName+" to run at "+record.StartsAt+" instead of now. Schedule "+record.ScheduleID+" holds exactly this call and runs it then without asking again; cancelling that schedule cancels it. It has not run yet.",
		data,
	)
	result.Effects = []toolcontract.ResourceEffect{{ObjectType: deferredObjectKey, Effect: deferredEffect, ID: record.ScheduleID}}
	return result
}

var errNoDeferrer = errors.New("this agent was started without a scheduler for approved calls")

type ApprovedCallDeferrer interface {
	DeferApprovedCall(context.Context, DeferralRequest) (toolcontract.ToolResult, error)
}

func DeferHeldCall(ctx context.Context, deferrer ApprovedCallDeferrer, heldCall agentcontract.HeldCall, request DeferralRequest) agentcontract.CarriedOutCall {
	request.ToolName = heldCall.ToolName
	request.ToolInput = heldCall.ApprovedInput()
	return agentcontract.CarriedOutCall{ToolName: request.ToolName, ToolInput: request.ToolInput, HoldID: heldCall.HoldID, Result: deferralResult(ctx, deferrer, request)}
}

func deferralResult(ctx context.Context, deferrer ApprovedCallDeferrer, request DeferralRequest) toolcontract.ToolResult {
	if deferrer == nil {
		return DeferralFailedResult(errNoDeferrer)
	}
	result, errorValue := deferrer.DeferApprovedCall(ctx, request)
	if errorValue != nil {
		return DeferralFailedResult(errorValue)
	}
	return result
}
