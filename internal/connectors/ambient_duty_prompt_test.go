//go:build !nobundledharness

package connectors

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
)

func overheardTurn(ambientDuty inboundengagement.AmbientDutyContext) ConversationTurn {
	event := PlatformInboundEvent{Prompt: "9월 2일 오전 7시부터 라운지에서 촬영이 있습니다."}
	event.Context.Sender.Name = "이샘플"
	return ConversationTurn{Event: event, AmbientDuty: ambientDuty}
}

func TestOverheardMessageNeverBecomesTheInstruction(t *testing.T) {
	turn := overheardTurn(inboundengagement.AmbientDutyContext{IsMatch: true, Name: "calendar_upkeep", Confidence: 0.92})

	prompt := promptForTurn(turn)

	if strings.HasPrefix(strings.TrimSpace(prompt), turn.Event.Prompt) {
		t.Fatalf("expected the overheard message to be quoted material, not the instruction: %q", prompt)
	}
	for _, fragment := range []string{"Ambient duty context", "Overheard message from 이샘플", turn.Event.Prompt} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("expected prompt to contain %q, got %q", fragment, prompt)
		}
	}
}

func TestAddressedMessageStaysTheInstruction(t *testing.T) {
	turn := overheardTurn(inboundengagement.AmbientDutyContext{})

	if prompt := promptForTurn(turn); prompt != turn.Event.Prompt {
		t.Fatalf("expected an addressed message to reach the agent unchanged, got %q", prompt)
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
