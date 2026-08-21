package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/access"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

func capabilityToolIdempotencyKey(toolContext context.Context, descriptor CapabilityToolDescriptor) string {
	if !descriptor.Idempotency.Supported {
		return ""
	}
	taskRunID := strings.TrimSpace(toolcontract.TaskRunIDFromContext(toolContext))
	observationID := strings.TrimSpace(toolcontract.ObservationIDFromContext(toolContext))
	if taskRunID == "" || observationID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(taskRunID + ":" + observationID + ":" + strings.TrimSpace(descriptor.CanonicalName)))
	return hex.EncodeToString(digest[:])
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerMCPTools(toolRegistry *toolcontract.ToolSet, request ToolCatalogRequest) {
	quarantinedProviders, errorValue := toolRegistry.RegisterProviders(context.Background(), mcpToolProviders(toolCatalogBuilder.mcpRegistry, request))
	if errorValue != nil {
		panic(errorValue)
	}
	toolCatalogBuilder.reportMCPQuarantines(quarantinedProviders)
}

func (toolCatalogBuilder *ToolCatalogBuilder) reportMCPQuarantines(quarantinedProviders []toolcontract.QuarantinedToolProvider) {
	if toolCatalogBuilder.mcpQuarantineReporter == nil {
		return
	}
	for _, quarantinedProvider := range quarantinedProviders {
		toolCatalogBuilder.mcpQuarantineReporter(quarantinedProvider)
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerCapabilityTools(toolRegistry *toolcontract.ToolSet, request ToolCatalogRequest) {
	provider := capabilityToolProvider{
		toolCatalogBuilder: toolCatalogBuilder,
		request:            request,
		descriptors:        toolCatalogBuilder.reachableCapabilityToolDefinitions(),
	}
	quarantinedProviders, errorValue := toolRegistry.RegisterProviders(context.Background(), []toolcontract.ToolProviderRegistration{{
		Provider: provider,
		Trust:    toolcontract.ToolProviderExternal,
	}})
	if errorValue != nil {
		panic(errorValue)
	}
	toolCatalogBuilder.reportCapabilityQuarantines(quarantinedProviders)
}

func (toolCatalogBuilder *ToolCatalogBuilder) reportCapabilityQuarantines(quarantinedProviders []toolcontract.QuarantinedToolProvider) {
	if toolCatalogBuilder.capabilityQuarantineReporter == nil {
		return
	}
	for _, quarantinedProvider := range quarantinedProviders {
		toolCatalogBuilder.capabilityQuarantineReporter(quarantinedProvider)
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) invokeCapabilityOperation(toolContext context.Context, operation string, toolDescriptor CapabilityToolDescriptor, request ToolCatalogRequest, rawInput json.RawMessage) (toolcontract.ToolResult, error) {
	var response struct {
		Provider        string                        `json:"provider"`
		SelectedBackend string                        `json:"selectedBackend"`
		ToolName        string                        `json:"toolName"`
		Outcome         string                        `json:"outcome"`
		Effects         []toolcontract.ResourceEffect `json:"effects"`
		Content         string                        `json:"content"`
		IsError         bool                          `json:"isError"`
		Status          string                        `json:"status"`
		Message         string                        `json:"message"`
		ErrorCode       string                        `json:"errorCode"`
		FailureStage    string                        `json:"failureStage"`
		Retryable       bool                          `json:"retryable"`
		SafeRetry       bool                          `json:"safeRetry"`
		Result          json.RawMessage               `json:"result"`
	}
	if !access.CanAccess(access.Request{PersonAccess: request.PersonAccess, Action: access.ActionExecute, Resource: toolDescriptor.PolicyResource}) {
		return toolcontract.ToolFailureResult(toolcontract.FailurePermissionDenied, toolcontract.FailureCodes.AccessDenied, "capability_access", "current account cannot execute this tool"), nil
	}
	if unexpected := unexpectedCapabilityInputFields(toolDescriptor.InputSchema, rawInput); len(unexpected) > 0 {
		return capabilityUnexpectedInputFailure(operation, toolDescriptor, unexpected), nil
	}
	preparedPayload, toolFailure, errorValue := toolCatalogBuilder.prepareCapabilityToolInput(toolContext, operation, request, rawInput)
	if toolFailure != nil {
		return *toolFailure, nil
	}
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "capability_input", errorValue.Error()), nil
	}
	toolInput := preparedPayload.Input
	if missing := missingRequiredCapabilityInputFields(toolDescriptor.InputSchema, toolInput); len(missing) > 0 {
		return capabilityMissingInputFailure(operation, toolDescriptor, missing), nil
	}
	errorValue = toolCatalogBuilder.capabilityClient.PostJSON(toolContext, "/v1/tools/"+url.PathEscape(operation)+"/invoke", capabilityToolRequest(toolContext, toolDescriptor, request, preparedPayload), &response)
	if errorValue != nil {
		return toolcontract.ToolResult{}, errorValue
	}
	isError := response.IsError || response.Status == "error" || response.Status == "denied"
	if errorValue := validateCapabilityResultIdentity(operation, response.Provider, response.SelectedBackend, response.ToolName, response.Outcome, isError); errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "capability_result_identity", errorValue.Error()), nil
	}
	content := strings.TrimSpace(response.Content)
	if content == "" && len(response.Result) > 0 {
		content = string(response.Result)
	}
	result := capabilityToolResult(content, response.Result, response.Effects, isError, response.Message, response.ErrorCode, response.FailureStage, response.Retryable, response.SafeRetry)
	return reviewCapabilityApprovalRefusal(toolContext, request, toolDescriptor, toolInput, result), nil
}

