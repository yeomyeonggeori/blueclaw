package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
)

type capabilityToolProvider struct {
	toolCatalogBuilder *ToolCatalogBuilder
	request            ToolCatalogRequest
	descriptors        []CapabilityToolDescriptor
}

func (provider capabilityToolProvider) ProviderID() string {
	return "capabilityd"
}

func (provider capabilityToolProvider) ListTools(context.Context) ([]toolcontract.BoundTool, error) {
	boundTools := make([]toolcontract.BoundTool, 0, len(provider.descriptors))
	for _, descriptor := range provider.descriptors {
		if errorValue := validateCapabilityToolDescriptor(descriptor); errorValue != nil {
			return nil, errorValue
		}
		if !capabilityDescriptorIsRegistered(descriptor, provider.request) {
			continue
		}
		boundTools = append(boundTools, provider.boundTool(descriptor))
	}
	return boundTools, nil
}

func validateCapabilityToolDescriptor(descriptor CapabilityToolDescriptor) error {
	requiredValues := map[string]string{
		"name":            descriptor.Name,
		"canonicalName":   descriptor.CanonicalName,
		"namespace":       descriptor.Namespace,
		"modelName":       descriptor.ModelName,
		"modelVisibility": descriptor.ModelVisibility,
		"description":     descriptor.Description,
		"privacyClass":    descriptor.PrivacyClass,
		"policyResource":  descriptor.PolicyResource,
		"sideEffectClass": descriptor.SideEffectClass,
		"availability":    descriptor.Availability.State,
	}
	for fieldName, fieldValue := range requiredValues {
		if strings.TrimSpace(fieldValue) == "" {
			return errors.New("capability descriptor " + fieldName + " is required")
		}
	}
	if capabilityToolIdempotency(descriptor.Idempotency) != toolcontract.ToolIdempotencyNone && strings.TrimSpace(descriptor.Idempotency.Scope) == "" {
		return errors.New("capability descriptor idempotency.scope is required")
	}
	if descriptor.ModelVisibility != toolcontract.ToolVisibilityModel && descriptor.ModelVisibility != toolcontract.ToolVisibilityInternal && descriptor.ModelVisibility != toolcontract.ToolVisibilityControl {
		return errors.New("capability descriptor modelVisibility is invalid")
	}
	if descriptor.ModelVisibility == toolcontract.ToolVisibilityModel && descriptor.ResultContract == nil {
		return errors.New("capability descriptor resultContract is required for model-visible tools")
	}
	toolDescriptor := toolcontract.ToolDescriptor{
		Visibility:      descriptor.ModelVisibility,
		SideEffectClass: descriptor.SideEffectClass,
	}
	if toolcontract.ToolDescriptorRequiresInputIntentSchema(toolDescriptor) && !validCapabilityObjectSchema(descriptor.InputIntentSchema) {
		return errors.New("capability descriptor inputIntentSchema is required for model-visible state-changing tools")
	}
	if len(descriptor.InputIntentSchema) > 0 && !validCapabilityObjectSchema(descriptor.InputIntentSchema) {
		return errors.New("capability descriptor inputIntentSchema must describe an object")
	}
	if descriptor.Availability.State != "ok" && descriptor.Availability.State != "not_allowed" && descriptor.Availability.State != "not_connected" && descriptor.Availability.State != "not_ready" {
		return errors.New("capability descriptor availability is invalid")
	}
	if !validCapabilityObjectSchema(descriptor.InputSchema) || !validCapabilityObjectSchema(descriptor.OutputSchema) {
		return errors.New("capability descriptor input and output schemas must describe objects")
	}
	if descriptor.ResultContract != nil && !validCapabilityObjectSchema(descriptor.ResultContract.Schema) {
		return errors.New("capability descriptor result contract schema must describe an object")
	}
	if descriptor.CompletionEvidence != nil {
		if descriptor.CompletionEvidence.Mode != "success" {
			return errors.New("capability descriptor completion evidence mode is invalid")
		}
	}
	return nil
}

func validCapabilityObjectSchema(schema json.RawMessage) bool {
	var document struct {
		Type string `json:"type"`
	}
	return len(schema) > 0 && json.Unmarshal(schema, &document) == nil && document.Type == "object"
}

