package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"slices"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type kernelHistoryProvider struct{}

func (kernelHistoryProvider) FetchHistory(context.Context, string, int) (agentcontract.VisibleContext, error) {
	return agentcontract.VisibleContext{}, nil
}

func TestKernelToolProviderUsesCanonicalDescriptors(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	provider := newKernelToolProvider(toolCatalogBuilder, toolHandlerContext{
		request: ToolCatalogRequest{HistoryProvider: kernelHistoryProvider{}},
	}, toolcontract.NewToolSet(nil))

	boundTools, errorValue := provider.ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(boundTools) != len(localKernelToolNames())-1 {
		t.Fatalf("expected local kernel palette, got %d tools", len(boundTools))
	}

	expectedToolNames := map[string]bool{}
	for _, toolName := range localKernelToolNames() {
		if toolName != toolcontract.SkillSearchToolName {
			expectedToolNames[toolName] = true
		}
	}
	for _, boundTool := range boundTools {
		descriptor := boundTool.Definition
		if !expectedToolNames[descriptor.Name] {
			t.Fatalf("unexpected local kernel tool %q", descriptor.Name)
		}
		delete(expectedToolNames, descriptor.Name)
		descriptorSpec, isFound := kernelToolDescriptorSpecForName(descriptor.Name)
		if !isFound {
			t.Fatalf("missing descriptor spec for %q", descriptor.Name)
		}
		if descriptor.ProviderID != kernelToolProviderID || descriptor.ID != kernelToolProviderID+"/"+descriptor.Name {
			t.Fatalf("expected canonical kernel identity, got %+v", descriptor)
		}
		if descriptor.Namespace != descriptorSpec.Namespace || descriptor.PrivacyClass != descriptorSpec.PrivacyClass || descriptor.Visibility != descriptorSpec.Visibility || descriptor.PolicyResource != descriptorSpec.PolicyResource {
			t.Fatalf("expected model-visible policy metadata, got %+v", descriptor)
		}
		if descriptor.SideEffectClass != descriptorSpec.SideEffectClass || descriptor.RequiresApproval != descriptorSpec.RequiresApproval {
			t.Fatalf("expected explicit side-effect metadata, got %+v", descriptor)
		}
		if descriptor.Completion.Mode != descriptorSpec.CompletionMode || descriptor.Idempotency != descriptorSpec.Idempotency {
			t.Fatalf("expected complete lifecycle metadata, got %+v", descriptor)
		}
		if len(descriptor.Description) == 0 || len(descriptor.InputSchema) == 0 || len(descriptor.OutputSchema) == 0 {
			t.Fatalf("expected schemas in descriptor, got %+v", descriptor)
		}
		var inputSchema map[string]any
		if errorValue := json.Unmarshal(descriptor.InputSchema, &inputSchema); errorValue != nil {
			t.Fatalf("expected valid input schema: %v", errorValue)
		}
		if inputSchema["type"] != "object" {
			t.Fatalf("expected object input schema, got %s", descriptor.InputSchema)
		}
		var outputSchema map[string]any
		if errorValue := json.Unmarshal(descriptor.OutputSchema, &outputSchema); errorValue != nil || outputSchema["type"] != "object" {
			t.Fatalf("expected object output schema, got %s", descriptor.OutputSchema)
		}
	}
	if len(expectedToolNames) != 0 {
		t.Fatalf("missing local kernel tools: %+v", expectedToolNames)
	}
}

func TestKernelToolProviderPassesExplicitSchemaValidation(t *testing.T) {
	provider := newKernelToolProvider(NewToolCatalogBuilder(), toolHandlerContext{
		request: ToolCatalogRequest{HistoryProvider: kernelHistoryProvider{}},
	}, toolcontract.NewToolSet(nil))
	toolSet := toolcontract.NewToolSet(nil)
	quarantinedProviders, errorValue := toolSet.RegisterProviders(context.Background(), []toolcontract.ToolProviderRegistration{{
		Provider: provider,
		Trust:    toolcontract.ToolProviderExternal,
	}})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(quarantinedProviders) != 0 {
		t.Fatalf("expected kernel provider to pass explicit schema validation, got %+v", quarantinedProviders)
	}
	if len(toolSet.ListRegisteredToolNames()) == 0 {
		t.Fatal("expected kernel tools to register")
	}
}