func reviewCapabilityApprovalRefusal(ctx context.Context, request ToolCatalogRequest, toolDescriptor CapabilityToolDescriptor, toolInput json.RawMessage, result toolcontract.ToolResult) toolcontract.ToolResult {
	if result.Failure == nil || !result.Failure.RequiresApproval || request.ToolCallGate == nil {
		return result
	}
	review, errorValue := request.ToolCallGate.ReviewToolCall(ctx, toolcontract.ToolInvocation{
		ToolName: toolDescriptor.Name,
		Input:    toolInput,
	}, toolcontract.ToolDefinition{
		Name:             toolDescriptor.Name,
		RequiresApproval: true,
		ApprovalScope:    toolDescriptor.ApprovalScope,
		SideEffectClass:  toolDescriptor.SideEffectClass,
	})
	if errorValue != nil || review.MayProceed {
		return result
	}
	return review.Result
}

func validateCapabilityResultIdentity(operation string, provider string, selectedBackend string, toolName string, outcome string, isError bool) error {
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(selectedBackend) == "" {
		return errors.New("capability result provider and selectedBackend are required")
	}
	if strings.TrimSpace(toolName) != strings.TrimSpace(operation) {
		return errors.New("capability result toolName does not match the invoked operation")
	}
	expectedOutcome := "succeeded"
	if isError {
		expectedOutcome = "failed"
	}
	if strings.TrimSpace(outcome) != expectedOutcome && !(isError && strings.TrimSpace(outcome) == "denied") {
		return errors.New("capability result outcome does not match its status")
	}
	return nil
}

const capabilityCatalogMaxDepth = 4

func capabilityCatalogParameters(inputSchema json.RawMessage) string {
	fields := capabilityCatalogObjectFields(inputSchema, 0)
	if fields == "" {
		return ""
	}
	return "{ " + fields + " }"
}

