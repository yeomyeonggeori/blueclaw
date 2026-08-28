package connectors

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestApprovalSignalDoesNotSurviveARedirectingRoute(t *testing.T) {
	approveTask := agentcontract.ApprovalSignalApproveTask
	for _, route := range []agentcontract.TurnRoute{agentcontract.TurnRouteReviseTask, agentcontract.TurnRouteStartTask} {
		surviving := approvalSignalSurvivingRoute(&approveTask, route)
		if surviving == nil || *surviving != agentcontract.ApprovalSignalUnclear {
			t.Fatalf("route %s must not carry an approval through, got %v", route, surviving)
		}
	}
}

func TestApprovalSignalSurvivesAContinuingRoute(t *testing.T) {
	approve := agentcontract.ApprovalSignalApprove
	surviving := approvalSignalSurvivingRoute(&approve, agentcontract.TurnRouteContinueTask)
	if surviving == nil || *surviving != agentcontract.ApprovalSignalApprove {
		t.Fatalf("a continuing route keeps its approval, got %v", surviving)
	}
}

func TestRejectionIsUntouchedByRoute(t *testing.T) {
	reject := agentcontract.ApprovalSignalReject
	surviving := approvalSignalSurvivingRoute(&reject, agentcontract.TurnRouteReviseTask)
	if surviving == nil || *surviving != agentcontract.ApprovalSignalReject {
		t.Fatalf("a rejection stays a rejection, got %v", surviving)
	}
}