func (provider capabilityToolProvider) boundTool(descriptor CapabilityToolDescriptor) toolcontract.BoundTool {
	operation := strings.TrimSpace(descriptor.CanonicalName)
	return toolcontract.BoundTool{
		Definition: toolcontract.ToolDescriptor{
			ID:                      "capabilityd/" + strings.TrimSpace(descriptor.CanonicalName),
			ProviderID:              provider.ProviderID(),
			Namespace:               strings.TrimSpace(descriptor.Namespace),
			Name:                    strings.TrimSpace(descriptor.ModelName),
			Description:             strings.TrimSpace(descriptor.Description),
			PrivacyClass:            strings.TrimSpace(descriptor.PrivacyClass),
			RequiresUserPresence:    descriptor.RequiresUserPresence,
			RequiresRequesterDevice: descriptor.RequiresRequesterDevice,
			WorksOffline:            descriptor.WorksOffline,
			InputSchema:             descriptor.InputSchema,
			InputIntentSchema:       descriptor.InputIntentSchema,
			OutputSchema:            descriptor.OutputSchema,
			ResultContract:          capabilityResultContract(descriptor.ResultContract),
			Visibility:              strings.TrimSpace(descriptor.ModelVisibility),
			PolicyResource:          strings.TrimSpace(descriptor.PolicyResource),
			SideEffectClass:         strings.TrimSpace(descriptor.SideEffectClass),
			RequiresApproval:        descriptor.RequiresApproval,
			ApprovalScope:           descriptor.ApprovalScope,
			Completion:              capabilityToolCompletion(descriptor.CompletionEvidence),
			Idempotency:             capabilityToolIdempotency(descriptor.Idempotency),
			IdempotencyScope:        strings.TrimSpace(descriptor.Idempotency.Scope),
		},
		Availability: capabilityToolAvailability(descriptor, provider.request),
		Handler: func(toolContext context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return provider.toolCatalogBuilder.invokeCapabilityOperation(
				toolContext,
				operation,
				descriptor,
				provider.request,
				invocation.Input,
			)
		},
	}
}

func capabilityResultContract(contract *CapabilityToolResultContract) *toolcontract.ToolResultContract {
	if contract == nil {
		return nil
	}
	effects := make([]toolcontract.ResourceEffectContract, 0, len(contract.Effects))
	for _, effectContract := range contract.Effects {
		effects = append(effects, toolcontract.ResourceEffectContract{
			ObjectType:     strings.TrimSpace(effectContract.ObjectType),
			Effect:         strings.TrimSpace(effectContract.Effect),
			ResultField:    strings.TrimSpace(effectContract.ResultField),
			EffectIdentity: strings.TrimSpace(effectContract.EffectIdentity),
			When:           capabilityEvidenceCondition(effectContract.When),
		})
	}
	return &toolcontract.ToolResultContract{
		Schema:            contract.Schema,
		Effects:           effects,
		EvidenceCondition: capabilityEvidenceCondition(contract.EvidenceCondition),
	}
}

func capabilityEvidenceCondition(condition *CapabilityEvidenceCondition) *toolcontract.EvidenceCondition {
	if condition == nil {
		return nil
	}
	return &toolcontract.EvidenceCondition{
		ResultField: strings.TrimSpace(condition.ResultField),
		Equals:      append(json.RawMessage{}, condition.Equals...),
	}
}

func capabilityDescriptorIsRegistered(descriptor CapabilityToolDescriptor, request ToolCatalogRequest) bool {
	return !request.IsScheduledRun || !descriptor.RequiresUserPresence
}

func capabilityToolCompletion(evidence *CapabilityCompletionEvidence) toolcontract.ToolCompletion {
	if evidence == nil {
		return toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionNone}
	}
	return toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation}
}

func capabilityToolIdempotency(idempotency CapabilityIdempotency) string {
	if idempotency.Required {
		return toolcontract.ToolIdempotencyRequired
	}
	if idempotency.Supported {
		return toolcontract.ToolIdempotencySupported
	}
	return toolcontract.ToolIdempotencyNone
}