func capabilityCatalogObjectFields(objectSchema json.RawMessage, depth int) string {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if json.Unmarshal(objectSchema, &schema) != nil || len(schema.Properties) == 0 {
		return ""
	}
	requiredParameter := map[string]bool{}
	for _, name := range schema.Required {
		requiredParameter[name] = true
	}
	requiredNames := []string{}
	optionalNames := []string{}
	for name := range schema.Properties {
		if requiredParameter[name] {
			requiredNames = append(requiredNames, name)
		} else {
			optionalNames = append(optionalNames, name)
		}
	}
	sort.Strings(requiredNames)
	sort.Strings(optionalNames)
	fields := []string{}
	for _, name := range requiredNames {
		fields = append(fields, name+" "+capabilityCatalogFieldType(schema.Properties[name], depth)+" (required)")
	}
	for _, name := range optionalNames {
		fields = append(fields, name+" "+capabilityCatalogFieldType(schema.Properties[name], depth))
	}
	return strings.Join(fields, ", ")
}

func capabilityCatalogFieldType(property json.RawMessage, depth int) string {
	var document struct {
		Type  string            `json:"type"`
		Enum  []json.RawMessage `json:"enum"`
		Items json.RawMessage   `json:"items"`
	}
	if json.Unmarshal(property, &document) != nil {
		return "value"
	}
	if len(document.Enum) > 0 {
		return "enum(" + strings.Join(capabilityCatalogEnumValues(document.Enum), "|") + ")"
	}
	fieldType := strings.TrimSpace(document.Type)
	if fieldType == "object" && depth < capabilityCatalogMaxDepth {
		if nested := capabilityCatalogObjectFields(property, depth+1); nested != "" {
			return "object{ " + nested + " }"
		}
	}
	if fieldType == "array" && depth < capabilityCatalogMaxDepth && len(document.Items) > 0 {
		return "array[" + capabilityCatalogFieldType(document.Items, depth+1) + "]"
	}
	if fieldType != "" {
		return fieldType
	}
	return "value"
}

func capabilityCatalogBaseType(property json.RawMessage) string {
	var document struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(property, &document) == nil && strings.TrimSpace(document.Type) != "" {
		return document.Type
	}
	return "value"
}

func capabilityCatalogEnumValues(values []json.RawMessage) []string {
	rendered := make([]string, 0, len(values))
	for _, value := range values {
		var text string
		if json.Unmarshal(value, &text) == nil {
			rendered = append(rendered, text)
			continue
		}
		rendered = append(rendered, strings.TrimSpace(string(value)))
	}
	return rendered
}

func missingRequiredCapabilityInputFields(inputSchema json.RawMessage, toolInput json.RawMessage) []string {
	if len(inputSchema) == 0 {
		return nil
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if json.Unmarshal(inputSchema, &schema) != nil || len(schema.Required) == 0 {
		return nil
	}
	input := map[string]json.RawMessage{}
	if len(toolInput) > 0 {
		json.Unmarshal(toolInput, &input)
	}
	missing := []string{}
	for _, field := range schema.Required {
		if value, exists := input[field]; !exists || isEmptyCapabilityInputValue(value) {
			missing = append(missing, field)
		}
	}
	return missing
}

func unexpectedCapabilityInputFields(inputSchema json.RawMessage, toolInput json.RawMessage) []string {
	var schema struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	if json.Unmarshal(inputSchema, &schema) != nil || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		return nil
	}
	input := map[string]json.RawMessage{}
	if json.Unmarshal(toolInput, &input) != nil {
		return nil
	}
	unexpected := []string{}
	for fieldName := range input {
		if _, isAllowed := schema.Properties[fieldName]; !isAllowed {
			unexpected = append(unexpected, fieldName)
		}
	}
	sort.Strings(unexpected)
	return unexpected
}

func isEmptyCapabilityInputValue(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed == "" || trimmed == "null" || trimmed == `""` || trimmed == "{}" || trimmed == "[]"
}

