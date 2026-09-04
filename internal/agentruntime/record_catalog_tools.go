package agentruntime

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/access"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
)

const (
	recordCatalogProviderID   = "mcp:internkim"
	recordCatalogDiscoveryAge = 5 * time.Minute
	recordCatalogTimeout      = 10 * time.Second
)

type RecordCatalogClient interface {
	DiscoverTools(context.Context, string) ([]capability.ToolDescriptor, error)
	CallTool(context.Context, string, string, json.RawMessage) (mcp.ToolResult, error)
}

type discoveredCatalog struct {
	descriptors  []capability.ToolDescriptor
	discoveredAt time.Time
}

type RecordCatalogDivergence struct {
	RequesterEmail  string
	DiscoveredOnly  []string
	StampedOnly     []string
	DiscoveryFailed string
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseRecordCatalog(recordCatalog RecordCatalogClient) {
	toolCatalogBuilder.recordCatalog = recordCatalog
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseRecordCatalogDivergenceReporter(reporter func(RecordCatalogDivergence)) {
	toolCatalogBuilder.recordCatalogDivergenceReporter = reporter
}

func (toolCatalogBuilder *ToolCatalogBuilder) discoveredRecordTools(request ToolCatalogRequest) []capability.ToolDescriptor {
	requesterEmail := strings.ToLower(strings.TrimSpace(request.RequesterEmail))
	if toolCatalogBuilder.recordCatalog == nil || requesterEmail == "" {
		return nil
	}

	if cached, isFresh := toolCatalogBuilder.freshDiscovery(requesterEmail); isFresh {
		return cached
	}

	discoveryContext, cancelDiscovery := context.WithTimeout(context.Background(), recordCatalogTimeout)
	defer cancelDiscovery()
	discovered, errorValue := toolCatalogBuilder.recordCatalog.DiscoverTools(discoveryContext, requesterEmail)
	if errorValue != nil {
		toolCatalogBuilder.reportRecordCatalogDivergence(RecordCatalogDivergence{
			RequesterEmail:  requesterEmail,
			DiscoveryFailed: errorValue.Error(),
		})
		return nil
	}

	recordAnswered := descriptorsAnsweredByTheRecord(discovered)
	toolCatalogBuilder.keepDiscovery(requesterEmail, recordAnswered)
	toolCatalogBuilder.reportRecordCatalogDivergence(divergenceBetween(
		requesterEmail,
		discovered,
		toolCatalogBuilder.capabilityToolDefinitions(),
	))
	return recordAnswered
}

func (toolCatalogBuilder *ToolCatalogBuilder) freshDiscovery(requesterEmail string) ([]capability.ToolDescriptor, bool) {
	toolCatalogBuilder.recordCatalogMutex.Lock()
	defer toolCatalogBuilder.recordCatalogMutex.Unlock()
	cached, isKnown := toolCatalogBuilder.recordCatalogByRequester[requesterEmail]
	if !isKnown || time.Since(cached.discoveredAt) > recordCatalogDiscoveryAge {
		return nil, false
	}
	return cached.descriptors, true
}

func (toolCatalogBuilder *ToolCatalogBuilder) keepDiscovery(requesterEmail string, descriptors []capability.ToolDescriptor) {
	toolCatalogBuilder.recordCatalogMutex.Lock()
	defer toolCatalogBuilder.recordCatalogMutex.Unlock()
	if toolCatalogBuilder.recordCatalogByRequester == nil {
		toolCatalogBuilder.recordCatalogByRequester = map[string]discoveredCatalog{}
	}
	toolCatalogBuilder.recordCatalogByRequester[requesterEmail] = discoveredCatalog{
		descriptors:  descriptors,
		discoveredAt: time.Now(),
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) reportRecordCatalogDivergence(divergence RecordCatalogDivergence) {
	if toolCatalogBuilder.recordCatalogDivergenceReporter == nil {
		return
	}
	if divergence.DiscoveryFailed == "" && len(divergence.DiscoveredOnly) == 0 && len(divergence.StampedOnly) == 0 {
		return
	}
	toolCatalogBuilder.recordCatalogDivergenceReporter(divergence)
}

func descriptorsAnsweredByTheRecord(descriptors []capability.ToolDescriptor) []capability.ToolDescriptor {
	answered := make([]capability.ToolDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if strings.TrimSpace(descriptor.AnsweredBy) != capability.AnsweredByRecord {
			continue
		}
		answered = append(answered, descriptor)
	}
	return answered
}

func divergenceBetween(
	requesterEmail string,
	discovered []capability.ToolDescriptor,
	stamped []capability.ToolDescriptor,
) RecordCatalogDivergence {
	stampedNames := map[string]bool{}
	for _, descriptor := range stamped {
		stampedNames[modelNameOf(descriptor)] = true
	}
	discoveredNames := map[string]bool{}
	for _, descriptor := range discovered {
		discoveredNames[modelNameOf(descriptor)] = true
	}
	return RecordCatalogDivergence{
		RequesterEmail: requesterEmail,
		DiscoveredOnly: namesMissingFrom(discoveredNames, stampedNames),
		StampedOnly:    namesMissingFrom(stampedNames, discoveredNames),
	}
}

func namesMissingFrom(names map[string]bool, other map[string]bool) []string {
	missing := []string{}
	for name := range names {
		if name != "" && !other[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func modelNameOf(descriptor capability.ToolDescriptor) string {
	if modelName := strings.TrimSpace(descriptor.ModelName); modelName != "" {
		return modelName
	}
	return strings.TrimSpace(descriptor.Name)
}

type recordCatalogToolProvider struct {
	recordCatalog RecordCatalogClient
	request       ToolCatalogRequest
	descriptors   []capability.ToolDescriptor
}

func (provider recordCatalogToolProvider) ProviderID() string {
	return recordCatalogProviderID
}

func (provider recordCatalogToolProvider) ListTools(context.Context) ([]toolcontract.BoundTool, error) {
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

func (provider recordCatalogToolProvider) boundTool(descriptor capability.ToolDescriptor) toolcontract.BoundTool {
	toolName := modelNameOf(descriptor)
	resultContract := capabilityResultContract(descriptor.ResultContract)
	return boundToolFromDescriptor(
		descriptor,
		recordCatalogProviderID+"/"+strings.TrimSpace(descriptor.CanonicalName),
		provider.ProviderID(),
		toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityAvailable},
		func(toolContext context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			if !access.CanAccess(access.Request{
				PersonAccess: provider.request.PersonAccess,
				Action:       access.ActionExecute,
				Resource:     descriptor.PolicyResource,
			}) {
				return toolcontract.ToolFailureResult(
					toolcontract.FailurePermissionDenied,
					toolcontract.FailureCodes.AccessDenied,
					"capability_access",
					"current account cannot execute this tool",
				), nil
			}
			result, errorValue := provider.recordCatalog.CallTool(
				toolContext,
				provider.request.RequesterEmail,
				toolName,
				invocation.Input,
			)
			if errorValue != nil {
				return toolcontract.ToolResult{}, errorValue
			}
			return recordCatalogToolResult(result, resultContract)
		},
	)
}

func recordCatalogToolResult(
	result mcp.ToolResult,
	resultContract *toolcontract.ToolResultContract,
) (toolcontract.ToolResult, error) {
	encoded, errorValue := json.Marshal(result)
	if errorValue != nil {
		return toolcontract.ToolResult{}, errorValue
	}
	if result.IsError {
		return toolcontract.ToolFailureWithOutput(
			toolcontract.FailureExternalService,
			toolcontract.FailureCodes.OperationFailed,
			"record_tool",
			"the record refused this call",
			json.RawMessage(encoded),
		), nil
	}
	answered := resultInsideTheEnvelope(result.StructuredContent)
	toolResult := toolcontract.ToolSuccessData(string(encoded), answered)
	toolResult.Effects = toolcontract.ProjectResourceEffects(resultContract, answered)
	return toolResult, nil
}

func resultInsideTheEnvelope(structuredContent json.RawMessage) json.RawMessage {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if len(structuredContent) == 0 || json.Unmarshal(structuredContent, &envelope) != nil {
		return structuredContent
	}
	if len(envelope.Result) == 0 {
		return structuredContent
	}
	return envelope.Result
}

func namesOf(descriptors []capability.ToolDescriptor) []string {
	names := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		names = append(names, modelNameOf(descriptor))
	}
	return names
}

func withoutToolNames(descriptors []capability.ToolDescriptor, takenToolNames []string) []capability.ToolDescriptor {
	if len(takenToolNames) == 0 {
		return descriptors
	}
	taken := map[string]bool{}
	for _, toolName := range takenToolNames {
		taken[toolName] = true
	}
	kept := make([]capability.ToolDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if taken[modelNameOf(descriptor)] {
			continue
		}
		kept = append(kept, descriptor)
	}
	return kept
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerRecordCatalogTools(
	toolRegistry *toolcontract.ToolSet,
	request ToolCatalogRequest,
	descriptors []capability.ToolDescriptor,
) {
	if len(descriptors) == 0 {
		return
	}
	quarantinedProviders, errorValue := toolRegistry.RegisterProviders(context.Background(), []toolcontract.ToolProviderRegistration{{
		Provider: recordCatalogToolProvider{
			recordCatalog: toolCatalogBuilder.recordCatalog,
			request:       request,
			descriptors:   descriptors,
		},
		Trust: toolcontract.ToolProviderExternal,
	}})
	if errorValue != nil {
		panic(errorValue)
	}
	toolCatalogBuilder.reportCapabilityQuarantines(quarantinedProviders)
}
