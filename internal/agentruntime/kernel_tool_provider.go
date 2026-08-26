package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
)

const kernelToolProviderID = "kernel"

type kernelToolDescriptorSpec struct {
	Name              string
	Namespace         string
	PrivacyClass      string
	Visibility        string
	PolicyResource    string
	SideEffectClass   string
	RequiresApproval  bool
	CompletionMode    string
	Idempotency       string
	InputIntentSchema json.RawMessage
	OutputSchema      json.RawMessage
	ResultContract    *toolcontract.ToolResultContract
}

var (
	fileReadResultSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string"},
			"content":{"type":"string"},
			"startLine":{"type":"integer","minimum":0},
			"endLine":{"type":"integer","minimum":0},
			"totalLines":{"type":"integer","minimum":0},
			"returnedBytes":{"type":"integer","minimum":0},
			"startByte":{"type":"integer","minimum":0},
			"endByte":{"type":"integer","minimum":0},
			"nextByte":{"type":"integer","minimum":0},
			"totalBytes":{"type":"integer","minimum":0},
			"isEndOfFile":{"type":"boolean"},
			"totalLinesKnown":{"type":"boolean"},
			"originalSizeBytes":{"type":"integer","minimum":0},
			"sizeBytes":{"type":"integer","minimum":0},
			"isTruncated":{"type":"boolean"},
			"exists":{"type":"boolean"},
			"optional":{"type":"boolean"},
			"recommendedWritePath":{"type":"string"},
			"readHint":{"type":"string"},
			"source":{"type":"string"},
			"isExactFileRead":{"type":"boolean"}
		},
		"required":["path","content","startLine","endLine","totalLines","returnedBytes","startByte","endByte","nextByte","totalBytes","isEndOfFile","totalLinesKnown","originalSizeBytes","sizeBytes","isTruncated"],
		"additionalProperties":false
		}`)
	fileWriteResultSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string","minLength":1},
			"sizeBytes":{"type":"integer","minimum":0}
		},
		"required":["path","sizeBytes"],
		"additionalProperties":false
		}`)
	fileDeleteResultSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string","minLength":1},
			"deleted":{"const":true}
		},
		"required":["path","deleted"],
		"additionalProperties":false
		}`)
	fileEditResultSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"editedFiles":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"uniqueItems":true},
			"editCount":{"type":"integer","minimum":1}
		},
		"required":["editedFiles","editCount"],
		"additionalProperties":false
		}`)
	filePreviewResultSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string"},
			"filename":{"type":"string"},
			"contentType":{"type":"string"},
			"sizeBytes":{"type":"integer","minimum":0},
			"previewFormat":{"type":"string","minLength":1},
			"markdownPreview":{"type":"string"},
			"conversionStatus":{"type":"string"},
			"conversionMessage":{"type":"string"}
		},
		"required":["path","filename","contentType","sizeBytes","previewFormat","markdownPreview","conversionStatus","conversionMessage"],
		"additionalProperties":false
		}`)
	fileDeliverResultSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"deliveredPaths":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"uniqueItems":true},
			"attachmentCount":{"type":"integer","minimum":1}
		},
		"required":["deliveredPaths","attachmentCount"],
		"additionalProperties":false
		}`)
	fileWriteInputIntentSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string"},
			"content":{"type":"string"}
		},
		"additionalProperties":false
	}`)
	fileDeleteInputIntentSchema = json.RawMessage(`{
		"type":"object",
		"properties":{"path":{"type":"string"}},
		"additionalProperties":false
	}`)
	fileEditInputIntentSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"edits":{
				"type":"array",
				"minItems":1,
				"items":{
					"type":"object",
					"properties":{
						"path":{"type":"string"},
						"oldText":{"type":"string"},
						"newText":{"type":"string"}
					},
					"additionalProperties":false
				}
			}
		},
		"additionalProperties":false
	}`)
	fileDeliverInputIntentSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
)

