package inboundengagement

import (
	"context"
	"errors"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"log/slog"
	"strings"
	"testing"
)

type gateReturning AddressingDecision

func (decision gateReturning) Resolve(ctx context.Context, platform string, request Request) Decision {
	return resolveJudgment(ctx, request, Judgment{Addressing: AddressingDecision(decision)})
}

func resolveJudgment(ctx context.Context, request Request, judgment Judgment) Decision {
	return Resolve(ctx, slog.Default(), "buzz", request, func(context.Context) (Judgment, error) {
		return judgment, nil
	})
}

func directRequest() Request {
	return Request{ConversationType: "dm"}
}

func channelRequest() Request {
	return Request{ConversationType: "O"}
}

func TestResolveIgnoresUninvitedAttachmentsOnly(t *testing.T) {
	gate := gateReturning{Target: AddressingTargetBot, ShouldRespond: true}

	uninvitedRequest := Request{ConversationType: "O", AttachmentsOnly: true}
	decision := gate.Resolve(context.Background(), "mattermost", uninvitedRequest)
	if decision.ShouldLaunch {
		t.Fatalf("uninvited attachments-only channel post must be ignored, got %+v", decision)
	}
	if !strings.Contains(decision.IgnoreReason, "attachments_only") {
		t.Fatalf("expected attachments_only ignore reason, got %q", decision.IgnoreReason)
	}

	directRequest := Request{ConversationType: "D", AttachmentsOnly: true}
	if !gate.Resolve(context.Background(), "mattermost", directRequest).ShouldLaunch {
		t.Fatal("DM with only an attachment must still engage")
	}

	mentionRequest := Request{ConversationType: "O", AttachmentsOnly: true, BotMentioned: true}
	if !gate.Resolve(context.Background(), "mattermost", mentionRequest).ShouldLaunch {
		t.Fatal("bot-mentioned attachment-only post must still engage")
	}
}

func TestResolveReactOnly(t *testing.T) {
	gate := gateReturning{Target: AddressingTargetAnyone, ShouldRespond: false, ReactionEmoji: "eyes"}

	decision := gate.Resolve(context.Background(), "mattermost", channelRequest())

	if decision.ShouldLaunch {
		t.Fatalf("react-only message must not launch a task, got %+v", decision)
	}
	if decision.ReactionEmoji != "eyes" {
		t.Fatalf("expected react-only emoji 'eyes', got %+v", decision)
	}
}

func TestResolveReactAndRespond(t *testing.T) {
	gate := gateReturning{Target: AddressingTargetBot, ShouldRespond: true, ReactionEmoji: "+1"}

	decision := gate.Resolve(context.Background(), "mattermost", channelRequest())

	if !decision.ShouldLaunch || decision.ReactionEmoji != "+1" {
		t.Fatalf("expected react-and-respond (launch + emoji), got %+v", decision)
	}
}

func TestADirectMessageWithNothingToDoOrSayIsLeftAlone(t *testing.T) {
	decision := gateReturning{Target: AddressingTargetBot}.Resolve(context.Background(), "buzz", directRequest())

	if decision.ShouldLaunch || decision.ReactionEmoji != "" {
		t.Fatalf("a direct message that asks for nothing launched or reacted: %+v", decision)
	}
}

func TestADirectMessageThatOnlyDeservesAReactionGetsOneAndNoTurn(t *testing.T) {
	decision := gateReturning{Target: AddressingTargetBot, ReactionEmoji: "pray"}.Resolve(context.Background(), "buzz", directRequest())

	if decision.ShouldLaunch || decision.ReactionEmoji != "pray" || decision.IgnoreReason != reactionOnlyReason {
		t.Fatalf("a thanks in a direct message decided %+v, expected a reaction and no turn", decision)
	}
}