func TestLocalKernelToolNamesExcludeCapabilityBackedImageReader(t *testing.T) {
	expectedKernelToolNames := []string{
		toolcontract.ShellToolName,
		toolcontract.FileDeliverToolName,
		toolcontract.SkillSearchToolName,
		toolcontract.ReadToolName,
		toolcontract.FileReadToolName,
		toolcontract.FileWriteToolName,
		toolcontract.FileDeleteToolName,
		toolcontract.FileEditToolName,
		toolcontract.FilePreviewToolName,
		toolcontract.PlanUpdateToolName,
		toolcontract.RequestToolsToolName,
		toolcontract.ConversationHistoryToolName,
	}
	if len(toolcontract.KernelToolNames()) != len(expectedKernelToolNames)+1 {
		t.Fatalf("expected the kernel names to exceed local names by image_read only, got %+v", toolcontract.KernelToolNames())
	}
	if len(localKernelToolNames()) != len(expectedKernelToolNames) {
		t.Fatalf("expected every locally bound kernel tool accounted for, got %+v", localKernelToolNames())
	}
	for index, toolName := range localKernelToolNames() {
		if toolName != expectedKernelToolNames[index] {
			t.Fatalf("unexpected local kernel membership: %+v", localKernelToolNames())
		}
	}
}

func TestKernelToolsHaveCanonicalResultContracts(t *testing.T) {
	provider := newKernelToolProvider(NewToolCatalogBuilder(), toolHandlerContext{
		request: ToolCatalogRequest{HistoryProvider: kernelHistoryProvider{}},
	}, toolcontract.NewToolSet(nil))
	boundTools, errorValue := provider.ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	expectedEffectCounts := map[string]int{
		toolcontract.ShellToolName:               0,
		toolcontract.FileReadToolName:            0,
		toolcontract.FileWriteToolName:           2,
		toolcontract.FileDeleteToolName:          1,
		toolcontract.FileEditToolName:            2,
		toolcontract.FilePreviewToolName:         0,
		toolcontract.FileDeliverToolName:         1,
		toolcontract.ConversationHistoryToolName: 0,
	}
	for _, boundTool := range boundTools {
		expectedEffectCount, isContractedTool := expectedEffectCounts[boundTool.Definition.Name]
		if !isContractedTool {
			continue
		}
		contract := boundTool.Definition.ResultContract
		if contract == nil {
			t.Fatalf("expected %s result contract", boundTool.Definition.Name)
		}
		if len(contract.Effects) != expectedEffectCount {
			t.Fatalf("expected %s effect contracts, got %+v", boundTool.Definition.Name, contract.Effects)
		}
		if !equalJSONSchema(boundTool.Definition.OutputSchema, contract.Schema) {
			t.Fatalf("expected %s output and result schemas to match", boundTool.Definition.Name)
		}
		delete(expectedEffectCounts, boundTool.Definition.Name)
	}
	if len(expectedEffectCounts) != 0 {
		t.Fatalf("missing contracted kernel tools: %+v", expectedEffectCounts)
	}
}

func TestTerminalRunDescriptorUsesStrictCanonicalContract(t *testing.T) {
	provider := newKernelToolProvider(NewToolCatalogBuilder(), toolHandlerContext{}, toolcontract.NewToolSet(nil))
	boundTools, errorValue := provider.ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var definition toolcontract.ToolDefinition
	for _, boundTool := range boundTools {
		if boundTool.Definition.Name == toolcontract.ShellToolName {
			definition = boundTool.Definition
			break
		}
	}
	if definition.Name == "" || definition.ResultContract == nil || definition.ResultContract.EvidenceCondition == nil {
		t.Fatalf("expected canonical terminal descriptor, got %+v", definition)
	}
	if definition.ResultContract.EvidenceCondition.ResultField != "completed" ||
		string(definition.ResultContract.EvidenceCondition.Equals) != "true" ||
		len(definition.ResultContract.Effects) != 0 {
		t.Fatalf("expected completed terminal evidence without inferred effects, got %+v", definition.ResultContract)
	}
	var inputSchema map[string]any
	if errorValue := json.Unmarshal(definition.InputSchema, &inputSchema); errorValue != nil {
		t.Fatal(errorValue)
	}
	properties, isObject := inputSchema["properties"].(map[string]any)
	if !isObject || inputSchema["additionalProperties"] != false {
		t.Fatalf("expected closed terminal input schema, got %s", definition.InputSchema)
	}
	timeoutSchema, isObject := properties["timeoutSecond"].(map[string]any)
	if !isObject || timeoutSchema["type"] != "integer" || timeoutSchema["minimum"] != float64(1) {
		t.Fatalf("expected positive integer timeout, got %v", timeoutSchema)
	}
	expectedPropertyNames := []string{"approvalReason", "approvalRequired", "command", "timeoutSecond", "workingDirectoryPath"}
	propertyNames := make([]string, 0, len(properties))
	for propertyName := range properties {
		propertyNames = append(propertyNames, propertyName)
	}
	slices.Sort(propertyNames)
	if !slices.Equal(propertyNames, expectedPropertyNames) {
		t.Fatalf("expected shallow portable terminal fields, got %v", propertyNames)
	}
	requiredFields, isArray := inputSchema["required"].([]any)
	if !isArray || len(requiredFields) != 1 || requiredFields[0] != "command" {
		t.Fatalf("expected command as the required terminal input, got %v", inputSchema["required"])
	}
	if _, isFound := inputSchema["allOf"]; isFound {
		t.Fatalf("terminal input schema must not use allOf: %s", definition.InputSchema)
	}
	if _, isFound := inputSchema["oneOf"]; isFound {
		t.Fatalf("terminal input schema must not use oneOf: %s", definition.InputSchema)
	}
	var inputIntentSchema map[string]any
	if errorValue := json.Unmarshal(definition.InputIntentSchema, &inputIntentSchema); errorValue != nil {
		t.Fatal(errorValue)
	}
	intentProperties, isObject := inputIntentSchema["properties"].(map[string]any)
	if !isObject || len(intentProperties) != 0 || inputIntentSchema["additionalProperties"] != false {
		t.Fatalf("expected terminal execution details outside the operation intent, got %s", definition.InputIntentSchema)
	}
}

