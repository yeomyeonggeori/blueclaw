package connectors

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func overheardTurn(ambientDuty agentcontract.AmbientDutyContext) ConversationTurn {
	event := PlatformInboundEvent{Prompt: "9월 2일 오전 7시부터 라운지에서 촬영이 있습니다."}
	event.Context.Sender.Name = "이샘플"
	return ConversationTurn{Event: event, AmbientDuty: ambientDuty}
}

func TestOverheardMessageNeverBecomesTheInstruction(t *testing.T) {
	turn := overheardTurn(agentcontract.AmbientDutyContext{IsMatch: true, Name: "calendar_upkeep", Confidence: 0.92})

	prompt := promptForTurn(turn)

	if strings.HasPrefix(strings.TrimSpace(prompt), turn.Event.Prompt) {
		t.Fatalf("expected the overheard message to be quoted material, not the instruction: %q", prompt)
	}
	for _, fragment := range []string{"Ambient duty context", "not addressed to you", "Never send a text reply", "Overheard message from 이샘플", turn.Event.Prompt} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("expected prompt to contain %q, got %q", fragment, prompt)
		}
	}
}

func TestAddressedMessageStaysTheInstruction(t *testing.T) {
	turn := overheardTurn(agentcontract.AmbientDutyContext{})

	if prompt := promptForTurn(turn); prompt != turn.Event.Prompt {
		t.Fatalf("expected an addressed message to reach the agent unchanged, got %q", prompt)
	}
}