func capabilityMissingInputFailure(operation string, toolDescriptor CapabilityToolDescriptor, missing []string) toolcontract.ToolResult {
	message := operation + " needs these input fields: " + strings.Join(missing, ", ") + ". Call " + operation + " with a JSON object containing them. See inputSkeleton in this failure's data for a fillable template."
	result := toolcontract.ToolFailureData(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "capability_input", message, capabilityInputSkeletonData(toolDescriptor.InputSchema, missing))
	if result.Failure == nil {
		return result
	}
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	result.Failure.FailureClass = "schema"
	result.Failure.RetryPolicy = "different_input"
	result.Failure.RecoveryHints = []toolcontract.RecoveryHint{{
		Action:    "Retry " + operation + " with real values for required fields: " + capabilityRequiredInputDescription(toolDescriptor.InputSchema, missing) + ".",
		ToolNames: []string{operation},
		Reason:    "Input schema for " + operation + ": " + capabilityCatalogParameters(toolDescriptor.InputSchema) + ". Never send an empty input.",
	}}
	return result
}

func capabilityUnexpectedInputFailure(operation string, toolDescriptor CapabilityToolDescriptor, unexpected []string) toolcontract.ToolResult {
	allowedFields := capabilityInputFieldNames(toolDescriptor.InputSchema)
	message := operation + " does not accept these input fields: " + strings.Join(unexpected, ", ") + ". Call " + operation + " using only these fields: " + strings.Join(allowedFields, ", ") + "."
	result := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "capability_input", message)
	if result.Failure == nil {
		return result
	}
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	result.Failure.FailureClass = "schema"
	result.Failure.RetryPolicy = "different_input"
	result.Failure.RecoveryHints = []toolcontract.RecoveryHint{{
		Action:    "Retry " + operation + " using only the operation's declared input fields.",
		ToolNames: []string{operation},
		Reason:    "Input schema for " + operation + ": " + capabilityCatalogParameters(toolDescriptor.InputSchema) + ".",
	}}
	return result
}

func capabilityInputFieldNames(inputSchema json.RawMessage) []string {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(inputSchema, &schema) != nil {
		return nil
	}
	fieldNames := make([]string, 0, len(schema.Properties))
	for fieldName := range schema.Properties {
		fieldNames = append(fieldNames, fieldName)
	}
	sort.Strings(fieldNames)
	return fieldNames
}

func capabilityInputNotObjectFailure(operation string, toolDescriptor CapabilityToolDescriptor) toolcontract.ToolResult {
	requiredFields := requiredFieldsFromSchema(toolDescriptor.InputSchema)
	message := operation + " requires input to be an object. Call " + operation + " with one JSON object, written directly or inside a string, not null and not prose. See inputSkeleton in this failure's data for a fillable template."
	result := toolcontract.ToolFailureData(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "capability_input", message, capabilityInputSkeletonData(toolDescriptor.InputSchema, requiredFields))
	if result.Failure == nil {
		return result
	}
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	result.Failure.FailureClass = "schema"
	result.Failure.RetryPolicy = "different_input"
	result.Failure.RecoveryHints = []toolcontract.RecoveryHint{{
		Action:    "Retry " + operation + " with one JSON object, written directly or inside a string, holding that operation's fields.",
		ToolNames: []string{operation},
		Reason:    "Input schema for " + operation + ": " + capabilityCatalogParameters(toolDescriptor.InputSchema) + ".",
	}}
	return result
}

func requiredFieldsFromSchema(inputSchema json.RawMessage) []string {
	var schema struct {
		Required []string `json:"required"`
	}
	if json.Unmarshal(inputSchema, &schema) != nil {
		return nil
	}
	return schema.Required
}

func (toolCatalogBuilder *ToolCatalogBuilder) capabilityToolDescriptorByName(operation string) (CapabilityToolDescriptor, bool) {
	for _, toolDescriptor := range toolCatalogBuilder.capabilityToolDefinitions() {
		if strings.TrimSpace(toolDescriptor.Name) == operation {
			return toolDescriptor, true
		}
	}
	return CapabilityToolDescriptor{}, false
}

