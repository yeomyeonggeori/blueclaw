package app

import (
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

// Every axis the runtime reads has to survive runtime.json. A field that admind
// writes and this struct omits reads as its zero value, which turns the behaviour
// off with no error anywhere: a call that needs approval runs unasked.

func TestCapabilityToolDescriptorKeepsTheAxesTheRuntimeReads(t *testing.T) {
	var configured config.CapabilityToolDescriptor
	document := `{"name":"browser_open","requiresApproval":true}`
	if errorValue := json.Unmarshal([]byte(document), &configured); errorValue != nil {
		t.Fatalf("expected the configured descriptor to decode, got %v", errorValue)
	}

	catalogDescriptors := capabilityToolDescriptors([]config.CapabilityToolDescriptor{configured})

	if len(catalogDescriptors) != 1 {
		t.Fatalf("expected one catalog descriptor, got %d", len(catalogDescriptors))
	}
	descriptor := catalogDescriptors[0]
	if !descriptor.RequiresApproval {
		t.Error("expected requiresApproval to reach the chat approval gate")
	}
}
