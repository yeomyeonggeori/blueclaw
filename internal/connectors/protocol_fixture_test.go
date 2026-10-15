package connectors

import (
	"encoding/json"
	"os"
	"testing"
)

func TestProtocolPlatformEventFixtureMatchesPlatformInboundEvent(t *testing.T) {
	var event PlatformInboundEvent
	if errorValue := json.Unmarshal(protocolConnectorFixture(t, "platform-inbound-event"), &event); errorValue != nil {
		t.Fatal(errorValue)
	}
	if event.ConversationID != "channel-1" || event.MessageID != "message-1" {
		t.Fatalf("unexpected platform event fixture: %#v", event)
	}
	if len(event.Context.Messages) != 1 || len(event.InputParts) != 1 {
		t.Fatalf("platform event fixture lost context or attachments: %#v", event)
	}
	if event.Context.Messages[0].SentAt.IsZero() {
		t.Fatalf("platform event fixture lost sent timestamp: %#v", event.Context.Messages[0])
	}
}

func protocolConnectorFixture(t *testing.T, fixtureName string) json.RawMessage {
	t.Helper()
	documentBytes, errorValue := os.ReadFile("../../protocol/fixtures/valid.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var fixtures map[string][]json.RawMessage
	if errorValue := json.Unmarshal(documentBytes, &fixtures); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(fixtures[fixtureName]) != 1 {
		t.Fatalf("expected one %s fixture", fixtureName)
	}
	return fixtures[fixtureName][0]
}