func TestADirectRequestForWorkLaunchesEvenWhenNoWordsAreWanted(t *testing.T) {
	decision := gateReturning{Target: AddressingTargetBot, Work: agentcontract.WorkEasy, ReactionEmoji: "saluting_face"}.Resolve(context.Background(), "buzz", directRequest())

	if !decision.ShouldLaunch || decision.ReactionEmoji != "saluting_face" {
		t.Fatalf("a direct request for work decided %+v, expected a turn and the reaction", decision)
	}
}

func TestADirectMessageAboutTheOpenTaskReachesItEvenWithNothingNewToDo(t *testing.T) {
	for _, judgment := range []Judgment{
		{Addressing: AddressingDecision{Target: AddressingTargetBot}, HasRelatesToActiveTask: true, RelatesToActiveTask: true},
		{Addressing: AddressingDecision{Target: AddressingTargetBot}, BusyRoute: BusyRouteSteer},
	} {
		if decision := resolveJudgment(context.Background(), directRequest(), judgment); !decision.ShouldLaunch {
			t.Fatalf("a correction or answer for the open task was dropped at the gate: %+v from %+v", decision, judgment)
		}
	}
}

func TestADirectMessageIsLaunchedWhenTheGatewayCannotDecide(t *testing.T) {
	decision := Resolve(context.Background(), slog.Default(), "buzz", directRequest(), func(context.Context) (Judgment, error) {
		return Judgment{}, errors.New("decision model unreachable")
	})

	if !decision.ShouldLaunch {
		t.Fatalf("a direct message was dropped because the gateway failed: %+v", decision)
	}
}

func TestWorkOverheardInAChannelIsNotTakenOn(t *testing.T) {
	overheard := Judgment{Addressing: AddressingDecision{Target: AddressingTargetHuman, Work: agentcontract.WorkEasy}, HasRelatesToActiveTask: true, RelatesToActiveTask: true}

	if decision := resolveJudgment(context.Background(), channelRequest(), overheard); decision.ShouldLaunch {
		t.Fatalf("work one colleague asked of another launched a turn: %+v", decision)
	}
}

func TestWorkAskedOfTheAgentInAChannelLaunches(t *testing.T) {
	addressed := gateReturning{Target: AddressingTargetBot, Work: agentcontract.WorkEasy}.Resolve(context.Background(), "buzz", channelRequest())
	mentioned := gateReturning{Target: AddressingTargetAnyone, Work: agentcontract.WorkEasy}.Resolve(context.Background(), "buzz", Request{ConversationType: "O", BotMentioned: true})

	if !addressed.ShouldLaunch || !mentioned.ShouldLaunch {
		t.Fatalf("work asked of the agent in a channel decided %+v and %+v, expected both to launch", addressed, mentioned)
	}
}

func TestALaunchHandsTheHarnessTheWorkTheGatewayJudged(t *testing.T) {
	decision := gateReturning{Target: AddressingTargetBot, Work: agentcontract.WorkNormal}.Resolve(context.Background(), "buzz", directRequest())
	if !decision.ShouldLaunch || decision.DecidedWork != agentcontract.WorkNormal {
		t.Fatalf("a direct request for normal work decided %+v, expected a launch carrying the judged work", decision)
	}

	thanks := gateReturning{Target: AddressingTargetBot, ShouldRespond: true, Work: agentcontract.WorkNone}.Resolve(context.Background(), "buzz", directRequest())
	if !thanks.ShouldLaunch || thanks.DecidedWork != agentcontract.WorkNone {
		t.Fatalf("a message wanting only words decided %+v, expected the harness to be told there is no work", thanks)
	}
}

func TestWorkOnAnOpenTaskIsLeftForThePlannerWhoSeesTheGoal(t *testing.T) {
	steering := Judgment{Addressing: AddressingDecision{Target: AddressingTargetBot, Work: agentcontract.WorkNone}, BusyRoute: BusyRouteSteer}
	decision := resolveJudgment(context.Background(), directRequest(), steering)
	if !decision.ShouldLaunch || decision.DecidedWork != "" {
		t.Fatalf("a message steering an open task decided %+v, expected a launch that leaves the work to the planner", decision)
	}
}
