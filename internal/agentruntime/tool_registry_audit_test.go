package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

func TestHashCapabilityDescriptorsIncludesBroadenedFields(t *testing.T) {
	baseDescriptor := CapabilityToolDescriptor{
		Name:            "task_add",
		InputSchema:     json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		SideEffectClass: toolcontract.ToolSideEffectStateChange,
		Idempotency:     CapabilityIdempotency{Supported: true, Scope: "operation"},
	}
	baseHash := hashCapabilityDescriptors([]CapabilityToolDescriptor{baseDescriptor})

	testCases := []struct {
		name   string
		mutate func(CapabilityToolDescriptor) CapabilityToolDescriptor
	}{
		{name: "resultContract schema changes", mutate: func(descriptor CapabilityToolDescriptor) CapabilityToolDescriptor {
			descriptor.ResultContract = &CapabilityToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"additionalProperties":false}`)}
			return descriptor
		}},
		{name: "sideEffectClass changes", mutate: func(descriptor CapabilityToolDescriptor) CapabilityToolDescriptor {
			descriptor.SideEffectClass = toolcontract.ToolSideEffectRead
			return descriptor
		}},
		{name: "requiresApproval changes", mutate: func(descriptor CapabilityToolDescriptor) CapabilityToolDescriptor {
			descriptor.RequiresApproval = true
			return descriptor
		}},
		{name: "idempotency scope changes", mutate: func(descriptor CapabilityToolDescriptor) CapabilityToolDescriptor {
			descriptor.Idempotency.Scope = "different"
			return descriptor
		}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mutatedHash := hashCapabilityDescriptors([]CapabilityToolDescriptor{testCase.mutate(baseDescriptor)})
			if mutatedHash == baseHash {
				t.Fatalf("expected %s to change the descriptor hash", testCase.name)
			}
		})
	}
}

func TestBuildToolRegistryAuditRunsLiveCheckForNonMessageCapabilityDescriptors(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{
		Endpoint: "http://capability.local",
		HTTPClient: &recordingHTTPClient{responseBody: `{"deviceCapabilities":[{
			"name":"task_add",
			"inputSchema":{"type":"object","properties":{},"additionalProperties":false},
			"sideEffectClass":"state_change",
			"idempotency":{"scope":"operation"}
		}]}`},
	}, []CapabilityToolDescriptor{{Name: "task_add"}})

	audit, errorValue := toolCatalogBuilder.BuildToolRegistryAudit(context.Background(), toolcontract.NewToolSet(nil))

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if audit.LiveCapabilityHash == "" {
		t.Fatal("expected the live capability registry check to run for a non-message descriptor")
	}
}

func TestBuildToolRegistryAuditSkipsLiveCheckWithoutCapabilityDescriptors(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()

	audit, errorValue := toolCatalogBuilder.BuildToolRegistryAudit(context.Background(), toolcontract.NewToolSet(nil))

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if audit.LiveCapabilityHash != "" {
		t.Fatalf("expected no live capability hash without configured capability descriptors, got %+v", audit)
	}
}

func TestBuildToolRegistryAuditDegradesWhenLiveCapabilityRegistryIsUnavailable(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{Name: "task_add"}})

	audit, errorValue := toolCatalogBuilder.BuildToolRegistryAudit(context.Background(), toolcontract.NewToolSet(nil))

	if errorValue != nil {
		t.Fatalf("expected an unavailable live capability registry to degrade, got %v", errorValue)
	}
	if !audit.LiveRegistryUnavailable {
		t.Fatalf("expected the audit to record live registry unavailability, got %+v", audit)
	}
}

func TestBuildToolRegistryAuditServesCachedSnapshotWhenLiveFetchFails(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{Name: "task_add"}})
	toolCatalogBuilder.capabilityRegistry.keepLive([]CapabilityToolDescriptor{{Name: "task_add"}}, "snapshot-hash", "")

	audit, errorValue := toolCatalogBuilder.BuildToolRegistryAudit(context.Background(), toolcontract.NewToolSet(nil))

	if errorValue != nil {
		t.Fatalf("expected cached snapshot to serve the audit, got %v", errorValue)
	}
	if !audit.LiveRegistryServedFromCache || audit.LiveCapabilityHash != "snapshot-hash" {
		t.Fatalf("expected the audit to use the cached snapshot, got %+v", audit)
	}
}

func TestReachableCapabilityToolDefinitionsGateBrowserOnCompanionStatus(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{
		{Name: "browser_open", Namespace: "browser", RequiresRequesterDevice: true},
		{Name: "message_send", Namespace: "message"},
	})

	toolCatalogBuilder.UseCompanionStatus("unavailable")
	gatedNames := capabilityDescriptorNames(toolCatalogBuilder.reachableCapabilityToolDefinitions())
	if registryContainsString(gatedNames, "browser_open") || !registryContainsString(gatedNames, "message_send") {
		t.Fatalf("expected browser tools hidden while the companion is unavailable, got %v", gatedNames)
	}

	toolCatalogBuilder.UseCompanionStatus("available")
	openNames := capabilityDescriptorNames(toolCatalogBuilder.reachableCapabilityToolDefinitions())
	if !registryContainsString(openNames, "browser_open") {
		t.Fatalf("expected browser tools back when the companion is available, got %v", openNames)
	}
}
