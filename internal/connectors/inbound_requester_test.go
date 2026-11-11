//go:build !nobundledharness

package connectors

import (
	"context"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"

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

func TestAMessageJudgedBeforeItIsAdmittedStillNamesItsSender(t *testing.T) {
	connectorRuntime, _, adapter := recordingGatewayDecisionRuntime(t)
	decider := &requesterRecordingDecider{scriptedGatewayDecider: scriptedGatewayDecider{addressing: addressedToBot()}}
	connectorRuntime.UseGatewayDecider(decider)
	connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com"})

	if _, errorValue := connectorRuntime.judgeInboundMessage(context.Background(), adapter, testChannelInboundEvent("message-1")); errorValue != nil {
		t.Fatalf("expected the message to be judged: %v", errorValue)
	}

	if len(decider.requesterEmails) != 1 || decider.requesterEmails[0] != "invited@example.com" {
		t.Fatalf("a judgment made before admission must carry the sender, got %v", decider.requesterEmails)
	}
}

func TestABurstJudgedInOneCallNamesTheSenderOfItsNewestMessage(t *testing.T) {
	connectorRuntime, _, _ := recordingGatewayDecisionRuntime(t)
	decider := &requesterRecordingDecider{scriptedGatewayDecider: scriptedGatewayDecider{addressing: addressedToBot()}}
	connectorRuntime.UseGatewayDecider(decider)
	connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com"})
	receivedAt := time.Unix(1756800000, 0)

	connectorRuntime.decideClaimedBurst(context.Background(), []QueuedConnectorEvent{
		burstChannelQueuedEvent("message-1", receivedAt, true, "이거 정리해줘"),
		burstChannelQueuedEvent("message-2", receivedAt.Add(time.Second), true, "이어서 부탁해"),
	})

	if len(decider.requesterEmails) != 1 || decider.requesterEmails[0] != "invited@example.com" {
		t.Fatalf("a burst's one judgment must carry the sender of its newest message, got %v", decider.requesterEmails)
	}
}
