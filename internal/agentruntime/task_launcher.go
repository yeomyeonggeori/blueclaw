package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueclaw/internal/toolcallprogress"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type TaskLaunchSource string

const (
	TaskLaunchSourceConnector TaskLaunchSource = "connector"
	TaskLaunchSourceAdmin     TaskLaunchSource = "admin"
	TaskLaunchSourceScheduled TaskLaunchSource = "scheduled"
)

type TaskLauncher struct {
	harness                       agentcontract.Harness
	launchFailureCompleter        LaunchFailureCompleter
	taskRunService                *taskstate.TaskRunService
	toolCatalogBuilder            *ToolCatalogBuilder
	requesterWorkspaceProvisioner RequesterWorkspaceProvisioner
	requesterEmailResolver        RequesterEmailResolver
	agentIdentityProvider         func() agentcontract.AgentIdentity
	companyProvider               func() agentcontract.CompanyContext
	approvalGate                  *approvalgate.Gate
	observeTask                   func(TaskLaunchRequest) func(TaskLaunchResult, error)
}

func (taskLauncher *TaskLauncher) UseTaskObserver(observer func(TaskLaunchRequest) func(TaskLaunchResult, error)) {
	taskLauncher.observeTask = observer
}

func (taskLauncher *TaskLauncher) UseApprovalGate(approvalGate *approvalgate.Gate) {
	taskLauncher.approvalGate = approvalGate
}

type RequesterWorkspaceProvisioner interface {
	ProvisionRequesterWorkspace(context.Context, policy.PersonAccess, string) error
}

type RequesterEmailResolver interface {
	ResolvePersonPrimaryEmail(personID string) string
}

type TaskLaunchRequest struct {
	Source                     TaskLaunchSource
	SourceReference            string
	RequesterPersonID          string
	RequesterName              string
	RequesterCallingName       string
	RequesterHandle            string
	RequesterEmail             string
	RecordCatalog              RecordCatalogClient
	RequesterPlatformUserID    string
	IsRuntimeRestartResume     bool
	ExistingTaskRunID          string
	IsTaskRunOpenedForThisTurn bool
	OriginReplyTargetID        string
	OriginIsThread             bool
	ProfileName                string
	Platform                   string
	ConversationID             string
	DeliveryConversationID     string
	ConversationType           string
	ConversationChannelID      string
	ConversationChannelName    string
	ActiveCircleID             string
	ActiveCircleConflict       bool
	ReplyTargetID              string
	Prompt                     string
	InputParts                 []agentcontract.AgentPart
	ResponseLanguage           string
	VisibleContext             agentcontract.VisibleContext
	ActiveGoal                 agentcontract.ActiveGoal
	PriorTask                  agentcontract.PriorTaskContext
	ScheduledRun               agentcontract.ScheduledRunContext
	ScheduledApprovedCall      *task.ScheduleApprovedCall
	SettledCalls               []agentcontract.CarriedOutCall
	TaskLevel                  agentcontract.TaskLevel
	PendingInput               agentcontract.PendingInputContext
	SkipSkillSelection         bool
	UseEmptyToolCatalog        bool
	AmbientDuty                inboundengagement.AmbientDutyContext
	PinnedToolNames            []string
	PinnedSkillNames           []string
	HistoryProvider            HistoryProvider
	AttachmentMaterialResolver AttachmentMaterialResolver
	PersonAccess               policy.PersonAccess
	AccessibleConversationIDs  []string
	CheckpointSender           agentcontract.AgentCheckpointSender
	ToolCallObserver           toolcallprogress.Observer
	ArtifactManifest           []agentcontract.ArtifactManifestEntry
	TurnStartedAt              time.Time
	ExecutionStartedAt         time.Time
}

type TaskLaunchResult struct {
	TurnResult            agentcontract.AgentTurnResult
	MemoryFacts           []memory.MemoryFact
	ToolNames             []string
	NormalizedProfileName string
}

type taskLaunchStep[T any] interface {
	Name() string
	Run(context.Context, *taskLaunchExecution) (T, error)
}

type taskLaunchExecution struct {
	Launcher              *TaskLauncher
	Request               TaskLaunchRequest
	NormalizedProfileName string
	TaskEvents            *taskEventSubscription
}

const (
	launchStepStatusError          = "error"
	launchStepStatusResult         = "result"
	contextualMemorySearchToolName = "memory_search"
)

type launchStepRecord struct {
	StepName        string `json:"stepName"`
	Status          string `json:"status"`
	StartedAtUnixMs int64  `json:"startedAtUnixMs"`
	DurationMs      int64  `json:"durationMs"`
	Error           string `json:"error,omitempty"`
	errorValue      error  `json:"-"`
}

type launchMemoryResult struct {
	Facts          []memory.MemoryFact
	IdentityCount  int
	RecalledCount  int
	Mode           string
	DegradedReason string
	Error          string
}

