//go:build !nobundledharness

package approvalgate

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
)

func TestAGatedCallIsAskedAboutOnceWhetherOrNotTheHarnessAsksFirst(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		harnessAsks bool
		isApproved  bool
	}{
		{name: "harness that does not ask, approved", isApproved: true},
		{name: "harness that does not ask, rejected", isApproved: false},
		{name: "harness that asks first, approved", harnessAsks: true, isApproved: true},
		{name: "harness that asks first, rejected", harnessAsks: true, isApproved: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			gate, _, taskRun, poster := gateWithPoster(t)
			request := approvalRequestFixture(taskRun.TaskRunID)
			optionKey := RejectOptionKey
			if testCase.isApproved {
				optionKey = ApproveOptionKey
			}
			if testCase.harnessAsks {
				gate.AskHarnessPermission(context.Background(), request, harnessQuestion(map[string]any{"eventID": "event-1"}))
			} else {
				gate.AwaitApproval(context.Background(), request)
			}
			if _, errorValue := gate.SettleAnswer(context.Background(), request, optionKey, "test"); errorValue != nil {
				t.Fatalf("settle answer: %v", errorValue)
			}

			outcome, _ := gate.AwaitApproval(context.Background(), request)

			if isApproved := outcome.Decision == mcpserver.ApprovalDecisionApproved; isApproved != testCase.isApproved {
				t.Fatalf("the call was decided %q after the answer %q", outcome.Decision, optionKey)
			}
			if testCase.isApproved && len(poster.questions) != 1 {
				t.Fatalf("the person was asked %d times about one approved call, expected once", len(poster.questions))
			}
		})
	}
}
