package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
)

const localToolProviderID = "local"

type localToolProvider struct {
	handlerToolSet *toolcontract.ToolSet
}

type localToolDescriptorSpec struct {
	ID                   string
	ProviderID           string
	Namespace            string
	Name                 string
	PrivacyClass         string
	RequiresUserPresence bool
	WorksOffline         bool
	InputIntentSchema    json.RawMessage
	OutputSchema         json.RawMessage
	ResultContract       *toolcontract.ToolResultContract
	Visibility           string
	PolicyResource       string
	SideEffectClass      string
	RequiresApproval     bool
	Completion           toolcontract.ToolCompletion
	Idempotency          string
	Availability         toolcontract.ToolAvailability
}

var localToolDescriptorSpecs = []localToolDescriptorSpec{
	{
		ID:              "local/memory_search",
		ProviderID:      localToolProviderID,
		Namespace:       "memory",
		Name:            "memory_search",
		PrivacyClass:    "workspace_memory",
		OutputSchema:    memorySearchOutputSchema,
		ResultContract:  &toolcontract.ToolResultContract{Schema: memorySearchOutputSchema},
		Visibility:      toolcontract.ToolVisibilityModel,
		PolicyResource:  "tool:memory_search",
		SideEffectClass: toolcontract.ToolSideEffectRead,
		Completion:      toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionNone},
		Idempotency:     toolcontract.ToolIdempotencyNone,
		Availability:    localToolAvailable,
	},
	{
		ID:           "local/memory_remember",
		ProviderID:   localToolProviderID,
		Namespace:    "memory",
		Name:         "memory_remember",
		PrivacyClass: "workspace_memory",
		OutputSchema: memoryRememberOutputSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: memoryRememberOutputSchema,
			Effects: []toolcontract.ResourceEffectContract{{
				ObjectType:     "memory_update",
				Effect:         "accepted",
				ResultField:    "jobID",
				EffectIdentity: "id",
			}},
			EvidenceCondition: &toolcontract.EvidenceCondition{
				ResultField: "accepted",
				Equals:      json.RawMessage(`true`),
			},
		},
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:memory_remember",
		SideEffectClass:   toolcontract.ToolSideEffectStateChange,
		InputIntentSchema: memoryRememberInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
	{
		ID:           "local/memory_forget",
		ProviderID:   localToolProviderID,
		Namespace:    "memory",
		Name:         "memory_forget",
		PrivacyClass: "workspace_memory",
		OutputSchema: memoryForgetOutputSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: memoryForgetOutputSchema,
			Effects: []toolcontract.ResourceEffectContract{{
				ObjectType:     "memory_fact",
				Effect:         "forgotten",
				ResultField:    "forgottenFactIDs",
				EffectIdentity: "id",
			}},
		},
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:memory_forget",
		SideEffectClass:   toolcontract.ToolSideEffectStateChange,
		InputIntentSchema: memoryForgetInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
	{
		ID:                   "local/ask_input",
		ProviderID:           localToolProviderID,
		Namespace:            "ask",
		Name:                 "ask_input",
		PrivacyClass:         "user_input",
		RequiresUserPresence: true,
		WorksOffline:         true,
		OutputSchema:         askInputResultSchema,
		ResultContract:       &toolcontract.ToolResultContract{Schema: askInputResultSchema},
		Visibility:           toolcontract.ToolVisibilityModel,
		PolicyResource:       "tool:ask_input",
		SideEffectClass:      toolcontract.ToolSideEffectApproval,
		InputIntentSchema:    askInputIntentSchema,
		Completion:           toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionNone},
		Idempotency:          toolcontract.ToolIdempotencyNone,
		Availability:         localToolAvailable,
	},
	{
		ID:              "local/schedule_list",
		ProviderID:      localToolProviderID,
		Namespace:       "schedule",
		Name:            "schedule_list",
		PrivacyClass:    "workspace_schedule",
		OutputSchema:    scheduleListOutputSchema,
		ResultContract:  scheduleListResultContract(),
		Visibility:      toolcontract.ToolVisibilityModel,
		PolicyResource:  "tool:schedule_list",
		SideEffectClass: toolcontract.ToolSideEffectRead,
		Completion:      toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionNone},
		Idempotency:     toolcontract.ToolIdempotencyNone,
		Availability:    localToolAvailable,
	},
	{
		ID:                "local/schedule_create",
		ProviderID:        localToolProviderID,
		Namespace:         "schedule",
		Name:              "schedule_create",
		PrivacyClass:      "workspace_schedule",
		OutputSchema:      scheduleMutationResultSchema,
		ResultContract:    scheduleMutationResultContract("created"),
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:schedule_create",
		SideEffectClass:   toolcontract.ToolSideEffectStateChange,
		InputIntentSchema: scheduleCreateInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
	{
		ID:                "local/schedule_update",
		ProviderID:        localToolProviderID,
		Namespace:         "schedule",
		Name:              "schedule_update",
		PrivacyClass:      "workspace_schedule",
		OutputSchema:      scheduleMutationResultSchema,
		ResultContract:    scheduleMutationResultContract("updated"),
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:schedule_update",
		SideEffectClass:   toolcontract.ToolSideEffectStateChange,
		InputIntentSchema: scheduleUpdateInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
	{
		ID:                "local/schedule_cancel",
		ProviderID:        localToolProviderID,
		Namespace:         "schedule",
		Name:              "schedule_cancel",
		PrivacyClass:      "workspace_schedule",
		OutputSchema:      scheduleCancelResultSchema,
		ResultContract:    scheduleCancelResultContract(),
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:schedule_cancel",
		SideEffectClass:   toolcontract.ToolSideEffectStateChange,
		InputIntentSchema: scheduleCancelInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
	{
		ID:           "local/skill_add",
		ProviderID:   localToolProviderID,
		Namespace:    "skill",
		Name:         "skill_add",
		PrivacyClass: "workspace_skill",
		OutputSchema: skillAddResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: skillAddResultSchema,
			Effects: []toolcontract.ResourceEffectContract{{
				ObjectType:     "skill",
				Effect:         "written",
				ResultField:    "path",
				EffectIdentity: "path",
			}},
			EvidenceCondition: &toolcontract.EvidenceCondition{
				ResultField: "written",
				Equals:      json.RawMessage(`true`),
			},
		},
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:skill_add",
		SideEffectClass:   toolcontract.ToolSideEffectWorkspaceWrite,
		InputIntentSchema: skillAddInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
	{
		ID:           "local/skill_remove",
		ProviderID:   localToolProviderID,
		Namespace:    "skill",
		Name:         "skill_remove",
		PrivacyClass: "workspace_skill",
		OutputSchema: skillRemoveResultSchema,
		ResultContract: &toolcontract.ToolResultContract{
			Schema: skillRemoveResultSchema,
			Effects: []toolcontract.ResourceEffectContract{{
				ObjectType:     "skill",
				Effect:         "removed",
				ResultField:    "path",
				EffectIdentity: "path",
			}},
			EvidenceCondition: &toolcontract.EvidenceCondition{
				ResultField: "removed",
				Equals:      json.RawMessage(`true`),
			},
		},
		Visibility:        toolcontract.ToolVisibilityModel,
		PolicyResource:    "tool:skill_remove",
		SideEffectClass:   toolcontract.ToolSideEffectDestructive,
		InputIntentSchema: skillRemoveInputIntentSchema,
		Completion:        toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation},
		Idempotency:       toolcontract.ToolIdempotencyNone,
		Availability:      localToolAvailable,
	},
}

var (
	localToolAvailable = toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityAvailable}
)

