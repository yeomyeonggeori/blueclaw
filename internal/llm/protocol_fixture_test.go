package llm

import (
	"encoding/json"
	"os"
	"testing"
)

func TestProtocolStructuredRequestFixtureMatchesCapabilityRequest(t *testing.T) {
	var request capabilityStructuredResponseRequestDocument
	if errorValue := json.Unmarshal(protocolLLMFixture(t, "structured-response-request"), &request); errorValue != nil {
		t.Fatal(errorValue)
	}
	if request.ExecutionMode != "auto" || request.Model != "example/router" {
		t.Fatalf("unexpected structured request fixture: %#v", request)
	}
	if request.Context == nil || request.Context.RequesterPersonID != "person-1" {
		t.Fatalf("structured request fixture lost requester context: %#v", request.Context)
	}
	if request.StructuredOutputSchema.Name != "task_list" || len(request.StructuredOutputSchema.Document) == 0 {
		t.Fatalf("structured request fixture lost output schema: %#v", request.StructuredOutputSchema)
	}
}

func protocolLLMFixture(t *testing.T, fixtureName string) json.RawMessage {
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