type LaunchFailureCompleter interface {
	CompleteLaunchFailure(context.Context, agentcontract.AgentTurnRequest, string, string, error) agentcontract.AgentTurnResult
}

func (taskLauncher *TaskLauncher) UseLaunchFailureCompleter(launchFailureCompleter LaunchFailureCompleter) {
	taskLauncher.launchFailureCompleter = launchFailureCompleter
}

func NewTaskLauncher(harness agentcontract.Harness, taskRunService *taskstate.TaskRunService, toolCatalogBuilder *ToolCatalogBuilder) *TaskLauncher {
	if toolCatalogBuilder == nil {
		toolCatalogBuilder = NewToolCatalogBuilder()
	}
	return &TaskLauncher{
		harness:            harness,
		taskRunService:     taskRunService,
		toolCatalogBuilder: toolCatalogBuilder,
	}
}

func (taskLauncher *TaskLauncher) UseRequesterWorkspaceProvisioner(provisioner RequesterWorkspaceProvisioner) {
	taskLauncher.requesterWorkspaceProvisioner = provisioner
}

func (taskLauncher *TaskLauncher) UseRequesterEmailResolver(resolver RequesterEmailResolver) {
	taskLauncher.requesterEmailResolver = resolver
}

func (taskLauncher *TaskLauncher) UseAgentIdentityProvider(agentIdentityProvider func() agentcontract.AgentIdentity) {
	taskLauncher.agentIdentityProvider = agentIdentityProvider
}

func (taskLauncher *TaskLauncher) UseCompanyProvider(companyProvider func() agentcontract.CompanyContext) {
	taskLauncher.companyProvider = companyProvider
}

func (taskLauncher *TaskLauncher) company() agentcontract.CompanyContext {
	if taskLauncher.companyProvider == nil {
		return agentcontract.CompanyContext{}
	}
	return taskLauncher.companyProvider()
}

func (taskLauncher *TaskLauncher) agentIdentity() agentcontract.AgentIdentity {
	if taskLauncher.agentIdentityProvider == nil {
		return agentcontract.AgentIdentity{}
	}
	return taskLauncher.agentIdentityProvider()
}

func (taskLauncher *TaskLauncher) resolveRequesterEmail(request TaskLaunchRequest) string {
	personID := strings.TrimSpace(request.RequesterPersonID)
	if taskLauncher.requesterEmailResolver != nil && personID != "" {
		if resolvedEmail := strings.TrimSpace(taskLauncher.requesterEmailResolver.ResolvePersonPrimaryEmail(personID)); resolvedEmail != "" {
			return resolvedEmail
		}
	}
	return request.RequesterEmail
}

func (taskLauncher *TaskLauncher) Launch(ctx context.Context, request TaskLaunchRequest) (TaskLaunchResult, error) {
	var finishObservation func(TaskLaunchResult, error)
	if taskLauncher.observeTask != nil {
		finishObservation = taskLauncher.observeTask(request)
	}
	if request.TurnStartedAt.IsZero() {
		request.TurnStartedAt = time.Now()
	}
	launchResult, errorValue := taskLauncher.launchTask(ctx, request)
	if finishObservation != nil {
		finishObservation(launchResult, errorValue)
	}
	return launchResult, errorValue
}

type launchTaskRun struct {
	TaskRunID      string
	IsOpenedByHost bool
}

