//go:build !nobundledharness

package approvalgate

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type countingAsker struct {
	signal               agentcontract.ApprovalSignal
	harnessOptionID      acp.PermissionOptionId
	gateQuestionCount    int
	harnessQuestionCount int
}

func (asker *countingAsker) AskPermission(context.Context, mcpserver.ApprovalRequest, PermissionQuestion) (ApprovalAnswer, AskStatus) {
	asker.gateQuestionCount++
	return ApprovalAnswer{Signal: asker.signal}, AskAnswered
}

func (asker *countingAsker) AskHarnessPermission(context.Context, mcpserver.ApprovalRequest, HarnessPermissionQuestion) (acp.RequestPermissionOutcome, AskStatus) {
	asker.harnessQuestionCount++
	return chosen(asker.harnessOptionID), AskAnswered
}

func TestAGatedCallIsAskedAboutOnceWhetherOrNotTheHarnessAsksFirst(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		harnessAsks    bool
		isApproved     bool
		expectedResult mcpserver.ApprovalDecision
	}{
		{name: "harness that does not ask, approved", isApproved: true, expectedResult: mcpserver.ApprovalDecisionApproved},
		{name: "harness that does not ask, rejected", isApproved: false, expectedResult: mcpserver.ApprovalDecisionRejected},
		{name: "harness that asks first, approved", harnessAsks: true, isApproved: true, expectedResult: mcpserver.ApprovalDecisionApproved},
		{name: "harness that asks first, rejected", harnessAsks: true, isApproved: false, expectedResult: mcpserver.ApprovalDecisionRejected},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			gate, _, taskRun := gateFixture(t)
			asker := &countingAsker{signal: agentcontract.ApprovalSignalReject, harnessOptionID: "reject-once"}
			if testCase.isApproved {
				asker.signal, asker.harnessOptionID = agentcontract.ApprovalSignalApprove, "allow-once"
			}
			gate.UsePermissionAsker(asker)
			request := approvalRequestFixture(taskRun.TaskRunID)
			if testCase.harnessAsks {
				gate.AskHarnessPermission(context.Background(), request, harnessQuestion(map[string]any{"eventID": "event-1"}))
			}

			outcome, _ := gate.AwaitApproval(context.Background(), request)

			if outcome.Decision != testCase.expectedResult {
				t.Fatalf("the call was decided %q, expected %q", outcome.Decision, testCase.expectedResult)
			}
			if asked := asker.gateQuestionCount + asker.harnessQuestionCount; testCase.isApproved && asked != 1 {
				t.Fatalf("the person was asked %d times about one approved call, expected once", asked)
			}
		})
	}
}
