//go:build !nobundledharness

package connectors

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type requesterRecordingDecider struct {
	scriptedGatewayDecider
	requesterEmails []string
}

func (decider *requesterRecordingDecider) Decide(ctx context.Context, facts inboundengagement.Facts, observe agentcontract.LLMCallObserver) ([]inboundengagement.Judgment, error) {
	decider.requesterEmails = append(decider.requesterEmails, model.RequestContextFromContext(ctx).RequesterEmail)
	return decider.scriptedGatewayDecider.Decide(ctx, facts, observe)
}

func TestTheJudgmentOfAnInboundMessageKnowsWhoSentIt(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	decider := &requesterRecordingDecider{scriptedGatewayDecider: scriptedGatewayDecider{addressing: addressedToBot(), busyRoute: inboundengagement.BusyRouteStatus}}
	connectorRuntime.UseGatewayDecider(decider)
	seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"}, "보고서 작성")
	event := testInboundEvent("message-status")
	event.Prompt = "하고 있어?"
	expected := connectorRuntime.requesterEmailForEvent("", event)
	if authorization, errorValue := connectorRuntime.authorizeSender(context.Background(), adapter, event); errorValue == nil {
		expected = connectorRuntime.requesterEmailForEvent(authorization.PersonID, event)
	}

	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event); errorValue != nil {
		t.Fatalf("expected the message to process: %v", errorValue)
	}

	if expected == "" {
		t.Fatal("the test sender must resolve to an email for this test to mean anything")
	}
	if len(decider.requesterEmails) == 0 {
		t.Fatal("expected the message to be judged")
	}
	for _, email := range decider.requesterEmails {
		if email != expected {
			t.Fatalf("a judgment made for a message must carry its sender %q, got %v", expected, decider.requesterEmails)
		}
	}
}