func (toolCatalogBuilder *ToolCatalogBuilder) capabilityRequestForOperation(toolContext context.Context, operation string, request ToolCatalogRequest, input json.RawMessage) (map[string]any, error) {
	descriptor, isFound := toolCatalogBuilder.capabilityToolDescriptorByName(operation)
	if !isFound {
		return nil, errors.New("capability descriptor is not registered: " + operation)
	}
	return capabilityToolRequest(toolContext, descriptor, request, preparedCapabilityToolPayload{Input: input}), nil
}

func capabilityRequiredInputDescription(inputSchema json.RawMessage, requiredFields []string) string {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	json.Unmarshal(inputSchema, &schema)
	descriptions := []string{}
	for _, field := range requiredFields {
		descriptions = append(descriptions, field+" "+capabilityCatalogBaseType(schema.Properties[field]))
	}
	return strings.Join(descriptions, ", ")
}

func capabilityInputSkeletonData(inputSchema json.RawMessage, requiredFields []string) json.RawMessage {
	if len(requiredFields) == 0 {
		return nil
	}
	encoded, errorValue := json.Marshal(map[string]any{"inputSkeleton": capabilityInputSkeleton(inputSchema, requiredFields)})
	if errorValue != nil {
		return nil
	}
	return encoded
}

func capabilityInputSkeleton(inputSchema json.RawMessage, requiredFields []string) map[string]string {
	var schema struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
	}
	json.Unmarshal(inputSchema, &schema)
	skeleton := map[string]string{}
	for _, field := range requiredFields {
		property := schema.Properties[field]
		fieldType := firstNonEmptyString(property.Type, "value")
		description := strings.TrimSpace(property.Description)
		if description == "" {
			skeleton[field] = "<" + fieldType + ">"
			continue
		}
		skeleton[field] = "<" + fieldType + ": " + description + ">"
	}
	return skeleton
}

func defaultCapabilityToolDescription(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "document_read":
		return "Read a workspace document path as Markdown using the shared document conversion pipeline. Prefer file_preview for paths listed in the conversation attachment catalog."
	case "image_read":
		return "Load an image path from the conversation attachment catalog or workspace into the model as an image input. Use only when visual inspection is needed; do not call for PDFs or text documents."
	case "message_context":
		return "Read the current platform conversation, thread, channel, requester, and bot message context."
	case "message_search":
		return "Search platform messages by scope, author, and queries. queries is an OR list; use one item for a single keyword. Returns compact messageIDs before previews. Use before deleting or editing messages described in natural language."
	case "message_delete":
		return "Delete assistant bot messages by exact messageIDs returned from message.search. Deletes one selected page at a time and never searches internally."
	case "message_send":
		return "Send a platform message to a direct message, current thread, current channel, or named channel. Recipient resolution and ambiguity are handled by this tool."
	case "message_update":
		return "Replace one exact span of text inside an assistant bot message, or change its pin state. oldText must occur exactly once in that message."
	default:
		return "Workspace capability tool"
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) capabilityToolDefinitions() []CapabilityToolDescriptor {
	return copyCapabilityToolDescriptors(toolCatalogBuilder.capabilityToolDescriptors)
}

func capabilityToolAvailability(toolDescriptor CapabilityToolDescriptor, request ToolCatalogRequest) toolcontract.ToolAvailability {
	switch strings.TrimSpace(toolDescriptor.Availability.State) {
	case "not_allowed":
		return toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityDenied, Reason: toolDescriptor.Availability.Reason}
	case "not_connected", "not_ready":
		return toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityUnavailable, Reason: toolDescriptor.Availability.Reason}
	case "ok":
	default:
		return toolcontract.ToolAvailability{}
	}
	if !access.CanAccess(access.Request{PersonAccess: request.PersonAccess, Action: access.ActionExecute, Resource: toolDescriptor.PolicyResource}) {
		return toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityDenied, Reason: "access denied"}
	}
	return toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityAvailable}
}

