package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type HistoryProvider interface {
	FetchHistory(context.Context, string, int) (agentcontract.VisibleContext, error)
}

type AttachmentMaterialResolver interface {
	ResolveAttachmentMaterial(context.Context, string) (agentcontract.VisibleContextMaterial, error)
}

type ToolCatalogBuilder struct {
	allowedToolNamesByProfile    map[string][]string
	defaultAllowedToolNames      []string
	memoryStore                  *memory.Store
	memoryIngester               *memory.Ingester
	mcpRegistry                  *mcp.McpRegistry
	capabilityClient             capability.Client
	companyProvider              func() agentcontract.CompanyContext
	capabilityToolDescriptors    []CapabilityToolDescriptor
	terminalService              *security.ShellService
	workspaceActorFactory        security.WorkspaceActorFactory
	taskRunService               *task.TaskRunService
	taskArtifactService          *task.TaskArtifactService
	taskScheduleRepository       task.TaskScheduleRepository
	taskWaitTokenRepository      task.TaskWaitTokenRepository
	workspaceRootPath            string
	optionalFileReadPathSuffixes []string
	skillChangeHandler           func(context.Context)
	skillRetriever               agentcontract.SkillRetriever
	instructionBundleLoader      func() agentcontract.InstructionBundle
	mcpQuarantineReporter        func(toolcontract.QuarantinedToolProvider)
	capabilityQuarantineReporter func(toolcontract.QuarantinedToolProvider)
	liveSnapshotMutex            sync.Mutex
	liveSnapshotDescriptors      []CapabilityToolDescriptor
	liveSnapshotHash             string
	companionStatusMutex         sync.Mutex
	companionStatusValue         string
	companionStatusCheckedAt     time.Time
}

type toolHandlerContext struct {
	request           ToolCatalogRequest
	conversationScope ConversationResourceScope
}

type ToolCatalogRequest struct {
	ProfileName                string
	ToolCallGate               toolcontract.ToolCallGate
	Prompt                     string
	VisibleContext             agentcontract.VisibleContext
	RequesterPersonID          string
	RequesterName              string
	RequesterEmail             string
	RequesterPlatformUserID    string
	TaskSource                 TaskLaunchSource
	IsScheduledRun             bool
	IsApprovalContinuation     bool
	ConversationID             string
	ConversationType           string
	ConversationChannelID      string
	ConversationChannelName    string
	ActiveCircleID             string
	ActiveCircleConflict       bool
	ReplyTargetID              string
	Platform                   string
	HistoryCursor              string
	HistoryProvider            HistoryProvider
	AttachmentMaterialResolver AttachmentMaterialResolver
	PersonAccess               policy.PersonAccess
	MemoryLabel                memory.SecurityLabel
	AccessibleConversationIDs  []string
	InputParts                 []agentcontract.AgentPart
	ScheduledRun               agentcontract.ScheduledRunContext
	RegisteredToolNameCeiling  []string
}

type CapabilityToolDescriptor = capability.ToolDescriptor
type CapabilityToolResultContract = capability.ToolResultContract
type CapabilityEvidenceCondition = capability.EvidenceCondition
type CapabilityResourceEffectContract = capability.ResourceEffectContract
type CapabilityCompletionEvidence = capability.CompletionEvidence
type CapabilityAvailability = capability.Availability
type CapabilityIdempotency = capability.Idempotency

type historyToolInput struct {
	HistoryCursor string `json:"historyCursor"`
	Limit         int    `json:"limit"`
}

