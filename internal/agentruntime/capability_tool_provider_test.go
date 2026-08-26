package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestCapabilityToolProviderRegistersGeneratedCatalogAtExternalBoundary(t *testing.T) {
	document, errorValue := os.ReadFile("../../protocol/generated/capability-tools.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var catalog struct {
		Tools []CapabilityToolDescriptor `json:"tools"`
	}
	if errorValue := json.Unmarshal(document, &catalog); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolNames := make([]string, 0, len(catalog.Tools))
	for _, descriptor := range catalog.Tools {
		toolNames = append(toolNames, descriptor.ModelName)
	}
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		descriptors:        catalog.Tools,
	}
	toolSet := toolcontract.NewToolSet(toolNames)

	quarantinedProviders, errorValue := toolSet.RegisterProviders(context.Background(), []toolcontract.ToolProviderRegistration{{
		Provider: provider,
		Trust:    toolcontract.ToolProviderExternal,
	}})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(quarantinedProviders) != 0 {
		t.Fatalf("expected generated capability catalog to pass the external boundary, got %+v", quarantinedProviders)
	}
	if len(toolSet.ListRegisteredToolDefinitions()) != len(catalog.Tools) {
		t.Fatalf("expected %d registered capability tools, got %d", len(catalog.Tools), len(toolSet.ListRegisteredToolDefinitions()))
	}
}

func TestCapabilityToolProviderRegistersCanonicalDescriptor(t *testing.T) {
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		request:            ToolCatalogRequest{},
		descriptors: []CapabilityToolDescriptor{{
			Name:              "task_add",
			CanonicalName:     "task_add",
			Namespace:         "task",
			ModelName:         "task_add",
			ModelVisibility:   toolcontract.ToolVisibilityModel,
			Description:       "Create a task.",
			PrivacyClass:      "workspace_task",
			InputSchema:       json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
			InputIntentSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"additionalProperties":false}`),
			OutputSchema:      json.RawMessage(`{"type":"object","properties":{"result":{}},"additionalProperties":false}`),
			ResultContract: &CapabilityToolResultContract{
				Schema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`),
				EvidenceCondition: &CapabilityEvidenceCondition{
					ResultField: "taskID",
					Equals:      json.RawMessage(`"task-1"`),
				},
			},
			PolicyResource:     "tool:task_add",
			SideEffectClass:    toolcontract.ToolSideEffectWorkspaceWrite,
			CompletionEvidence: &CapabilityCompletionEvidence{Mode: "success", Action: "write_task", TargetKind: "task"},
			Availability:       CapabilityAvailability{State: "ok"},
			Idempotency:        CapabilityIdempotency{Scope: "operation"},
		}},
	}
	toolSet := toolcontract.NewToolSet([]string{"task_add"})

	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		t.Fatal(errorValue)
	}
	descriptor, isFound := toolSet.ToolDefinition("task_add")
	if !isFound {
		t.Fatal("expected task_add")
	}
	if descriptor.ID != "capabilityd/task_add" || descriptor.ProviderID != "capabilityd" || descriptor.Completion.Mode != toolcontract.ToolCompletionObservation || descriptor.IdempotencyScope != "operation" {
		t.Fatalf("unexpected descriptor: %+v", descriptor)
	}
	if descriptor.ResultContract == nil || descriptor.ResultContract.EvidenceCondition == nil ||
		string(descriptor.ResultContract.EvidenceCondition.Equals) != `"task-1"` {
		t.Fatalf("expected evidence condition to survive capability binding, got %+v", descriptor.ResultContract)
	}
	expectedInputIntentSchema := `{"additionalProperties":false,"properties":{"title":{"type":"string"}},"type":"object"}`
	if string(descriptor.InputIntentSchema) != expectedInputIntentSchema {
		t.Fatalf("expected canonical input intent schema, got %s", descriptor.InputIntentSchema)
	}
}

func TestCapabilityToolProviderPreservesCanonicalReadResultContract(t *testing.T) {
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		request:            ToolCatalogRequest{},
		descriptors:        []CapabilityToolDescriptor{canonicalReadDescriptor("document_read")},
	}
	toolSet := toolcontract.NewToolSet([]string{"document_read"})

	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		t.Fatal(errorValue)
	}
	descriptor, isFound := toolSet.ToolDefinition("document_read")
	if !isFound || descriptor.ResultContract == nil {
		t.Fatalf("expected canonical read result contract, found=%v descriptor=%+v", isFound, descriptor)
	}
	if len(descriptor.ResultContract.Effects) != 0 {
		t.Fatalf("expected read result contract to prohibit effects, got %+v", descriptor.ResultContract.Effects)
	}
	if !strings.Contains(string(descriptor.ResultContract.Schema), `"format"`) || !strings.Contains(string(descriptor.ResultContract.Schema), `"truncated"`) {
		t.Fatalf("expected document result schema to survive provider binding, got %s", descriptor.ResultContract.Schema)
	}
}

func TestCapabilityToolProviderRejectsIncompleteDescriptor(t *testing.T) {
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		descriptors:        []CapabilityToolDescriptor{{Name: "task_add"}},
	}

	errorValue := toolcontract.NewToolSet(nil).RegisterProvider(context.Background(), provider)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "required") {
		t.Fatalf("expected fail-closed descriptor validation, got %v", errorValue)
	}
}

