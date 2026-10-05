package approvalgate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const (
	TaskEventApprovalChoicesOffered = "approval.choices_offered"
	TaskEventApprovalDeferred       = "approval.deferred"

	CancelChoiceKey   = "cancel"
	deferredDecision  = "defer"
	deferredEffect    = "created"
	deferredObjectKey = "schedule"
)

var errChoiceStartsAtUnreadable = errors.New("an approval choice's startsAt is not an RFC 3339 instant")

type ApprovalChoice struct {
	Key      string `json:"key"`
	StartsAt string `json:"startsAt,omitempty"`
}

func (choice ApprovalChoice) DefersTheCall() bool {
	return strings.TrimSpace(choice.StartsAt) != ""
}

type ApprovalAnswer struct {
	Signal    agentcontract.ApprovalSignal
	ChoiceKey string
}

func (answer ApprovalAnswer) ChosenFrom(choices []ApprovalChoice) (ApprovalChoice, bool) {
	return ChoiceByKey(choices, answer.ChoiceKey)
}

func ChoiceReplyOptions(choices []ApprovalChoice) []agentcontract.ChoiceReplyOption {
	options := []agentcontract.ChoiceReplyOption{}
	for _, choice := range choices {
		options = append(options, agentcontract.ChoiceReplyOption{Key: strings.TrimSpace(choice.Key), Label: choiceReplyLabel(choice)})
	}
	return append(options, agentcontract.ChoiceReplyOption{Key: CancelChoiceKey, Label: "cancel, do not run it"})
}

func choiceReplyLabel(choice ApprovalChoice) string {
	if choice.DefersTheCall() {
		return "run it at " + strings.TrimSpace(choice.StartsAt)
	}
	return "run it now"
}

func ChoiceByKey(choices []ApprovalChoice, key string) (ApprovalChoice, bool) {
	trimmedKey := strings.TrimSpace(key)
	for _, choice := range choices {
		if strings.TrimSpace(choice.Key) == trimmedKey && trimmedKey != "" {
			return choice, true
		}
	}
	return ApprovalChoice{}, false
}

type offeredChoices struct {
	ToolName  string           `json:"toolName"`
	ToolInput json.RawMessage  `json:"toolInput,omitempty"`
	Choices   []ApprovalChoice `json:"choices"`
}

func OfferedChoices(taskEvents []agentcontract.TaskEvent) []ApprovalChoice {
	heldCallKey := ""
	choices := []ApprovalChoice(nil)
	for _, taskEvent := range taskEvents {
		switch taskEvent.Name {
		case agentcontract.TaskEventApprovalPendingCall:
			heldCallKey = decodeHeldCallEventBody(taskEvent.Body).CanonicalCallKey()
			choices = nil
		case TaskEventApprovalChoicesOffered:
			offered := offeredChoices{}
			unmarshalEventBody(taskEvent.Body, &offered)
			if agentcontract.CanonicalToolCallKey(offered.ToolName, offered.ToolInput) == heldCallKey {
				choices = offered.Choices
			}
		}
	}
	return choices
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
	Choice            ApprovalChoice
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
		Prompt:        firstNonEmpty(request.Prompt, request.ToolName),
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
	recordHoldDecision(gate.taskRunService, request.TaskRunID, deferredDecision, "approval_choice")
	gate.taskRunService.AppendTaskEvent(request.TaskRunID, TaskEventApprovalDeferred, marshalEventBody(record))
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

func offeredChoicesBody(toolName string, toolInput json.RawMessage, choices []ApprovalChoice) string {
	return marshalEventBody(offeredChoices{
		ToolName:  strings.TrimSpace(toolName),
		ToolInput: toolInput,
		Choices:   choices,
	})
}