func capabilityToolResult(content string, data json.RawMessage, effects []toolcontract.ResourceEffect, isFailed bool, message string, errorCode string, failureStage string, retryable bool, safeRetry bool) toolcontract.ToolResult {
	result := toolcontract.ToolResult{
		Output:          toolcontract.ToolOutput{Content: content, Data: data},
		Effects:         append([]toolcontract.ResourceEffect{}, effects...),
		Attachments:     capabilityAttachments(data),
		RecoveryActions: capabilityRecoveryActions(data),
	}
	if !isFailed {
		return result
	}
	resolvedErrorCode := firstNonEmptyString(errorCode, capabilityResultString(data, "errorCode"), toolcontract.FailureCodes.OperationFailed.String())
	canonicalErrorCode := toolcontract.CanonicalFailureCode(toolcontract.FailureCode(resolvedErrorCode))
	result.Failure = &toolcontract.ToolFailure{
		Kind:             capabilityFailureKind(canonicalErrorCode),
		Code:             canonicalErrorCode,
		Stage:            firstNonEmptyString(failureStage, capabilityResultString(data, "failureStage"), "capability_invoke"),
		UserSafeSummary:  firstNonEmptyString(message, capabilityResultString(data, "message"), content),
		RequiresApproval: strings.TrimSpace(resolvedErrorCode) == "approval_required",
		Retryable:        retryable || capabilityResultBoolean(data, "retryable"),
		SafeRetry:        safeRetry || capabilityResultBoolean(data, "safeRetry"),
		RecoveryHints:    capabilityRecoveryHints(data),
	}
	return result
}

func capabilityFailureKind(errorCode string) toolcontract.FailureKind {
	switch strings.TrimSpace(errorCode) {
	case toolcontract.FailureCodes.AccessDenied.String():
		return toolcontract.FailurePermissionDenied
	case toolcontract.FailureCodes.InvalidInput.String():
		return toolcontract.FailureInvalidInput
	case toolcontract.FailureCodes.RateLimited.String():
		return toolcontract.FailureRateLimited
	case toolcontract.FailureCodes.NotFound.String():
		return toolcontract.FailureNotFound
	case toolcontract.FailureCodes.Unavailable.String():
		return toolcontract.FailureDependencyUnavailable
	case toolcontract.FailureCodes.PolicyBlocked.String():
		return toolcontract.FailurePolicyBlocked
	case toolcontract.FailureCodes.InteractionRequired.String():
		return toolcontract.FailureInteractionRequired
	default:
		return toolcontract.FailureExternalService
	}
}

type preparedCapabilityToolPayload struct {
	Input     json.RawMessage
	Transport map[string]any
}

func capabilityToolRequest(toolContext context.Context, descriptor CapabilityToolDescriptor, request ToolCatalogRequest, payload preparedCapabilityToolPayload) map[string]any {
	contextDocument := map[string]any{
		"requesterPersonID":       request.RequesterPersonID,
		"requesterEmail":          request.RequesterEmail,
		"requesterName":           request.RequesterName,
		"requesterPlatformUserID": request.RequesterPlatformUserID,
		"taskSource":              string(request.TaskSource),
		"isScheduledRun":          request.IsScheduledRun,
		"isApprovalContinuation":  request.IsApprovalContinuation,
		"conversationID":          request.ConversationID,
		"conversationType":        request.ConversationType,
		"channelID":               request.ConversationChannelID,
		"channelName":             request.ConversationChannelName,
		"replyTargetID":           request.ReplyTargetID,
		"platform":                request.Platform,
	}
	if !request.ScheduledRun.IsEmpty() {
		contextDocument["scheduledRun"] = request.ScheduledRun
	}
	if conflictResolution := toolcontract.ToolConflictResolutionFromContext(toolContext); conflictResolution != "" {
		contextDocument["conflictResolution"] = conflictResolution
	}
	requestDocument := map[string]any{
		"toolName":       descriptor.CanonicalName,
		"input":          payload.Input,
		"idempotencyKey": capabilityToolIdempotencyKey(toolContext, descriptor),
		"context":        contextDocument,
	}
	if len(payload.Transport) > 0 {
		requestDocument["transport"] = payload.Transport
	}
	if descriptor.PrivacyClass != "" {
		requestDocument["privacyClass"] = descriptor.PrivacyClass
	}
	if descriptor.RequiresUserPresence {
		requestDocument["requiresUserPresence"] = true
	}
	if descriptor.PrivacyClass == "user_browser" {
		requestDocument["executionMode"] = "companion"
	}
	return requestDocument
}

