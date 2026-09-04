package agentruntime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

type countingCapabilityRegistryClient struct {
	readCount    int
	responseBody string
}

func (httpClient *countingCapabilityRegistryClient) Do(request *http.Request) (*http.Response, error) {
	if request.URL.Path == "/v1/capabilities" {
		httpClient.readCount++
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(httpClient.responseBody)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func aLiveCapabilityRegistry(t *testing.T, toolNames ...string) (capability.Client, *countingCapabilityRegistryClient) {
	t.Helper()
	descriptors := make([]CapabilityToolDescriptor, 0, len(toolNames))
	for _, toolName := range toolNames {
		descriptors = append(descriptors, CapabilityToolDescriptor{Name: toolName})
	}
	served, errorValue := json.Marshal(map[string]any{
		"deviceCapabilities": completeTestCapabilityToolDescriptors(descriptors),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	registryClient := &countingCapabilityRegistryClient{responseBody: string(served)}
	return capability.Client{Endpoint: "http://capability.local", HTTPClient: registryClient}, registryClient
}

func TestCapabilityToolDefinitionsComeFromTheLiveRegistryWhenNothingIsStamped(t *testing.T) {
	capabilityClient, registryClient := aLiveCapabilityRegistry(t, "message_send")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capabilityClient, nil)

	definedNames := capabilityDescriptorNames(toolCatalogBuilder.capabilityToolDefinitions())

	if !registryContainsString(definedNames, "message_send") {
		t.Fatalf("expected the live registry to define the capability tools, got %v", definedNames)
	}
	if registryClient.readCount != 1 {
		t.Fatalf("expected one live registry read, got %d", registryClient.readCount)
	}
}

func TestALiveCapabilityToolRegistersRatherThanQuarantining(t *testing.T) {
	capabilityClient, _ := aLiveCapabilityRegistry(t, "message_send")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capabilityClient, nil)
	quarantined := []toolcontract.QuarantinedToolProvider{}
	toolCatalogBuilder.UseCapabilityQuarantineReporter(func(quarantinedProvider toolcontract.QuarantinedToolProvider) {
		quarantined = append(quarantined, quarantinedProvider)
	})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{"default": {"message_send"}}, nil)

	toolSet := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	if len(quarantined) != 0 {
		t.Fatalf("expected the live descriptors to register, got %+v", quarantined)
	}
	if !registryContainsString(toolSet.ListToolNames(), "message_send") {
		t.Fatalf("expected the live capability tool in the tool set, got %v", toolSet.ListToolNames())
	}
}

func TestCapabilityToolDefinitionsReadTheLiveRegistryOnceWhileTheSnapshotIsFresh(t *testing.T) {
	capabilityClient, registryClient := aLiveCapabilityRegistry(t, "message_send")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capabilityClient, nil)

	toolCatalogBuilder.capabilityToolDefinitions()
	toolCatalogBuilder.capabilityToolDefinitions()

	if registryClient.readCount != 1 {
		t.Fatalf("expected the fresh snapshot to answer the second build, got %d reads", registryClient.readCount)
	}
}

func TestCapabilityToolDefinitionsPreferTheStampedDescriptors(t *testing.T) {
	capabilityClient, registryClient := aLiveCapabilityRegistry(t, "message_send")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capabilityClient, []CapabilityToolDescriptor{{Name: "task_add"}})

	definedNames := capabilityDescriptorNames(toolCatalogBuilder.capabilityToolDefinitions())

	if !registryContainsString(definedNames, "task_add") || registryContainsString(definedNames, "message_send") {
		t.Fatalf("expected the stamped descriptors to define the capability tools, got %v", definedNames)
	}
	if registryClient.readCount != 0 {
		t.Fatalf("expected no live registry read while descriptors are stamped, got %d", registryClient.readCount)
	}
}

func TestBuildToolRegistryAuditSkipsTheLiveCheckWhenNothingIsStamped(t *testing.T) {
	capabilityClient, _ := aLiveCapabilityRegistry(t, "message_send")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityToolDescriptors(capabilityClient, nil)

	audit, errorValue := toolCatalogBuilder.BuildToolRegistryAudit(context.Background(), toolcontract.NewToolSet(nil))

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if audit.LiveCapabilityHash != "" {
		t.Fatalf("expected no stamp to compare the live registry against, got %+v", audit)
	}
	if audit.CapabilityDescriptorHash == "" {
		t.Fatalf("expected the audit to record the descriptors the turn was given, got %+v", audit)
	}
}