var kernelToolDescriptorSpecs = []kernelToolDescriptorSpec{
	{
		Name:              toolcontract.ShellToolName,
		Namespace:         "terminal",
		PrivacyClass:      "workspace",
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:shell",
		SideEffectClass:   toolcontract.ToolSideEffectWorkspaceWrite,
		CompletionMode:    toolcontract.ToolCompletionObservation,
		Idempotency:       toolcontract.ToolIdempotencyNone,
		InputIntentSchema: terminalRunInputIntentSchema,
		OutputSchema:      terminalRunResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: terminalRunResultSchema,
			EvidenceCondition: &toolcontract.EvidenceCondition{
				ResultField: "completed",
				Equals:      json.RawMessage(`true`),
			},
		},
	},
	{
		Name:              toolcontract.FileDeliverToolName,
		Namespace:         "file",
		PrivacyClass:      "workspace",
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:file_deliver",
		SideEffectClass:   toolcontract.ToolSideEffectExternalWrite,
		CompletionMode:    toolcontract.ToolCompletionObservation,
		Idempotency:       toolcontract.ToolIdempotencyNone,
		InputIntentSchema: fileDeliverInputIntentSchema,
		OutputSchema:      fileDeliverResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: fileDeliverResultSchema,
			Effects: []toolcontract.ResourceEffectContract{{
				ObjectType:     "file",
				Effect:         "attached",
				ResultField:    "deliveredPaths",
				EffectIdentity: "path",
			}},
		},
	},
	{
		Name:            toolcontract.SkillSearchToolName,
		Namespace:       "skill",
		PrivacyClass:    "workspace",
		Visibility:      toolcontract.ToolVisibilityModel,
		PolicyResource:  "tool:skill_search",
		SideEffectClass: toolcontract.ToolSideEffectRead,
		CompletionMode:  toolcontract.ToolCompletionNone,
		Idempotency:     toolcontract.ToolIdempotencyNone,
		OutputSchema:    skillSearchResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: skillSearchResultSchema,
		},
	},
	{
		Name:            toolcontract.FileReadToolName,
		Namespace:       "file",
		PrivacyClass:    "workspace",
		Visibility:      toolcontract.ToolVisibilityModel,
		PolicyResource:  "tool:file_read",
		SideEffectClass: toolcontract.ToolSideEffectRead,
		CompletionMode:  toolcontract.ToolCompletionNone,
		Idempotency:     toolcontract.ToolIdempotencyNone,
		OutputSchema:    fileReadResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: fileReadResultSchema,
		},
	},
	{
		Name:              toolcontract.FileWriteToolName,
		Namespace:         "file",
		PrivacyClass:      "workspace",
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:file_write",
		SideEffectClass:   toolcontract.ToolSideEffectWorkspaceWrite,
		CompletionMode:    toolcontract.ToolCompletionObservation,
		Idempotency:       toolcontract.ToolIdempotencyNone,
		InputIntentSchema: fileWriteInputIntentSchema,
		OutputSchema:      fileWriteResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: fileWriteResultSchema,
			Effects: []toolcontract.ResourceEffectContract{
				{
					ObjectType:     "file",
					Effect:         "created",
					ResultField:    "path",
					EffectIdentity: "path",
				},
				{
					ObjectType:     "workspace",
					Effect:         "modified",
					ResultField:    "path",
					EffectIdentity: "path",
				},
			},
		},
	},
	{
		Name:              toolcontract.FileDeleteToolName,
		Namespace:         "file",
		PrivacyClass:      "workspace",
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:file_delete",
		SideEffectClass:   toolcontract.ToolSideEffectDestructive,
		RequiresApproval:  true,
		CompletionMode:    toolcontract.ToolCompletionObservation,
		Idempotency:       toolcontract.ToolIdempotencyNone,
		InputIntentSchema: fileDeleteInputIntentSchema,
		OutputSchema:      fileDeleteResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: fileDeleteResultSchema,
			Effects: []toolcontract.ResourceEffectContract{{
				ObjectType:     "file",
				Effect:         "deleted",
				ResultField:    "path",
				EffectIdentity: "path",
			}},
		},
	},
	{
		Name:              toolcontract.FileEditToolName,
		Namespace:         "file",
		PrivacyClass:      "workspace",
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:file_edit",
		SideEffectClass:   toolcontract.ToolSideEffectWorkspaceWrite,
		CompletionMode:    toolcontract.ToolCompletionObservation,
		Idempotency:       toolcontract.ToolIdempotencyNone,
		InputIntentSchema: fileEditInputIntentSchema,
		OutputSchema:      fileEditResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: fileEditResultSchema,
			Effects: []toolcontract.ResourceEffectContract{
				{
					ObjectType:     "file",
					Effect:         "updated",
					ResultField:    "editedFiles",
					EffectIdentity: "path",
				},
				{
					ObjectType:     "workspace",
					Effect:         "modified",
					ResultField:    "editedFiles",
					EffectIdentity: "path",
				},
			},
		},
	},
	{
		Name:            toolcontract.FilePreviewToolName,
		Namespace:       "file",
		PrivacyClass:    "workspace",
		Visibility:      toolcontract.ToolVisibilityModel,
		PolicyResource:  "tool:file_preview",
		SideEffectClass: toolcontract.ToolSideEffectRead,
		CompletionMode:  toolcontract.ToolCompletionNone,
		Idempotency:     toolcontract.ToolIdempotencyNone,
		OutputSchema:    filePreviewResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: filePreviewResultSchema,
		},
	},
	{
		Name:            toolcontract.PlanUpdateToolName,
		Namespace:       "plan",
		PrivacyClass:    "workspace",
		Visibility:      toolcontract.ToolVisibilityModel,
		PolicyResource:  "tool:plan_update",
		SideEffectClass: toolcontract.ToolSideEffectNone,
		CompletionMode:  toolcontract.ToolCompletionNone,
		Idempotency:     toolcontract.ToolIdempotencyNone,
		OutputSchema:    planUpdateResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: planUpdateResultSchema,
		},
	},
	{
		Name:            toolcontract.RequestToolsToolName,
		Namespace:       "tools",
		PrivacyClass:    "workspace",
		Visibility:      toolcontract.ToolVisibilityModel,
		PolicyResource:  "tool:request_tools",
		SideEffectClass: toolcontract.ToolSideEffectNone,
		CompletionMode:  toolcontract.ToolCompletionNone,
		Idempotency:     toolcontract.ToolIdempotencyNone,
		OutputSchema:    requestToolsResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: requestToolsResultSchema,
		},
	},
	{
		Name:            toolcontract.ConversationHistoryToolName,
		Namespace:       "conversation",
		PrivacyClass:    "conversation",
		Visibility:      toolcontract.ToolVisibilityModel,
		PolicyResource:  "tool:conversation_history",
		SideEffectClass: toolcontract.ToolSideEffectRead,
		CompletionMode:  toolcontract.ToolCompletionNone,
		Idempotency:     toolcontract.ToolIdempotencyNone,
		OutputSchema:    conversationHistoryResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: conversationHistoryResultSchema,
		},
	},
}