func (toolCatalogBuilder *ToolCatalogBuilder) prepareCapabilityToolInput(toolContext context.Context, toolName string, request ToolCatalogRequest, toolInput json.RawMessage) (preparedCapabilityToolPayload, *toolcontract.ToolResult, error) {
	if siteToolNeedsSourceBundle(toolName) {
		transport, toolFailure, errorValue := toolCatalogBuilder.prepareSiteSourceBundle(toolContext, request, toolInput)
		return preparedCapabilityToolPayload{Input: toolInput, Transport: transport}, toolFailure, errorValue
	}
	if capabilityToolNeedsWorkspacePath(toolName) {
		input, errorValue := toolCatalogBuilder.resolveCapabilityWorkspacePathInput(toolContext, toolName, request, toolInput)
		return preparedCapabilityToolPayload{Input: input}, nil, errorValue
	}
	return preparedCapabilityToolPayload{Input: toolInput}, nil, nil
}

func capabilityToolNeedsWorkspacePath(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "document_read", "image_read", "image_generate":
		return true
	default:
		return false
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveCapabilityWorkspacePathInput(toolContext context.Context, toolName string, request ToolCatalogRequest, toolInput json.RawMessage) (json.RawMessage, error) {
	inputDocument := map[string]any{}
	if len(toolInput) > 0 {
		if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
			return nil, errorValue
		}
	}
	path, _ := inputDocument["path"].(string)
	if materialID, _ := inputDocument["materialID"].(string); strings.TrimSpace(materialID) != "" {
		material, errorValue := resolveReadableAttachmentMaterial(toolContext, request, materialID)
		if errorValue != nil {
			return nil, errorValue
		}
		if errorValue := validateAttachmentMaterialTool(toolName, material); errorValue != nil {
			return nil, errorValue
		}
		path = material.Path
		delete(inputDocument, "materialID")
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("path is required")
	}
	inputDocument["path"] = toolCatalogBuilder.capabilityBridgePath(request, path)
	return json.Marshal(inputDocument)
}

func nativeBridgePath(path string, identity security.ExecutionIdentity) string {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "~" {
		return identity.HomeDirectoryPath
	}
	if strings.HasPrefix(trimmedPath, "~/") {
		return filepath.Join(identity.HomeDirectoryPath, strings.TrimPrefix(trimmedPath, "~/"))
	}
	if filepath.IsAbs(trimmedPath) {
		return trimmedPath
	}
	return filepath.Join(identity.HomeDirectoryPath, trimmedPath)
}

func (toolCatalogBuilder *ToolCatalogBuilder) nativeRequesterPath(request ToolCatalogRequest, path string) string {
	identity := toolCatalogBuilder.executionIdentityForRequester(request)
	if strings.TrimSpace(identity.HomeDirectoryPath) == "" {
		identity.HomeDirectoryPath = toolCatalogBuilder.workspaceRootPath
	}
	return nativeBridgePath(toolCatalogBuilder.resolveAgentWorkspacePath(path), identity)
}

