package approvalgate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type recordingDeferrer struct {
	request DeferralRequest
	result  toolcontract.ToolResult
	failure error
}

func (deferrer *recordingDeferrer) DeferApprovedCall(_ context.Context, request DeferralRequest) (toolcontract.ToolResult, error) {
	deferrer.request = request
	return deferrer.result, deferrer.failure
}

var narrowedHeldCall = agentcontract.HeldCall{
	ToolName:          "event_delete",
	ToolInput:         json.RawMessage(`{"eventHint":"meeting"}`),
	ApprovedToolInput: json.RawMessage(`{"eventID":"event-1"}`),
}

func TestADeferredCallCarriesTheInputTheRequesterApproved(t *testing.T) {
	deferrer := &recordingDeferrer{result: toolcontract.ToolSuccessData("scheduled", nil)}

	carriedOutCall := DeferHeldCall(context.Background(), deferrer, narrowedHeldCall, DeferralRequest{TaskRunID: "task-1"})

	if string(deferrer.request.ToolInput) != `{"eventID":"event-1"}` || deferrer.request.ToolName != "event_delete" || deferrer.request.TaskRunID != "task-1" {
		t.Fatalf("expected the approved input to be scheduled, got %+v", deferrer.request)
	}
	if string(carriedOutCall.ToolInput) != `{"eventID":"event-1"}` || carriedOutCall.Result.Failure != nil {
		t.Fatalf("expected the approved call carried out, got %+v", carriedOutCall)
	}
}

func TestADeferralThatCannotBeScheduledFailsLoudly(t *testing.T) {
	failing := DeferHeldCall(context.Background(), &recordingDeferrer{failure: errors.New("scheduler down")}, narrowedHeldCall, DeferralRequest{})
	unconfigured := DeferHeldCall(context.Background(), nil, narrowedHeldCall, DeferralRequest{})

	if failing.Result.Failure == nil || unconfigured.Result.Failure == nil {
		t.Fatalf("expected both to report that nothing will run, got %+v %+v", failing.Result, unconfigured.Result)
	}
}

func TestADeferredCallNamesTheHoldItSettles(t *testing.T) {
	heldCall := narrowedHeldCall
	heldCall.HoldID = "hold-1"

	carriedOutCall := DeferHeldCall(context.Background(), &recordingDeferrer{result: toolcontract.ToolSuccessData("scheduled", nil)}, heldCall, DeferralRequest{})

	if carriedOutCall.HoldID != "hold-1" {
		t.Fatalf("the loop matches a carried-out call to its hold by id, got %q", carriedOutCall.HoldID)
	}
}