func (provider localToolProvider) ProviderID() string {
	return localToolProviderID
}

func (provider localToolProvider) ListTools(context.Context) ([]toolcontract.BoundTool, error) {
	if provider.handlerToolSet == nil {
		return nil, errors.New("local tool registry is unavailable")
	}
	boundTools := make([]toolcontract.BoundTool, 0, len(provider.handlerToolSet.ListRegisteredToolNames()))
	for _, toolName := range provider.handlerToolSet.ListRegisteredToolNames() {
		spec, found := localToolDescriptorSpecForName(toolName)
		if !found {
			return nil, fmt.Errorf("local tool %s has no canonical descriptor", toolName)
		}
		if errorValue := validateLocalToolDescriptorSpec(spec); errorValue != nil {
			return nil, fmt.Errorf("invalid local tool descriptor %s: %w", toolName, errorValue)
		}
		handlerDefinition, found := provider.handlerToolSet.ToolDefinition(toolName)
		if !found || strings.TrimSpace(handlerDefinition.Description) == "" || len(handlerDefinition.InputSchema) == 0 {
			return nil, fmt.Errorf("local tool %s has an incomplete handler definition", toolName)
		}
		boundTools = append(boundTools, provider.boundTool(spec, handlerDefinition))
	}
	return boundTools, nil
}