func (toolCatalogBuilder *ToolCatalogBuilder) capabilityBridgePath(request ToolCatalogRequest, path string) string {
	return toolCatalogBuilder.agentWorkspacePath(toolCatalogBuilder.nativeRequesterPath(request, path))
}

func capabilityAttachments(result json.RawMessage) []toolcontract.FileAttachment {
	if len(result) == 0 {
		return nil
	}
	var attachment capabilityFileAttachment
	if errorValue := json.Unmarshal(result, &attachment); errorValue == nil && strings.TrimSpace(attachment.DevicePath) != "" {
		return []toolcontract.FileAttachment{attachment.agentFileAttachment()}
	}
	var document struct {
		Attachments []capabilityFileAttachment `json:"attachments"`
	}
	if errorValue := json.Unmarshal(result, &document); errorValue != nil {
		return nil
	}
	attachments := []toolcontract.FileAttachment{}
	for _, candidate := range document.Attachments {
		if strings.TrimSpace(candidate.DevicePath) != "" {
			attachments = append(attachments, candidate.agentFileAttachment())
		}
	}
	return attachments
}

type capabilityFileAttachment struct {
	DevicePath    string `json:"devicePath"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	Title         string `json:"title,omitempty"`
	ContentBase64 string `json:"contentBase64,omitempty"`
}

func (attachment capabilityFileAttachment) agentFileAttachment() toolcontract.FileAttachment {
	return toolcontract.FileAttachment{
		DevicePath:    attachment.DevicePath,
		Filename:      attachment.Filename,
		ContentType:   attachment.ContentType,
		SizeBytes:     attachment.SizeBytes,
		Title:         attachment.Title,
		ContentBase64: attachment.ContentBase64,
	}
}

func capabilityRecoveryActions(result json.RawMessage) []toolcontract.RecoveryAction {
	var document struct {
		Recovery *toolcontract.RecoveryAction `json:"recovery"`
	}
	if json.Unmarshal(result, &document) != nil || document.Recovery == nil {
		return nil
	}
	if strings.TrimSpace(document.Recovery.Kind) == "" {
		return nil
	}
	return []toolcontract.RecoveryAction{*document.Recovery}
}

func capabilityRecoveryHints(result json.RawMessage) []toolcontract.RecoveryHint {
	var document struct {
		RecoveryHints []toolcontract.RecoveryHint  `json:"recoveryHints"`
		Recovery      *toolcontract.RecoveryAction `json:"recovery"`
	}
	if json.Unmarshal(result, &document) != nil {
		return nil
	}
	hints := statedCapabilityRecoveryHints(document.RecoveryHints)
	if document.Recovery == nil || strings.TrimSpace(document.Recovery.Kind) == "" {
		return hints
	}
	return append(hints, toolcontract.RecoveryHint{
		Action: strings.TrimSpace(document.Recovery.Kind),
		Reason: "Capability returned a user-visible recovery action.",
	})
}

func statedCapabilityRecoveryHints(hints []toolcontract.RecoveryHint) []toolcontract.RecoveryHint {
	var statedHints []toolcontract.RecoveryHint
	for _, hint := range hints {
		if strings.TrimSpace(hint.Action) == "" && len(hint.ToolNames) == 0 {
			continue
		}
		statedHints = append(statedHints, hint)
	}
	return statedHints
}

func capabilityResultString(result json.RawMessage, fieldName string) string {
	if len(result) == 0 {
		return ""
	}
	var document map[string]any
	if json.Unmarshal(result, &document) != nil {
		return ""
	}
	value, isString := document[fieldName].(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(value)
}

func capabilityResultBoolean(result json.RawMessage, fieldName string) bool {
	if len(result) == 0 {
		return false
	}
	var document map[string]any
	if json.Unmarshal(result, &document) != nil {
		return false
	}
	value, isBoolean := document[fieldName].(bool)
	return isBoolean && value
}