func NewToolCatalogBuilder() *ToolCatalogBuilder {
	return &ToolCatalogBuilder{
		workspaceRootPath: "/workspace",
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseCompanyProvider(companyProvider func() agentcontract.CompanyContext) {
	toolCatalogBuilder.companyProvider = companyProvider
}

func (toolCatalogBuilder *ToolCatalogBuilder) companyTimeZone() string {
	if toolCatalogBuilder.companyProvider == nil {
		return ""
	}
	return toolCatalogBuilder.companyProvider().TimeZone
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseAllowedToolNamesByProfile(allowedToolNamesByProfile map[string][]string, defaultAllowedToolNames []string) {
	toolCatalogBuilder.allowedToolNamesByProfile = copyAllowedToolNamesByProfile(allowedToolNamesByProfile)
	toolCatalogBuilder.defaultAllowedToolNames = trimNonEmptyStrings(defaultAllowedToolNames)
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseMemoryStore(memoryStore *memory.Store, memoryIngester *memory.Ingester) {
	toolCatalogBuilder.memoryStore = memoryStore
	toolCatalogBuilder.memoryIngester = memoryIngester
}

func (toolCatalogBuilder *ToolCatalogBuilder) MemoryStore() *memory.Store {
	return toolCatalogBuilder.memoryStore
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseMCPRegistry(mcpRegistry *mcp.McpRegistry) {
	toolCatalogBuilder.mcpRegistry = mcpRegistry
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseMCPQuarantineReporter(reporter func(toolcontract.QuarantinedToolProvider)) {
	toolCatalogBuilder.mcpQuarantineReporter = reporter
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseCapabilityQuarantineReporter(reporter func(toolcontract.QuarantinedToolProvider)) {
	toolCatalogBuilder.capabilityQuarantineReporter = reporter
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseCapabilityToolDescriptors(capabilityClient capability.Client, toolDescriptors []CapabilityToolDescriptor) {
	toolCatalogBuilder.capabilityClient = capabilityClient
	toolCatalogBuilder.capabilityToolDescriptors = copyCapabilityToolDescriptors(toolDescriptors)
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTerminalService(terminalService *security.ShellService) {
	toolCatalogBuilder.terminalService = terminalService
	if terminalService != nil && toolCatalogBuilder.workspaceActorFactory == nil {
		toolCatalogBuilder.workspaceActorFactory = terminalService.WorkspaceActorFactory()
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseWorkspaceActorFactory(workspaceActorFactory security.WorkspaceActorFactory) {
	toolCatalogBuilder.workspaceActorFactory = workspaceActorFactory
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTaskRunService(taskRunService *task.TaskRunService) {
	toolCatalogBuilder.taskRunService = taskRunService
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTaskArtifactService(taskArtifactService *task.TaskArtifactService) {
	toolCatalogBuilder.taskArtifactService = taskArtifactService
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTaskScheduleRepository(taskScheduleRepository task.TaskScheduleRepository) {
	toolCatalogBuilder.taskScheduleRepository = taskScheduleRepository
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTaskWaitTokenRepository(taskWaitTokenRepository task.TaskWaitTokenRepository) {
	toolCatalogBuilder.taskWaitTokenRepository = taskWaitTokenRepository
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseWorkspaceRootPath(workspaceRootPath string) {
	trimmedWorkspaceRootPath := strings.TrimSpace(workspaceRootPath)
	if trimmedWorkspaceRootPath != "" {
		toolCatalogBuilder.workspaceRootPath = trimmedWorkspaceRootPath
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseOptionalFileReadPathSuffixes(optionalFileReadPathSuffixes []string) {
	toolCatalogBuilder.optionalFileReadPathSuffixes = trimNonEmptyStrings(optionalFileReadPathSuffixes)
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseSkillChangeHandler(skillChangeHandler func(context.Context)) {
	toolCatalogBuilder.skillChangeHandler = skillChangeHandler
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseSkillSearch(skillRetriever agentcontract.SkillRetriever, instructionBundleLoader func() agentcontract.InstructionBundle) {
	toolCatalogBuilder.skillRetriever = skillRetriever
	toolCatalogBuilder.instructionBundleLoader = instructionBundleLoader
}

func (toolCatalogBuilder *ToolCatalogBuilder) WorkspaceRootPath() string {
	return strings.TrimSpace(toolCatalogBuilder.workspaceRootPath)
}

func (toolCatalogBuilder *ToolCatalogBuilder) BuildToolSet(request ToolCatalogRequest) *toolcontract.ToolSet {
	request = withResolvedActiveCircle(request)
	toolSet := toolcontract.NewToolSet(toolCatalogBuilder.allowedToolNames(request.ProfileName))
	handlerContext := toolHandlerContext{
		request:           request,
		conversationScope: toolCatalogBuilder.conversationScope(request),
	}
	toolCatalogBuilder.registerLocalTools(toolSet, request, handlerContext)
	toolCatalogBuilder.registerKernelTools(toolSet, handlerContext)
	toolCatalogBuilder.registerCapabilityTools(toolSet, request)
	toolCatalogBuilder.registerMCPTools(toolSet, request)
	toolSet.UseToolCallGate(request.ToolCallGate)
	return toolSetWithinRegisteredToolNameCeiling(toolSet, request.RegisteredToolNameCeiling)
}

func toolSetWithinRegisteredToolNameCeiling(toolSet *toolcontract.ToolSet, ceilingToolNames []string) *toolcontract.ToolSet {
	if len(ceilingToolNames) == 0 {
		return toolSet
	}
	return toolSet.WithRegisteredToolNamesLimitedTo(ceilingToolNames)
}

func (toolCatalogBuilder *ToolCatalogBuilder) allowedToolNames(profileName string) []string {
	normalizedProfileName := normalizeProfileName(profileName)
	if allowedToolNames, isFound := toolCatalogBuilder.allowedToolNamesByProfile[normalizedProfileName]; isFound {
		return trimNonEmptyStrings(allowedToolNames)
	}
	if len(toolCatalogBuilder.defaultAllowedToolNames) > 0 {
		return trimNonEmptyStrings(toolCatalogBuilder.defaultAllowedToolNames)
	}
	return DefaultAllowedToolNames()
}

func DefaultAllowedToolNames() []string {
	return append(toolcontract.KernelToolNames(), toolcontract.AskInputToolName)
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerHistoryTool(toolRegistry *toolcontract.ToolSet, request ToolCatalogRequest) {
	if request.HistoryProvider == nil {
		return
	}
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[historyToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "conversation_history",
			Description: "Fetch earlier visible messages for this conversation using the opaque history cursor.",
			InputSchema: conversationHistoryInputSchema,
		},
		Handler: func(toolContext context.Context, input historyToolInput) (toolcontract.ToolResult, error) {
			return fetchHistoryTool(toolContext, input, request)
		},
		Result: toolcontract.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerMemoryTool(toolRegistry *toolcontract.ToolSet, request ToolCatalogRequest) {
	if toolCatalogBuilder.memoryStore == nil {
		return
	}
	registerStoreMemoryTools(toolCatalogBuilder, toolRegistry, request)
}

func fetchHistoryTool(toolContext context.Context, input historyToolInput, request ToolCatalogRequest) (toolcontract.ToolResult, error) {
	historyCursor := firstNonEmptyString(input.HistoryCursor, request.HistoryCursor)
	if historyCursor == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "conversation_history", "history cursor is unavailable"), nil
	}
	limit := input.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	visibleContext, errorValue := request.HistoryProvider.FetchHistory(toolContext, historyCursor, limit)
	if errorValue != nil {
		return toolcontract.ToolResult{}, errorValue
	}
	document := json.RawMessage(marshalToolResult(projectConversationHistory(visibleContext)))
	return toolcontract.ToolSuccessData(string(document), document), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerBuiltInTools(toolRegistry *toolcontract.ToolSet, handlerContext toolHandlerContext) {
	toolCatalogBuilder.registerAskInputTool(toolRegistry)
	toolCatalogBuilder.registerScheduleTools(toolRegistry, handlerContext)
	toolCatalogBuilder.registerSkillManagementTools(toolRegistry)
}

func (toolCatalogBuilder *ToolCatalogBuilder) workspaceActorForRequest(toolContext context.Context, request ToolCatalogRequest) (security.WorkspaceActor, *toolcontract.ToolResult) {
	if toolCatalogBuilder.workspaceActorFactory == nil {
		result := actorToolFailure("requester", "actor_runtime", "", security.WorkspaceActorError{
			Operation: "requester",
			Stage:     "factory",
			Code:      security.ActorErrorCodeRuntimeUnavailable,
			Detail:    "workspace actor factory is unavailable",
		})
		return nil, &result
	}
	personAccess := request.PersonAccess
	if strings.TrimSpace(personAccess.PersonID) == "" {
		personAccess.PersonID = strings.TrimSpace(request.RequesterPersonID)
	}
	workspaceActor, errorValue := toolCatalogBuilder.workspaceActorFactory.Requester(toolContext, security.WorkspaceActorRequest{
		PersonAccess:      personAccess,
		WorkspaceRootPath: toolCatalogBuilder.workspaceRootPath,
	})
	if errorValue != nil {
		stage := "actor_runtime"
		if actorFailureCode(errorValue) == security.ActorErrorCodeIdentityMissing {
			stage = "actor_identity_missing"
		}
		result := actorToolFailure("requester", stage, "", errorValue)
		return nil, &result
	}
	return workspaceActor, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) executionIdentityForRequester(request ToolCatalogRequest) security.ExecutionIdentity {
	personAccess := request.PersonAccess
	if strings.TrimSpace(personAccess.PersonID) == "" {
		personAccess.PersonID = strings.TrimSpace(request.RequesterPersonID)
	}
	return security.ExecutionIdentityForPersonAccess(personAccess, toolCatalogBuilder.workspaceRootPath)
}

func actorToolFailure(operation string, stage string, virtualPath string, errorValue error) toolcontract.ToolResult {
	message := actorFailureMessage(operation, virtualPath, errorValue)
	failureKind := toolcontract.FailureExternalService
	failureCode := toolcontract.FailureCodes.OperationFailed
	switch actorFailureCode(errorValue) {
	case security.ActorErrorCodePermissionDenied:
		failureKind = toolcontract.FailurePermissionDenied
		failureCode = toolcontract.FailureCodes.AccessDenied
	case security.ActorErrorCodeNotFound:
		failureKind = toolcontract.FailureNotFound
		failureCode = toolcontract.FailureCodes.NotFound
	case security.ActorErrorCodeInvalidPath:
		failureKind = toolcontract.FailureInvalidInput
		failureCode = toolcontract.FailureCodes.InvalidInput
	}
	result := toolcontract.ToolFailureWithOutput(failureKind, failureCode, stage, message, json.RawMessage(marshalToolResult(actorFailureDataFields(operation, stage, virtualPath, errorValue))))
	result.Failure.Retryable = true
	result.Failure.SafeRetry = true
	return result
}

func actorFailureDataFields(operation string, stage string, virtualPath string, errorValue error) map[string]any {
	return map[string]any{
		"operation":   operation,
		"stage":       stage,
		"virtualPath": virtualPath,
		"code":        actorFailureCode(errorValue),
		"detail":      actorFailureDetail(errorValue),
		"actorUser":   actorFailureUser(errorValue),
	}
}

func actorFailureMessage(operation string, virtualPath string, errorValue error) string {
	detail := actorFailureDetail(errorValue)
	actorUser := actorFailureUser(errorValue)
	if actorUser == "" {
		actorUser = "unknown"
	}
	return fmt.Sprintf("actor.%s failed for %s as %s: %s", operation, strings.TrimSpace(virtualPath), actorUser, detail)
}

func actorFailureDetail(errorValue error) string {
	var actorError security.WorkspaceActorError
	if errors.As(errorValue, &actorError) {
		return firstNonEmptyString(strings.TrimSpace(actorError.Detail), errorValue.Error())
	}
	if errorValue == nil {
		return "operation failed"
	}
	return errorValue.Error()
}

func actorFailureCode(errorValue error) string {
	var actorError security.WorkspaceActorError
	if errors.As(errorValue, &actorError) {
		return strings.TrimSpace(actorError.Code)
	}
	return security.ActorErrorCodeOperationFailed
}

func actorFailureUser(errorValue error) string {
	var actorError security.WorkspaceActorError
	if errors.As(errorValue, &actorError) {
		return strings.TrimSpace(actorError.ActorUser)
	}
	return ""
}

func mergeWorkspaceEnvironment(environmentVariables map[string]string, workspaceEnvironment map[string]string) map[string]string {
	result := map[string]string{}
	for name, value := range environmentVariables {
		result[name] = value
	}
	for name, value := range workspaceEnvironment {
		if isWorkspaceManagedEnvironmentName(name) || strings.TrimSpace(result[name]) == "" {
			result[name] = value
		}
	}
	return result
}

func isWorkspaceManagedEnvironmentName(name string) bool {
	switch name {
	case "BLUECLAW_REQUESTER_TMP",
		"BLUECLAW_TASK_TMP",
		"BLUECLAW_REQUESTER_ARTIFACTS",
		"BLUECLAW_DEPENDENCY_CACHE",
		"HOME",
		"PATH",
		"TMPDIR",
		"TMP",
		"TEMP",
		"XDG_CACHE_HOME",
		"XDG_CONFIG_HOME",
		"XDG_RUNTIME_DIR",
		"BUN_TMPDIR",
		"BUN_INSTALL",
		"BUN_INSTALL_CACHE_DIR",
		"npm_config_cache":
		return true
	default:
		return false
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveAgentWorkspacePath(value string) string {
	trimmedPath := strings.TrimSpace(value)
	if trimmedPath == "" {
		return ""
	}
	if toolCatalogBuilder.workspaceRootPath == "/workspace" {
		return trimmedPath
	}
	if trimmedPath == "/workspace" {
		return toolCatalogBuilder.workspaceRootPath
	}
	if strings.HasPrefix(trimmedPath, "/workspace/") {
		return filepath.Join(toolCatalogBuilder.workspaceRootPath, strings.TrimPrefix(trimmedPath, "/workspace/"))
	}
	return trimmedPath
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveAgentWorkspaceReferences(value string) string {
	if strings.TrimSpace(value) == "" || toolCatalogBuilder.workspaceRootPath == "/workspace" {
		return value
	}
	const workspaceReference = "/workspace"
	var result strings.Builder
	remainingStart := 0
	searchStart := 0
	for {
		relativeIndex := strings.Index(value[searchStart:], workspaceReference)
		if relativeIndex < 0 {
			break
		}
		referenceStart := searchStart + relativeIndex
		referenceEnd := referenceStart + len(workspaceReference)
		searchStart = referenceEnd
		if !isWorkspaceReference(value, referenceStart, referenceEnd) {
			continue
		}
		result.WriteString(value[remainingStart:referenceStart])
		result.WriteString(toolCatalogBuilder.workspaceRootPath)
		remainingStart = referenceEnd
	}
	if remainingStart == 0 {
		return value
	}
	result.WriteString(value[remainingStart:])
	return result.String()
}

func isWorkspaceReference(value string, referenceStart int, referenceEnd int) bool {
	if referenceStart > 0 && isWorkspacePathCharacter(value[referenceStart-1]) {
		return false
	}
	return referenceEnd == len(value) || !isWorkspacePathCharacter(value[referenceEnd])
}

func isWorkspacePathCharacter(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value == '_' ||
		value == '-' ||
		value == '.'
}

func (toolCatalogBuilder *ToolCatalogBuilder) resolveAgentWorkspaceEnvironment(environmentVariables map[string]string) map[string]string {
	if len(environmentVariables) == 0 {
		return environmentVariables
	}
	resolvedEnvironmentVariables := map[string]string{}
	for key, value := range environmentVariables {
		resolvedEnvironmentVariables[key] = toolCatalogBuilder.resolveAgentWorkspacePath(value)
	}
	return resolvedEnvironmentVariables
}

func (toolCatalogBuilder *ToolCatalogBuilder) agentWorkspacePath(path string) string {
	relativePath, errorValue := filepath.Rel(toolCatalogBuilder.workspaceRootPath, path)
	if errorValue != nil || relativePath == "." || strings.HasPrefix(relativePath, "../") || relativePath == ".." {
		return path
	}
	return filepath.ToSlash(filepath.Join("/workspace", relativePath))
}

func marshalToolResult(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return fmt.Sprint(value)
	}
	return string(document)
}

func copyAllowedToolNamesByProfile(allowedToolNamesByProfile map[string][]string) map[string][]string {
	copiedAllowedToolNamesByProfile := map[string][]string{}
	for profileName, allowedToolNames := range allowedToolNamesByProfile {
		copiedAllowedToolNamesByProfile[normalizeProfileName(profileName)] = trimNonEmptyStrings(allowedToolNames)
	}
	return copiedAllowedToolNamesByProfile
}

func copyCapabilityToolDescriptors(toolDescriptors []CapabilityToolDescriptor) []CapabilityToolDescriptor {
	copiedToolDescriptors := []CapabilityToolDescriptor{}
	for _, toolDescriptor := range toolDescriptors {
		trimmedName := strings.TrimSpace(toolDescriptor.Name)
		if trimmedName == "" {
			continue
		}
		toolDescriptor.Name = trimmedName
		toolDescriptor.InputSchema = append(json.RawMessage{}, toolDescriptor.InputSchema...)
		toolDescriptor.InputIntentSchema = append(json.RawMessage{}, toolDescriptor.InputIntentSchema...)
		toolDescriptor.OutputSchema = append(json.RawMessage{}, toolDescriptor.OutputSchema...)
		copiedToolDescriptors = append(copiedToolDescriptors, toolDescriptor)
	}
	return copiedToolDescriptors
}

func trimNonEmptyStrings(values []string) []string {
	trimmedValues := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			trimmedValues = append(trimmedValues, trimmedValue)
		}
	}
	return trimmedValues
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func normalizeProfileName(profileName string) string {
	trimmedProfileName := strings.TrimSpace(profileName)
	if trimmedProfileName == "" {
		return "default"
	}
	return trimmedProfileName
}