type kernelToolProvider struct {
	handlerToolSet *toolcontract.ToolSet
}

func (provider kernelToolProvider) ProviderID() string {
	return kernelToolProviderID
}

func (provider kernelToolProvider) ListTools(context.Context) ([]toolcontract.BoundTool, error) {
	registeredToolNames := provider.handlerToolSet.ListRegisteredToolNames()
	for _, toolName := range registeredToolNames {
		if _, isFound := kernelToolDescriptorSpecForName(toolName); !isFound {
			return nil, fmt.Errorf("kernel provider registered unexpected tool %s", toolName)
		}
	}
	boundTools := make([]toolcontract.BoundTool, 0, len(registeredToolNames))
	for _, toolName := range localKernelToolNames() {
		toolDefinition, isFound := provider.handlerToolSet.ToolDefinition(toolName)
		if !isFound {
			continue
		}
		boundTool, errorValue := provider.boundTool(toolDefinition)
		if errorValue != nil {
			return nil, errorValue
		}
		boundTools = append(boundTools, boundTool)
	}
	return boundTools, nil
}

func (provider kernelToolProvider) boundTool(toolDefinition toolcontract.ToolDefinition) (toolcontract.BoundTool, error) {
	canonicalDefinition, errorValue := canonicalKernelToolDescriptor(toolDefinition)
	if errorValue != nil {
		return toolcontract.BoundTool{}, errorValue
	}
	return toolcontract.BoundTool{
		Definition: canonicalDefinition,
		Availability: toolcontract.ToolAvailability{
			Status: toolcontract.ToolAvailabilityAvailable,
		},
		Handler: func(toolContext context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			invocation.ToolName = canonicalDefinition.Name
			result, errorValue := provider.handlerToolSet.InvokeInternal(toolContext, invocation)
			if errorValue != nil || result.Failed() {
				return result, errorValue
			}
			result.Effects = toolcontract.ProjectResourceEffects(canonicalDefinition.ResultContract, result.Output.Data)
			return result, nil
		},
	}, nil
}

func localKernelToolNames() []string {
	toolNames := make([]string, 0, len(kernelToolDescriptorSpecs))
	for _, descriptorSpec := range kernelToolDescriptorSpecs {
		toolNames = append(toolNames, descriptorSpec.Name)
	}
	return toolNames
}

func kernelToolDescriptorSpecForName(toolName string) (kernelToolDescriptorSpec, bool) {
	for _, descriptorSpec := range kernelToolDescriptorSpecs {
		if descriptorSpec.Name == strings.TrimSpace(toolName) {
			return descriptorSpec, true
		}
	}
	return kernelToolDescriptorSpec{}, false
}

