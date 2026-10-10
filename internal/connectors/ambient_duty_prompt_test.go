//go:build !nobundledharness

package connectors

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func overheardTurn(ambientDuty inboundengagement.AmbientDutyContext) ConversationTurn {
	event := PlatformInboundEvent{Prompt: "9월 2일 오전 7시부터 라운지에서 촬영이 있습니다."}
	event.Context.Sender.Name = "이샘플"
	return ConversationTurn{Event: event, AmbientDuty: ambientDuty}
}

func sessionLaunchRequest(turn ConversationTurn) agentruntime.TaskLaunchRequest {
	return agentruntime.TaskLaunchRequest{
		Prompt: turn.Event.Prompt,
		CheckpointSender: func(context.Context, agentcontract.AgentCheckpoint) error {
			return nil
		},
	}
}

func TestOverheardMessageNeverBecomesTheInstruction(t *testing.T) {
	turn := overheardTurn(inboundengagement.AmbientDutyContext{IsMatch: true, Name: "calendar_upkeep", Confidence: 0.92})

	prompt := withTurnContinuation(sessionLaunchRequest(turn), turn).Prompt

	if strings.HasPrefix(strings.TrimSpace(prompt), turn.Event.Prompt) {
		t.Fatalf("expected the overheard message to be quoted material, not the instruction: %q", prompt)
	}
	for _, fragment := range []string{"Ambient duty context", "Overheard message from 이샘플", turn.Event.Prompt} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("expected prompt to contain %q, got %q", fragment, prompt)
		}
	}
}

func TestAnOverheardSessionTurnRunsAsTheDutyWithoutProgressMessages(t *testing.T) {
	ambientDuty := inboundengagement.AmbientDutyContext{IsMatch: true, Name: "calendar_upkeep", Confidence: 0.92}
	turn := overheardTurn(ambientDuty)

	launchRequest := withTurnContinuation(sessionLaunchRequest(turn), turn)

	if launchRequest.AmbientDuty != ambientDuty {
		t.Fatalf("expected the launch to carry the duty that bounds its tools, got %+v", launchRequest.AmbientDuty)
	}
	if launchRequest.CheckpointSender != nil {
		t.Fatal("expected an overheard run to post no progress messages into the room")
	}
}

func TestAddressedMessageStaysTheInstruction(t *testing.T) {
	turn := overheardTurn(inboundengagement.AmbientDutyContext{})

	launchRequest := withTurnContinuation(sessionLaunchRequest(turn), turn)

	if launchRequest.Prompt != turn.Event.Prompt {
		t.Fatalf("expected an addressed message to reach the agent unchanged, got %q", launchRequest.Prompt)
	}
	if launchRequest.CheckpointSender == nil {
		t.Fatal("expected an addressed run to keep its progress messages")
	}
}

func TestAnOverheardMessageHandsTheAgentTheAmbientTaskLevelAsAFact(t *testing.T) {
	turn := overheardTurn(inboundengagement.AmbientDutyContext{IsMatch: true, Name: "calendar_upkeep", Confidence: 0.92})

	if taskLevel := taskLevelForTurn(turn); taskLevel != ambientDutyTaskLevel {
		t.Fatalf("expected an ambient launch to carry the %q level, got %q", ambientDutyTaskLevel, taskLevel)
	}
}

func TestAnAddressedMessageCarriesNoTaskLevelForTheAgentToPlanFrom(t *testing.T) {
	turn := overheardTurn(inboundengagement.AmbientDutyContext{})

	if taskLevel := taskLevelForTurn(turn); taskLevel != "" {
		t.Fatalf("expected an addressed message to leave the level to the agent, got %q", taskLevel)
	}
}

func TestAnOverheardMessageLaunchesWithTheAmbientTaskLevel(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	turn := overheardTurn(inboundengagement.AmbientDutyContext{IsMatch: true, Name: "calendar_upkeep", Confidence: 0.92})

	launchRequest := connectorRuntime.buildTaskLaunchRequest(turn)

	if launchRequest.TaskLevel != ambientDutyTaskLevel {
		t.Fatalf("expected the launch to hand the agent the ambient level, got %q", launchRequest.TaskLevel)
	}
}

func TestTheJudgedWorkTravelsWithTheLaunch(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	turn := overheardTurn(inboundengagement.AmbientDutyContext{})
	turn.DecidedWork = agentcontract.WorkHard

	if launchRequest := connectorRuntime.buildTaskLaunchRequest(turn); launchRequest.DecidedWork != agentcontract.WorkHard {
		t.Fatalf("expected the launch to carry the judged work, got %q", launchRequest.DecidedWork)
	}
}
