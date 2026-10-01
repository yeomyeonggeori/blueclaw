package agentruntime

import (
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

func TestCapabilityDescriptorCarriesTheRequesterDeviceAxisFromTheWire(t *testing.T) {
	var descriptor CapabilityToolDescriptor
	if errorValue := json.Unmarshal([]byte(`{"name":"browser_open","namespace":"browser","requiresRequesterDevice":true}`), &descriptor); errorValue != nil {
		t.Fatalf("expected the registry descriptor to decode, got %v", errorValue)
	}
	if !descriptor.RequiresRequesterDevice {
		t.Fatal("expected requiresRequesterDevice to reach the descriptor the reachability gate reads")
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{descriptor})
	if reachable := toolCatalogBuilder.reachableCapabilityToolDefinitions(); len(reachable) != 0 {
		t.Fatalf("expected a tool that needs the requester's own device to be unreachable, got %v", capabilityDescriptorNames(reachable))
	}
}