func canonicalKernelToolDescriptor(toolDefinition toolcontract.ToolDefinition) (toolcontract.ToolDefinition, error) {
	descriptorSpec, isFound := kernelToolDescriptorSpecForName(toolDefinition.Name)
	if !isFound {
		return toolcontract.ToolDefinition{}, errors.New("kernel descriptor is not registered: " + strings.TrimSpace(toolDefinition.Name))
	}
	if descriptorSpec.Namespace == "" || descriptorSpec.PrivacyClass == "" || descriptorSpec.Visibility == "" || descriptorSpec.PolicyResource == "" || descriptorSpec.SideEffectClass == "" || descriptorSpec.CompletionMode == "" || descriptorSpec.Idempotency == "" || len(descriptorSpec.OutputSchema) == 0 {
		return toolcontract.ToolDefinition{}, errors.New("kernel descriptor is incomplete: " + descriptorSpec.Name)
	}
	if strings.TrimSpace(toolDefinition.Description) == "" || len(toolDefinition.InputSchema) == 0 {
		return toolcontract.ToolDefinition{}, errors.New("kernel handler definition is incomplete: " + descriptorSpec.Name)
	}
	toolDefinition.ID = kernelToolProviderID + "/" + descriptorSpec.Name
	toolDefinition.ProviderID = kernelToolProviderID
	toolDefinition.Namespace = descriptorSpec.Namespace
	toolDefinition.Name = descriptorSpec.Name
	toolDefinition.PrivacyClass = descriptorSpec.PrivacyClass
	toolDefinition.Visibility = descriptorSpec.Visibility
	toolDefinition.PolicyResource = descriptorSpec.PolicyResource
	toolDefinition.SideEffectClass = descriptorSpec.SideEffectClass
	toolDefinition.RequiresApproval = descriptorSpec.RequiresApproval
	toolDefinition.Completion = toolcontract.ToolCompletion{Mode: descriptorSpec.CompletionMode}
	toolDefinition.Idempotency = descriptorSpec.Idempotency
	toolDefinition.InputIntentSchema = append(json.RawMessage{}, descriptorSpec.InputIntentSchema...)
	toolDefinition.OutputSchema = append(json.RawMessage{}, descriptorSpec.OutputSchema...)
	toolDefinition.ResultContract = copyKernelToolResultContract(descriptorSpec.ResultContract)
	return toolDefinition, nil
}

func copyKernelToolResultContract(contract *toolcontract.ToolResultContract) *toolcontract.ToolResultContract {
	if contract == nil {
		return nil
	}
	return &toolcontract.ToolResultContract{
		Schema:            append(json.RawMessage{}, contract.Schema...),
		Effects:           append([]toolcontract.ResourceEffectContract{}, contract.Effects...),
		EvidenceCondition: copyKernelEvidenceCondition(contract.EvidenceCondition),
	}
}

func copyKernelEvidenceCondition(condition *toolcontract.EvidenceCondition) *toolcontract.EvidenceCondition {
	if condition == nil {
		return nil
	}
	return &toolcontract.EvidenceCondition{
		ResultField: condition.ResultField,
		Equals:      append(json.RawMessage{}, condition.Equals...),
	}
}

func newKernelToolProvider(toolCatalogBuilder *ToolCatalogBuilder, handlerContext toolHandlerContext, availableToolSet *toolcontract.ToolSet) kernelToolProvider {
	handlerToolSet := toolcontract.NewToolSet(nil)
	toolCatalogBuilder.registerHistoryTool(handlerToolSet, handlerContext.request)
	toolCatalogBuilder.registerTerminalTools(handlerToolSet, handlerContext)
	toolCatalogBuilder.registerFileTools(handlerToolSet, handlerContext)
	toolCatalogBuilder.registerSkillSearchTool(handlerToolSet, handlerContext, availableToolSet)
	toolCatalogBuilder.registerPlanUpdateTool(handlerToolSet)
	toolCatalogBuilder.registerRequestToolsTool(handlerToolSet)
	return kernelToolProvider{handlerToolSet: handlerToolSet}
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerKernelTools(toolSet *toolcontract.ToolSet, handlerContext toolHandlerContext) {
	provider := newKernelToolProvider(toolCatalogBuilder, handlerContext, toolSet)
	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		panic(fmt.Errorf("register trusted kernel tool provider: %w", errorValue))
	}
}