func (taskLauncher *TaskLauncher) openTaskRunForLaunch(request TaskLaunchRequest) launchTaskRun {
	if existingTaskRunID := strings.TrimSpace(request.ExistingTaskRunID); existingTaskRunID != "" {
		return launchTaskRun{TaskRunID: existingTaskRunID, IsOpenedByHost: request.IsTaskRunOpenedForThisTurn}
	}
	taskRun := taskLauncher.taskRunService.CreateTaskRunWithOrigin(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
	return launchTaskRun{TaskRunID: taskRun.TaskRunID, IsOpenedByHost: true}
}

func (taskLauncher *TaskLauncher) closeAbandonedLaunchTaskRun(openedTaskRun launchTaskRun, turnTaskRunID string, requesterPersonID string) {
	if !openedTaskRun.IsOpenedByHost {
		return
	}
	usedTaskRunID := strings.TrimSpace(turnTaskRunID)
	if usedTaskRunID == "" || usedTaskRunID == openedTaskRun.TaskRunID {
		return
	}
	if _, isFound := taskLauncher.taskRunService.FindTaskRun(openedTaskRun.TaskRunID); !isFound {
		return
	}
	taskLauncher.taskRunService.AppendTaskEvent(openedTaskRun.TaskRunID, agentcontract.TaskEventTaskAbandonedByTurn, MarshalBody(map[string]string{
		"turnTaskRunID": usedTaskRunID,
	}))
	taskLauncher.taskRunService.CancelTaskRunWithReason(openedTaskRun.TaskRunID, requesterPersonID, "the turn ran on task run "+usedTaskRunID)
}

func (taskLauncher *TaskLauncher) launchTask(ctx context.Context, request TaskLaunchRequest) (TaskLaunchResult, error) {
	launchRecords := []launchStepRecord{}
	normalizedProfileName := normalizeProfileName(request.ProfileName)
	completeFailedLaunch := func(record launchStepRecord, toolNames []string) TaskLaunchResult {
		return taskLauncher.completeLaunchFailure(ctx, request, normalizedProfileName, toolNames, record.StepName, launchRecords, errorFromStepRecord(record))
	}
	resolvedEmail, record := runLaunchStep(ctx, &taskLaunchExecution{Launcher: taskLauncher, Request: request}, resolveRequesterEmailLaunchStep{})
	launchRecords = append(launchRecords, record)
	request.RequesterEmail = resolvedEmail
	if record.Error != "" {
		return completeFailedLaunch(record, nil), nil
	}
	request.PersonAccess = requesterPersonAccessForTaskLaunch(request)
	activeCircleRequest, record := runLaunchStep(ctx, &taskLaunchExecution{Launcher: taskLauncher, Request: request, NormalizedProfileName: normalizedProfileName}, resolveActiveCircleLaunchStep{})
	launchRecords = append(launchRecords, record)
	if record.Error != "" {
		return completeFailedLaunch(record, nil), nil
	}
	request.ActiveCircleID = activeCircleRequest.ActiveCircleID
	request.ActiveCircleConflict = activeCircleRequest.ActiveCircleConflict
	artifactManifest, record := runLaunchStep(ctx, &taskLaunchExecution{Launcher: taskLauncher, Request: request, NormalizedProfileName: normalizedProfileName}, conversationArtifactManifestLaunchStep{})
	launchRecords = append(launchRecords, record)
	if record.Error != "" {
		return completeFailedLaunch(record, nil), nil
	}
	request.ArtifactManifest = artifactManifest
	if !request.IsRuntimeRestartResume {
		request.ExecutionStartedAt = time.Now()
	}
	openedTaskRun := taskLauncher.openTaskRunForLaunch(request)
	request.ExistingTaskRunID = openedTaskRun.TaskRunID
	taskLauncher.taskRunService.AppendTaskEvent(request.ExistingTaskRunID, task.TaskEventLaunchOrigin, marshalTaskLaunchOrigin(request, normalizedProfileName))
	request.IsTaskRunOpenedForThisTurn = openedTaskRun.IsOpenedByHost
	taskEvents := subscribeToTaskRun(taskLauncher.taskRunService, request.ToolCallObserver, request.ExistingTaskRunID)
	defer taskEvents.stop()
	request.VisibleContext = taskLauncher.visibleContextWithArtifactManifest(request.VisibleContext, request.ArtifactManifest)
	execution := &taskLaunchExecution{
		Launcher:              taskLauncher,
		Request:               request,
		NormalizedProfileName: normalizedProfileName,
		TaskEvents:            taskEvents,
	}
	_, record = runLaunchStep(ctx, execution, provisionRequesterWorkspaceLaunchStep{})
	launchRecords = append(launchRecords, record)
	if record.Error != "" {
		return completeFailedLaunch(record, nil), nil
	}
	toolSet, record := runLaunchStep(ctx, execution, buildToolSetLaunchStep{})
	launchRecords = append(launchRecords, record)
	if record.Error != "" {
		return completeFailedLaunch(record, nil), nil
	}
	toolNames := toolSet.ListToolNames()
	registryAudit, record := runLaunchStep(ctx, execution, auditToolRegistryLaunchStep{ToolSet: toolSet})
	launchRecords = append(launchRecords, record)
	if record.Error != "" {
		return completeFailedLaunch(record, toolNames), nil
	}
	conversationScope := ConversationScopeForRequest(taskLauncher.toolCatalogBuilder.WorkspaceRootPath(), ToolCatalogRequest{
		RequesterPersonID:       request.RequesterPersonID,
		ConversationID:          request.ConversationID,
		ConversationType:        request.ConversationType,
		ConversationChannelID:   request.ConversationChannelID,
		ConversationChannelName: request.ConversationChannelName,
	})
	memoryResult, record := runLaunchStep(ctx, execution, loadMemoryLaunchStep{})
	launchRecords = append(launchRecords, record)
	carriedOutCalls, record := runLaunchStep(ctx, execution, carryOutApprovedCallLaunchStep{ToolSet: toolSet})
	launchRecords = append(launchRecords, record)
	if request.ExistingTaskRunID != "" {
		taskLauncher.taskRunService.AppendTaskEvent(request.ExistingTaskRunID, agentcontract.TaskEventAgentTaskLaunched, marshalTaskLaunchEvent(request, normalizedProfileName, toolNames, registryAudit, len(memoryResult.Facts)))
	}
	turnResult, record := runLaunchStep(ctx, execution, runTurnLaunchStep{
		MemoryFacts:       memoryResult.Facts,
		ToolSet:           toolSet,
		ConversationScope: conversationScope,
		CarriedOutCalls:   carriedOutCalls,
	})
	launchRecords = append(launchRecords, record)
	taskLauncher.closeAbandonedLaunchTaskRun(openedTaskRun, turnResult.TaskRun.TaskRunID, request.RequesterPersonID)
	if heldRun, isLeftHeld := taskLauncher.runLeftHeldByItsClient(ctx, record, request.ExistingTaskRunID); isLeftHeld {
		return TaskLaunchResult{TurnResult: agentcontract.AgentTurnResult{TaskRun: heldRun}, ToolNames: toolNames, NormalizedProfileName: normalizedProfileName}, nil
	}
	if record.Error != "" {
		if taskRunID := strings.TrimSpace(turnResult.TaskRun.TaskRunID); taskRunID != "" {
			request.ExistingTaskRunID = taskRunID
		}
		return completeFailedLaunch(record, toolNames), nil
	}
	launchedToolNames := turnResult.ToolNames
	if len(launchedToolNames) == 0 {
		launchedToolNames = toolNames
	}
	if turnResult.TaskRun.TaskRunID != "" {
		taskLauncher.appendLaunchStepRecords(turnResult.TaskRun.TaskRunID, launchRecords)
		if turnResult.TaskRun.TaskRunID != request.ExistingTaskRunID {
			taskLauncher.taskRunService.AppendTaskEvent(turnResult.TaskRun.TaskRunID, agentcontract.TaskEventAgentTaskLaunched, marshalTaskLaunchEvent(request, normalizedProfileName, launchedToolNames, registryAudit, len(memoryResult.Facts)))
		}
		if taskLauncher.toolCatalogBuilder.memoryStores != nil {
			taskLauncher.appendStoreMemoryLaunchEvents(turnResult.TaskRun.TaskRunID, request, memoryResult)
		}
		taskLauncher.appendAmbientDutyLaunchEvent(turnResult.TaskRun.TaskRunID, request)
		taskLauncher.taskRunService.AppendTaskEvent(turnResult.TaskRun.TaskRunID, agentcontract.TaskEventAgentConversationScope, MarshalBody(conversationScope))
	}
	return TaskLaunchResult{
		TurnResult:            turnResult,
		MemoryFacts:           memoryResult.Facts,
		ToolNames:             launchedToolNames,
		NormalizedProfileName: normalizedProfileName,
	}, nil
}

func (taskLauncher *TaskLauncher) runLeftHeldByItsClient(ctx context.Context, record launchStepRecord, taskRunID string) (agentcontract.TaskRun, bool) {
	if record.Error == "" || ctx.Err() == nil || strings.TrimSpace(taskRunID) == "" {
		return agentcontract.TaskRun{}, false
	}
	taskRun, isFound := taskLauncher.taskRunService.FindTaskRun(taskRunID)
	return taskRun, isFound && taskRun.Status == agentcontract.TaskStatusWaitingApproval
}

type provisionRequesterWorkspaceLaunchStep struct{}

type resolveRequesterEmailLaunchStep struct{}

func (resolveRequesterEmailLaunchStep) Name() string {
	return "resolve_requester_email"
}

func (resolveRequesterEmailLaunchStep) Run(_ context.Context, execution *taskLaunchExecution) (string, error) {
	return execution.Launcher.resolveRequesterEmail(execution.Request), nil
}

type resolveActiveCircleLaunchStep struct{}

func (resolveActiveCircleLaunchStep) Name() string {
	return "resolve_active_circle"
}

func (resolveActiveCircleLaunchStep) Run(_ context.Context, execution *taskLaunchExecution) (ToolCatalogRequest, error) {
	request := execution.Request
	return withResolvedActiveCircle(ToolCatalogRequest{
		Prompt:                  request.Prompt,
		ConversationChannelName: request.ConversationChannelName,
		PersonAccess:            request.PersonAccess,
		ActiveCircleID:          request.ActiveCircleID,
		ActiveCircleConflict:    request.ActiveCircleConflict,
	}), nil
}

type conversationArtifactManifestLaunchStep struct{}

func (conversationArtifactManifestLaunchStep) Name() string {
	return "conversation_artifact_manifest"
}

func (conversationArtifactManifestLaunchStep) Run(_ context.Context, execution *taskLaunchExecution) ([]agentcontract.ArtifactManifestEntry, error) {
	return execution.Launcher.conversationArtifactManifest(execution.Request, execution.NormalizedProfileName), nil
}

func (provisionRequesterWorkspaceLaunchStep) Name() string {
	return "provision_requester_workspace"
}

func (provisionRequesterWorkspaceLaunchStep) Run(ctx context.Context, execution *taskLaunchExecution) (struct{}, error) {
	provisioner := execution.Launcher.requesterWorkspaceProvisioner
	if provisioner == nil {
		return struct{}{}, nil
	}
	return struct{}{}, provisioner.ProvisionRequesterWorkspace(ctx, execution.Request.PersonAccess, execution.Launcher.toolCatalogBuilder.WorkspaceRootPath())
}

type buildToolSetLaunchStep struct{}

func (buildToolSetLaunchStep) Name() string {
	return "build_tool_set"
}

func (buildToolSetLaunchStep) Run(_ context.Context, execution *taskLaunchExecution) (*toolcontract.ToolSet, error) {
	if execution.Request.UseEmptyToolCatalog {
		return toolcontract.NewToolSet(nil), nil
	}
	return execution.Launcher.toolCatalogBuilder.BuildToolSet(
		execution.Launcher.toolCatalogRequestForLaunch(execution.Request, execution.NormalizedProfileName),
	), nil
}

type auditToolRegistryLaunchStep struct {
	ToolSet *toolcontract.ToolSet
}

func (auditToolRegistryLaunchStep) Name() string {
	return "audit_tool_registry"
}

func (step auditToolRegistryLaunchStep) Run(ctx context.Context, execution *taskLaunchExecution) (ToolRegistryAudit, error) {
	return execution.Launcher.toolCatalogBuilder.BuildToolRegistryAudit(ctx, step.ToolSet)
}

type loadMemoryLaunchStep struct{}

func (loadMemoryLaunchStep) Name() string {
	return "load_memory"
}

func (loadMemoryLaunchStep) Run(ctx context.Context, execution *taskLaunchExecution) (launchMemoryResult, error) {
	if execution.Launcher.toolCatalogBuilder.memoryStores == nil {
		return launchMemoryResult{}, nil
	}
	return recallLaunchMemory(ctx, execution), nil
}

const launchGraphMemorySearchTimeout = 8 * time.Second

func recallLaunchMemory(ctx context.Context, execution *taskLaunchExecution) launchMemoryResult {
	request := execution.Request
	recallContext, cancelRecall := context.WithTimeout(ctx, launchGraphMemorySearchTimeout)
	defer cancelRecall()
	builder := execution.Launcher.toolCatalogBuilder
	recalled, errorValue := builder.memoryStores.RecallAcross(
		recallContext,
		request.PersonAccess,
		builder.memoryScopes(request.PersonAccess),
		request.Prompt,
		memory.DefaultRecallLimit,
	)
	if errorValue != nil {
		return launchMemoryResult{Error: errorValue.Error()}
	}
	return launchMemoryResult{
		Facts:          recalled.Facts,
		IdentityCount:  memory.IdentityFactCount(recalled.Facts),
		RecalledCount:  len(recalled.Facts),
		Mode:           recalled.Mode,
		DegradedReason: recalled.DegradedReason,
	}
}

func (taskLauncher *TaskLauncher) appendStoreMemoryLaunchEvents(taskRunID string, request TaskLaunchRequest, memoryResult launchMemoryResult) {
	if memoryResult.Error != "" {
		taskLauncher.taskRunService.AppendTaskEvent(taskRunID, "memory.recall_failed", memoryResult.Error)
	} else {
		taskLauncher.taskRunService.AppendTaskEvent(taskRunID, "memory.recall_injected", MarshalBody(map[string]any{
			"identityCount":  memoryResult.IdentityCount,
			"recalledCount":  memoryResult.RecalledCount,
			"characters":     memoryFactCharacterCount(memoryResult.Facts),
			"mode":           memoryResult.Mode,
			"degradedReason": memoryResult.DegradedReason,
		}))
	}
	taskLauncher.taskRunService.AppendTaskEvent(taskRunID, "memory.extraction_context", MarshalBody(memory.ExtractionContext{
		RequesterName:  request.RequesterName,
		ActiveCircleID: request.ActiveCircleID,
		Platform:       request.Platform,
	}))
}

func memoryFactCharacterCount(facts []memory.MemoryFact) int {
	count := 0
	for _, fact := range facts {
		count += len([]rune(fact.Content))
	}
	return count
}

type runTurnLaunchStep struct {
	MemoryFacts       []memory.MemoryFact
	ToolSet           *toolcontract.ToolSet
	ConversationScope ConversationResourceScope
	CarriedOutCalls   []agentcontract.CarriedOutCall
}

func (runTurnLaunchStep) Name() string {
	return "run_turn"
}

func (step runTurnLaunchStep) Run(ctx context.Context, execution *taskLaunchExecution) (agentcontract.AgentTurnResult, error) {
	turnRequest := execution.Launcher.agentTurnRequestForLaunch(
		execution.Request,
		execution.NormalizedProfileName,
		step.MemoryFacts,
		step.ToolSet,
		step.ConversationScope,
	)
	turnRequest.CarriedOutCalls = step.CarriedOutCalls
	turnRequest.TaskRunChosen = execution.TaskEvents.follow
	turnResult, errorValue := execution.Launcher.harness.RunTurn(toolcallprogress.WithObserver(ctx, execution.Request.ToolCallObserver), turnRequest)
	execution.Launcher.recordTurnInput(turnResult.TaskRun.TaskRunID, turnRequest)
	return turnResult, errorValue
}

func runLaunchStep[T any](ctx context.Context, execution *taskLaunchExecution, step taskLaunchStep[T]) (T, launchStepRecord) {
	startedAt := time.Now()
	result, errorValue := runLaunchStepRecoveringFromPanic(ctx, execution, step)
	record := launchStepRecord{
		StepName:        step.Name(),
		StartedAtUnixMs: startedAt.UnixMilli(),
		DurationMs:      time.Since(startedAt).Milliseconds(),
	}
	if errorValue != nil {
		record.Status = launchStepStatusError
		record.Error = errorValue.Error()
		record.errorValue = errorValue
		return result, record
	}
	record.Status = launchStepStatusResult
	return result, record
}

func runLaunchStepRecoveringFromPanic[T any](ctx context.Context, execution *taskLaunchExecution, step taskLaunchStep[T]) (result T, errorValue error) {
	defer func() {
		panicValue := recover()
		if panicValue == nil {
			return
		}
		errorValue = fmt.Errorf("%s panicked: %v", step.Name(), panicValue)
	}()
	return step.Run(ctx, execution)
}

func errorFromStepRecord(record launchStepRecord) error {
	if record.errorValue != nil {
		return record.errorValue
	}
	return errors.New(record.Error)
}

func (taskLauncher *TaskLauncher) completeLaunchFailure(ctx context.Context, request TaskLaunchRequest, profileName string, toolNames []string, stepName string, records []launchStepRecord, errorValue error) TaskLaunchResult {
	turnRequest := taskLauncher.agentTurnRequestForLaunch(request, profileName, nil, nil, ConversationResourceScope{})
	turnResult := taskLauncher.launchFailureCompleter.CompleteLaunchFailure(ctx, turnRequest, "launch", stepName, errorValue)
	turnResult.ToolNames = append([]string{}, toolNames...)
	taskLauncher.appendLaunchStepRecords(turnResult.TaskRun.TaskRunID, records)
	taskLauncher.appendAmbientDutyLaunchEvent(turnResult.TaskRun.TaskRunID, request)
	return TaskLaunchResult{
		TurnResult:            turnResult,
		ToolNames:             append([]string{}, toolNames...),
		NormalizedProfileName: profileName,
	}
}

func (taskLauncher *TaskLauncher) agentTurnRequestForLaunch(request TaskLaunchRequest, profileName string, memoryFacts []memory.MemoryFact, toolSet *toolcontract.ToolSet, conversationScope ConversationResourceScope) agentcontract.AgentTurnRequest {
	pinnedToolNames := append([]string{}, request.PinnedToolNames...)
	if toolSet != nil && toolSet.IsAllowed(contextualMemorySearchToolName) {
		pinnedToolNames = appendUniqueString(pinnedToolNames, contextualMemorySearchToolName)
	}
	turnRequest := agentcontract.AgentTurnRequest{
		ArtifactManifest:           request.ArtifactManifest,
		TurnStartedAt:              request.TurnStartedAt,
		ExecutionStartedAt:         request.ExecutionStartedAt,
		EnvironmentNow:             request.TurnStartedAt,
		Company:                    taskLauncher.company(),
		RequesterPersonID:          request.RequesterPersonID,
		RequesterEmail:             request.RequesterEmail,
		RequesterName:              request.RequesterName,
		RequesterPlatformUserID:    request.RequesterPlatformUserID,
		SourceReference:            request.SourceReference,
		IsRuntimeRestartResume:     request.IsRuntimeRestartResume,
		ExistingTaskRunID:          request.ExistingTaskRunID,
		IsTaskRunOpenedForThisTurn: request.IsTaskRunOpenedForThisTurn,
		OriginReplyTargetID:        request.OriginReplyTargetID,
		OriginIsThread:             request.OriginIsThread,
		Platform:                   request.Platform,
		RequesterCallingName:       request.RequesterCallingName,
		RequesterHandle:            request.RequesterHandle,
		RequesterCircles:           append([]string{}, request.PersonAccess.Circles...),
		ProfileName:                profileName,
		ConversationID:             request.ConversationID,
		ConversationType:           request.ConversationType,
		Prompt:                     request.Prompt,
		InputParts:                 append([]agentcontract.AgentPart{}, request.InputParts...),
		ResponseLanguage:           request.ResponseLanguage,
		VisibleContext:             request.VisibleContext,
		ActiveGoal:                 request.ActiveGoal,
		PriorTask:                  request.PriorTask,
		ScheduledRun:               request.ScheduledRun,
		TaskLevel:                  request.TaskLevel,
		PendingInput:               request.PendingInput,
		SkipSkillSelection:         request.SkipSkillSelection,
		MemoryFacts:                bluecollarMemoryFacts(memoryFacts),
		ToolSet:                    toolSet,
		PinnedToolNames:            pinnedToolNames,
		PinnedSkillNames:           append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:          taskLauncher.toolCatalogBuilder.WorkspaceRootPath(),
		WorkspaceDefaultPath:       conversationScope.DefaultDirectoryPath,
		WorkspaceGuidance:          workspaceGuidance(taskLauncher.toolCatalogBuilder.WorkspaceRootPath()),
		AgentIdentity:              taskLauncher.agentIdentity(),
		CheckpointSender:           request.CheckpointSender,
	}
	turnRequest.HostInstruction = hostInstructionForRequest(turnRequest)
	if requesterPersona := requesterPersonaInstruction(taskLauncher.toolCatalogBuilder.workspaceActorFactory, personAccessForLaunch(request), taskLauncher.toolCatalogBuilder.WorkspaceRootPath()); requesterPersona != "" {
		turnRequest.HostInstruction += "\n\n" + requesterPersona
	}
	return turnRequest
}

func appendUniqueString(values []string, value string) []string {
	for _, existingValue := range values {
		if existingValue == value {
			return values
		}
	}
	return append(values, value)
}

func conversationArtifactStore(taskArtifactService *task.TaskArtifactService) taskstate.TaskArtifactStore {
	if taskArtifactService == nil {
		return nil
	}
	return taskArtifactService
}

func (taskLauncher *TaskLauncher) conversationArtifactManifest(request TaskLaunchRequest, profileName string) []agentcontract.ArtifactManifestEntry {
	if taskLauncher.toolCatalogBuilder.taskRunService == nil {
		return nil
	}
	conversationScope := ConversationScopeForRequest(taskLauncher.toolCatalogBuilder.WorkspaceRootPath(), taskLauncher.toolCatalogRequestForLaunch(request, profileName))
	return buildConversationArtifactManifest(agentcontract.AgentTurnRequest{
		ConversationID:       request.ConversationID,
		ExistingTaskRunID:    request.ExistingTaskRunID,
		WorkspaceRootPath:    taskLauncher.toolCatalogBuilder.WorkspaceRootPath(),
		WorkspaceDefaultPath: conversationScope.DefaultDirectoryPath,
	}, taskLauncher.toolCatalogBuilder.taskRunService, conversationArtifactStore(taskLauncher.toolCatalogBuilder.taskArtifactService))
}

func (taskLauncher *TaskLauncher) visibleContextWithArtifactManifest(visibleContext agentcontract.VisibleContext, manifest []agentcontract.ArtifactManifestEntry) agentcontract.VisibleContext {
	for _, artifact := range manifest {
		visibleContext.Materials = append(visibleContext.Materials, agentcontract.VisibleContextMaterial{
			Filename:    filepath.Base(artifact.RelativePath),
			Path:        filepath.ToSlash(filepath.Join(taskLauncher.toolCatalogBuilder.WorkspaceRootPath(), artifact.RelativePath)),
			IsAvailable: true,
		})
	}
	return visibleContext
}

func (taskLauncher *TaskLauncher) appendAmbientDutyLaunchEvent(taskRunID string, request TaskLaunchRequest) {
	ambientDuty := request.AmbientDuty.Normalized()
	if !ambientDuty.IsMatch {
		return
	}
	taskLauncher.taskRunService.AppendTaskEvent(taskRunID, task.TaskEventAmbientDutyLaunch, MarshalBody(map[string]any{
		"dutyName":   ambientDuty.Name,
		"confidence": ambientDuty.Confidence,
	}))
}

func (taskLauncher *TaskLauncher) appendLaunchStepRecords(taskRunID string, records []launchStepRecord) {
	for _, record := range records {
		taskLauncher.taskRunService.AppendTaskEvent(taskRunID, launchStepTaskEventName(record.Status), MarshalBody(record))
	}
}

func launchStepTaskEventName(status string) string {
	if status == launchStepStatusError {
		return agentcontract.TaskEventAgentLaunchStepError
	}
	return agentcontract.TaskEventAgentLaunchStepResult
}

func (taskLauncher *TaskLauncher) toolCatalogRequestForLaunch(request TaskLaunchRequest, profileName string) ToolCatalogRequest {
	return ToolCatalogRequest{
		ProfileName:   profileName,
		Prompt:        request.Prompt,
		RecordCatalog: request.RecordCatalog,
		ToolCallGate: taskLauncher.approvalGate.TurnGate(approvalgate.TurnContext{
			RequesterPersonID: request.RequesterPersonID,
			RequesterEmail:    request.RequesterEmail,
			ResponseLanguage:  request.ResponseLanguage,
			Prompt:            request.Prompt,
			Platform:          request.Platform,
			ConversationID:    request.ConversationID,
			ConversationType:  request.ConversationType,
			ChannelID:         request.ConversationChannelID,
			ReplyTargetID:     request.ReplyTargetID,
		}),
		VisibleContext:             request.VisibleContext,
		RequesterPersonID:          request.RequesterPersonID,
		RequesterName:              request.RequesterName,
		RequesterEmail:             request.RequesterEmail,
		RequesterPlatformUserID:    request.RequesterPlatformUserID,
		TaskSource:                 request.Source,
		IsScheduledRun:             request.Source == TaskLaunchSourceScheduled,
		ConversationID:             request.ConversationID,
		DeliveryConversationID:     request.DeliveryConversationID,
		ConversationType:           request.ConversationType,
		ConversationChannelID:      request.ConversationChannelID,
		ConversationChannelName:    request.ConversationChannelName,
		ActiveCircleID:             request.ActiveCircleID,
		ActiveCircleConflict:       request.ActiveCircleConflict,
		ReplyTargetID:              request.ReplyTargetID,
		Platform:                   request.Platform,
		HistoryCursor:              request.VisibleContext.HistoryCursor,
		HistoryProvider:            request.HistoryProvider,
		AttachmentMaterialResolver: request.AttachmentMaterialResolver,
		PersonAccess:               request.PersonAccess,
		AccessibleConversationIDs:  request.AccessibleConversationIDs,
		InputParts:                 append([]agentcontract.AgentPart{}, request.InputParts...),
		ScheduledRun:               request.ScheduledRun,
		RegisteredToolNameCeiling:  registeredToolNameCeilingForLaunch(request),
	}
}

func registeredToolNameCeilingForLaunch(request TaskLaunchRequest) []string {
	if request.Source == TaskLaunchSourceScheduled && request.ScheduledRun.ScheduleID == task.MorningBriefingScheduleID(request.RequesterPersonID) {
		return []string{"task_list", "event_list", "conversation_history", "memory_search", "persona_read"}
	}
	duty, isKnownDuty := inboundengagement.StandingDutyByName(request.AmbientDuty.Name)
	if !request.AmbientDuty.IsMatch || !isKnownDuty {
		return nil
	}
	return duty.ToolNames
}

func requesterPersonAccessForTaskLaunch(request TaskLaunchRequest) policy.PersonAccess {
	return requesterPersonAccess(request.RequesterPersonID, request.PersonAccess)
}

func requesterPersonAccess(requesterPersonID string, personAccess policy.PersonAccess) policy.PersonAccess {
	if strings.TrimSpace(personAccess.PersonID) == "" {
		personAccess.PersonID = strings.TrimSpace(requesterPersonID)
	}
	return policy.EnsureRequesterDefaults(personAccess)
}

func bluecollarMemoryFacts(facts []memory.MemoryFact) []agentcontract.MemoryFact {
	converted := make([]agentcontract.MemoryFact, 0, len(facts))
	for _, fact := range facts {
		converted = append(converted, agentcontract.MemoryFact{
			FactID:          fact.FactID,
			ScopeType:       fact.ScopeType,
			Content:         fact.Content,
			Score:           fact.Score,
			SourceEpisodeID: fact.SourceEpisodeID,
			SourceKind:      fact.SourceKind,
			ValidAt:         fact.ValidAt,
		})
	}
	return converted
}

func workspaceGuidance(workspaceRootPath string) []string {
	return []string{
		"Do all document work — build, edit, and deliver — directly in ~/documents/; save finished documents (Word, PDF, Excel, slides) as ~/documents/<name>.<ext> so a later edit or delete task finds them with ls ~/documents.",
		"Circle-shared files live under " + filepath.Join(workspaceRootPath, "circles") + "/<circleID> when the requester belongs to that circle.",
		filepath.Join(workspaceRootPath, ".blueclaw") + " is service-owned runtime state and is normally not writable from terminal tools.",
	}
}

func personAccessForLaunch(request TaskLaunchRequest) policy.PersonAccess {
	personAccess := request.PersonAccess
	if strings.TrimSpace(personAccess.PersonID) == "" {
		personAccess.PersonID = strings.TrimSpace(request.RequesterPersonID)
	}
	return personAccess
}