func (provider localToolProvider) boundTool(spec localToolDescriptorSpec, handlerDefinition toolcontract.ToolDefinition) toolcontract.BoundTool {
	toolName := spec.Name
	return toolcontract.BoundTool{
		Definition: toolcontract.ToolDescriptor{
			ID:                   spec.ID,
			ProviderID:           spec.ProviderID,
			Namespace:            spec.Namespace,
			Name:                 spec.Name,
			Description:          handlerDefinition.Description,
			PrivacyClass:         spec.PrivacyClass,
			RequiresUserPresence: spec.RequiresUserPresence,
			WorksOffline:         spec.WorksOffline,
			InputSchema:          handlerDefinition.InputSchema,
			InputIntentSchema:    spec.InputIntentSchema,
			OutputSchema:         spec.OutputSchema,
			ResultContract:       spec.ResultContract,
			Visibility:           spec.Visibility,
			PolicyResource:       spec.PolicyResource,
			SideEffectClass:      spec.SideEffectClass,
			RequiresApproval:     spec.RequiresApproval,
			Completion:           spec.Completion,
			Idempotency:          spec.Idempotency,
		},
		Availability: spec.Availability,
		Handler: func(toolContext context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			invocation.ToolName = toolName
			result, errorValue := provider.handlerToolSet.InvokeInternal(toolContext, invocation)
			if errorValue == nil && !result.Failed() {
				result.Effects = toolcontract.ProjectResourceEffects(spec.ResultContract, result.Output.Data)
			}
			return result, errorValue
		},
	}
}

func localToolDescriptorSpecForName(toolName string) (localToolDescriptorSpec, bool) {
	trimmedToolName := strings.TrimSpace(toolName)
	for _, spec := range localToolDescriptorSpecs {
		if spec.Name == trimmedToolName {
			return spec, true
		}
	}
	return localToolDescriptorSpec{}, false
}

func validateLocalToolDescriptorSpec(spec localToolDescriptorSpec) error {
	if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.ProviderID) == "" || strings.TrimSpace(spec.Namespace) == "" || strings.TrimSpace(spec.Name) == "" {
		return errors.New("identity is required")
	}
	if spec.ProviderID != localToolProviderID {
		return errors.New("provider identifier is invalid")
	}
	if strings.TrimSpace(spec.PrivacyClass) == "" || strings.TrimSpace(spec.Visibility) == "" || strings.TrimSpace(spec.PolicyResource) == "" || strings.TrimSpace(spec.SideEffectClass) == "" || strings.TrimSpace(spec.Idempotency) == "" {
		return errors.New("privacy, visibility, policy, side effect, and idempotency metadata are required")
	}
	if strings.TrimSpace(spec.Completion.Mode) == "" || len(spec.OutputSchema) == 0 || strings.TrimSpace(spec.Availability.Status) == "" {
		return errors.New("completion, output schema, and availability metadata are required")
	}
	if spec.Visibility == toolcontract.ToolVisibilityModel && spec.ResultContract == nil {
		return errors.New("model-visible result contract is required")
	}
	return nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerLocalTools(toolSet *toolcontract.ToolSet, request ToolCatalogRequest, handlerContext toolHandlerContext) {
	handlerToolSet := toolcontract.NewToolSet(nil)
	toolCatalogBuilder.registerMemoryTool(handlerToolSet, request)
	toolCatalogBuilder.registerBuiltInTools(handlerToolSet, handlerContext)
	provider := localToolProvider{handlerToolSet: handlerToolSet}
	if errorValue := toolSet.RegisterProvider(context.Background(), provider); errorValue != nil {
		panic(fmt.Errorf("register trusted local tool provider: %w", errorValue))
	}
}

// LocalToolNames are the tools this runtime answers itself, whatever a product's
// catalog carries.
func LocalToolNames() []string {
	names := make([]string, 0, len(localToolDescriptorSpecs))
	for _, spec := range localToolDescriptorSpecs {
		names = append(names, spec.Name)
	}
	return names
}