func TestFileDeliverDescriptorKeepsGeneratedArtifactOutsideOperationIntent(t *testing.T) {
	provider := newKernelToolProvider(NewToolCatalogBuilder(), toolHandlerContext{}, toolcontract.NewToolSet(nil))
	boundTools, errorValue := provider.ListTools(context.Background())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, boundTool := range boundTools {
		if boundTool.Definition.Name != toolcontract.FileDeliverToolName {
			continue
		}
		var inputIntentSchema map[string]any
		if errorValue := json.Unmarshal(boundTool.Definition.InputIntentSchema, &inputIntentSchema); errorValue != nil {
			t.Fatal(errorValue)
		}
		properties, isObject := inputIntentSchema["properties"].(map[string]any)
		if !isObject || len(properties) != 0 || inputIntentSchema["additionalProperties"] != false {
			t.Fatalf("expected generated delivery details outside the operation intent, got %s", boundTool.Definition.InputIntentSchema)
		}
		return
	}
	t.Fatal("file_deliver descriptor is missing")
}

func TestKernelToolProviderProjectsEveryResultPathEffect(t *testing.T) {
	testCases := []struct {
		toolName       string
		data           json.RawMessage
		expectedEffect []toolcontract.ResourceEffect
	}{
		{
			toolName: toolcontract.FileDeleteToolName,
			data:     json.RawMessage(`{"path":"tmp/obsolete.txt","deleted":true}`),
			expectedEffect: []toolcontract.ResourceEffect{{
				ObjectType: "file",
				Effect:     "deleted",
				Path:       "tmp/obsolete.txt",
			}},
		},
		{
			toolName: toolcontract.FileEditToolName,
			data:     json.RawMessage(`{"editedFiles":["tmp/first.md","tmp/second.md"],"editCount":2}`),
			expectedEffect: []toolcontract.ResourceEffect{
				{ObjectType: "file", Effect: "updated", Path: "tmp/first.md"},
				{ObjectType: "file", Effect: "updated", Path: "tmp/second.md"},
				{ObjectType: "workspace", Effect: "modified", Path: "tmp/first.md"},
				{ObjectType: "workspace", Effect: "modified", Path: "tmp/second.md"},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.toolName, func(t *testing.T) {
			handlerToolSet := toolcontract.NewToolSet(nil)
			handlerToolSet.RegisterTool(toolcontract.ToolDefinition{
				Name:        testCase.toolName,
				Description: "test handler",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
				return toolcontract.ToolSuccessData(string(testCase.data), testCase.data), nil
			})
			provider := kernelToolProvider{handlerToolSet: handlerToolSet}
			handlerDefinition, isFound := handlerToolSet.ToolDefinition(testCase.toolName)
			if !isFound {
				t.Fatal("expected handler definition")
			}
			boundTool, errorValue := provider.boundTool(handlerDefinition)
			if errorValue != nil {
				t.Fatal(errorValue)
			}

			result, errorValue := boundTool.Handler(context.Background(), toolcontract.ToolInvocation{
				ToolName: testCase.toolName,
				Input:    json.RawMessage(`{}`),
			})

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if len(result.Effects) != len(testCase.expectedEffect) {
				t.Fatalf("expected effects %+v, got %+v", testCase.expectedEffect, result.Effects)
			}
			for index, expectedEffect := range testCase.expectedEffect {
				if result.Effects[index] != expectedEffect {
					t.Fatalf("expected effect %+v, got %+v", expectedEffect, result.Effects[index])
				}
			}
		})
	}
}