func TestCapabilityToolProviderRejectsMissingOrMalformedStateChangingInputIntentSchema(t *testing.T) {
	testCases := []struct {
		name              string
		inputIntentSchema json.RawMessage
	}{
		{name: "missing"},
		{name: "malformed", inputIntentSchema: json.RawMessage(`{"type":"string"}`)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{
				Name:            "task_add",
				SideEffectClass: toolcontract.ToolSideEffectStateChange,
			})
			descriptor.InputIntentSchema = testCase.inputIntentSchema

			errorValue := validateCapabilityToolDescriptor(descriptor)

			if errorValue == nil || !strings.Contains(errorValue.Error(), "inputIntentSchema") {
				t.Fatalf("expected input intent rejection, got %v", errorValue)
			}
		})
	}
}

func TestCapabilityToolProviderRejectsModelVisibleDescriptorWithoutResultContract(t *testing.T) {
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "task_add"})
	descriptor.ResultContract = nil
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		descriptors:        []CapabilityToolDescriptor{descriptor},
	}
	toolSet := toolcontract.NewToolSet([]string{"task_add"})

	errorValue := toolSet.RegisterProvider(context.Background(), provider)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "resultContract is required for model-visible tools") {
		t.Fatalf("expected missing result contract rejection, got %v", errorValue)
	}
	if toolSet.IsRegistered("task_add") {
		t.Fatal("expected rejected capability descriptor to remain unregistered")
	}
}

func TestCapabilityToolProviderAllowsHiddenDescriptorWithoutResultContract(t *testing.T) {
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "task_history"})
	descriptor.ModelVisibility = toolcontract.ToolVisibilityInternal
	descriptor.ResultContract = nil
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		descriptors:        []CapabilityToolDescriptor{descriptor},
	}
	toolSet := toolcontract.NewToolSet([]string{"task_history"})

	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		t.Fatal(errorValue)
	}
	if toolSet.IsRegistered("task_history") {
		t.Fatal("expected hidden capability descriptor not to be registered as a model tool")
	}
}

func TestCapabilityToolProviderRejectsMissingIdempotencyScopeWhenSupported(t *testing.T) {
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "task_add"})
	descriptor.Idempotency = CapabilityIdempotency{Supported: true}
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		descriptors:        []CapabilityToolDescriptor{descriptor},
	}

	errorValue := toolcontract.NewToolSet(nil).RegisterProvider(context.Background(), provider)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "idempotency.scope is required") {
		t.Fatalf("expected missing idempotency scope rejection, got %v", errorValue)
	}
}

func TestCapabilityToolProviderAllowsMissingIdempotencyScopeWhenIdempotencyIsNone(t *testing.T) {
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "task_add"})
	descriptor.Idempotency = CapabilityIdempotency{}
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		descriptors:        []CapabilityToolDescriptor{descriptor},
	}
	toolSet := toolcontract.NewToolSet([]string{"task_add"})

	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		t.Fatal(errorValue)
	}
	descriptorDefinition, isFound := toolSet.ToolDefinition("task_add")
	if !isFound || descriptorDefinition.Idempotency != toolcontract.ToolIdempotencyNone || descriptorDefinition.IdempotencyScope != "" {
		t.Fatalf("expected registered tool with no idempotency scope, got %+v", descriptorDefinition)
	}
}

func TestCapabilityToolProviderRejectsScalarSchema(t *testing.T) {
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "task_add"})
	descriptor.InputSchema = json.RawMessage(`{"type":"string"}`)
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		descriptors:        []CapabilityToolDescriptor{descriptor},
	}

	errorValue := toolcontract.NewToolSet(nil).RegisterProvider(context.Background(), provider)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "must describe objects") {
		t.Fatalf("expected scalar schema rejection, got %v", errorValue)
	}
}

func TestToolCatalogReportsEveryCapabilityQuarantine(t *testing.T) {
	reportedProviders := []toolcontract.QuarantinedToolProvider{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseCapabilityQuarantineReporter(func(quarantinedProvider toolcontract.QuarantinedToolProvider) {
		reportedProviders = append(reportedProviders, quarantinedProvider)
	})
	expectedProviders := []toolcontract.QuarantinedToolProvider{
		{ProviderID: "capabilityd", Reason: "tool name collides with a trusted provider: file_read"},
	}

	toolCatalogBuilder.reportCapabilityQuarantines(expectedProviders)

	if !reflect.DeepEqual(reportedProviders, expectedProviders) {
		t.Fatalf("expected every capability quarantine report, got %+v", reportedProviders)
	}
}

func TestCapabilityToolProviderRejectsUnknownCompletionEvidenceMode(t *testing.T) {
	descriptor := completeTestCapabilityToolDescriptor(CapabilityToolDescriptor{Name: "task_add"})
	descriptor.CompletionEvidence = &CapabilityCompletionEvidence{Mode: "unknown"}
	provider := capabilityToolProvider{
		toolCatalogBuilder: NewToolCatalogBuilder(),
		descriptors:        []CapabilityToolDescriptor{descriptor},
	}

	errorValue := toolcontract.NewToolSet(nil).RegisterProvider(context.Background(), provider)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "completion evidence mode is invalid") {
		t.Fatalf("expected unknown completion evidence mode rejection, got %v", errorValue)
	}
}
