package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
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
	turnRouter                    TurnRouter
	intakeBudget                  IntakeBudget
	taskRunService                *taskstate.TaskRunService
	toolCatalogBuilder            *ToolCatalogBuilder
	requesterWorkspaceProvisioner RequesterWorkspaceProvisioner
	requesterEmailResolver        RequesterEmailResolver
	agentIdentityProvider         func() agentcontract.AgentIdentity
	approvalGate                  *approvalgate.Gate
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
	RequesterPlatformUserID    string
	IsApprovalContinuation     bool
	IsRuntimeRestartResume     bool
	ExistingTaskRunID          string
	IsTaskRunOpenedForThisTurn bool
	OriginReplyTargetID        string
	OriginIsThread             bool
	ProfileName                string
	Platform                   string
	ConversationID             string
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
	PrecomputedTurnDecision    *agentcontract.TurnDecision
	IsPrecomputedDecisionExact bool
	SkipSkillSelection         bool
	UseEmptyToolCatalog        bool
	AmbientDuty                agentcontract.AmbientDutyContext
	PinnedToolNames            []string
	PinnedSkillNames           []string
	HistoryProvider            HistoryProvider
	AttachmentMaterialResolver AttachmentMaterialResolver
	PersonAccess               policy.PersonAccess
	MemoryNamespaces           []memory.MemoryNamespace
	AccessibleConversationIDs  []string
	CheckpointSender           agentcontract.AgentCheckpointSender
	ArtifactManifest           []agentcontract.ArtifactManifestEntry
	TurnStartedAt              time.Time
}

type TaskLaunchResult struct {
	TurnResult            agentcontract.AgentTurnResult
	MemoryFacts           []memory.MemoryFact
	ToolNames             []string
	NormalizedProfileName string
}

type TaskMemoryRequest struct {
	Query                     string
	RequesterPersonID         string
	ConversationID            string
	PersonAccess              policy.PersonAccess
	MemoryNamespaces          []memory.MemoryNamespace
	AccessibleConversationIDs []string
}

type TaskPinnedMemoryRequest struct {
	RequesterPersonID string
}

type taskLaunchStep[T any] interface {
	Name() string
	Run(context.Context, *taskLaunchExecution) (T, error)
}

type taskLaunchExecution struct {
	Launcher              *TaskLauncher
	Request               TaskLaunchRequest
	NormalizedProfileName string
}

type launchStepRecord struct {
	StepName string `json:"stepName"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

type launchMemoryResult struct {
	Facts           []memory.MemoryFact
	PinnedFactCount int
	GraphFactCount  int
	Error           string
}

type IntakeBudget struct {
	TaskLevel         string
	MaxIterationCount int
	MaxToolCallCount  int
	MaxElapsedSecond  int
}

type IntakeElapsedCompleter interface {
	CompleteIntakeElapsed(context.Context, agentcontract.AgentTurnRequest, launchfailure.IntakeLimit) agentcontract.AgentTurnResult
}

func (taskLauncher *TaskLauncher) UseIntakeBudget(intakeBudget IntakeBudget) {
	taskLauncher.intakeBudget = intakeBudget
}

type TurnRouter interface {
	Plan(context.Context, agentcontract.AgentRequest) (agentcontract.TurnDecision, error)
	PlanObserved(context.Context, agentcontract.AgentRequest, *agentcontract.TurnRouterCallLedger) (agentcontract.TurnDecision, error)
}

func (taskLauncher *TaskLauncher) UseTurnRouter(turnRouter TurnRouter) {
	taskLauncher.turnRouter = turnRouter
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
	launchResult, routerCallRecords, errorValue := taskLauncher.launchRoutedTask(ctx, request)
	taskLauncher.appendTurnRouterCallRecords(launchResult.TurnResult.TaskRun.TaskRunID, routerCallRecords)
	return launchResult, errorValue
}

type launchTaskRun struct {
	TaskRunID      string
	IsOpenedByHost bool
}

func (taskLauncher *TaskLauncher) openTaskRunForLaunch(request TaskLaunchRequest) launchTaskRun {
	if existingTaskRunID := strings.TrimSpace(request.ExistingTaskRunID); existingTaskRunID != "" {
		return launchTaskRun{TaskRunID: existingTaskRunID}
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
	taskLauncher.taskRunService.AppendTaskEvent(openedTaskRun.TaskRunID, "task.abandoned_by_turn", marshalToolResult(map[string]string{
		"turnTaskRunID": usedTaskRunID,
	}))
	taskLauncher.taskRunService.CancelTaskRunWithReason(openedTaskRun.TaskRunID, requesterPersonID, "the turn ran on task run "+usedTaskRunID)
}

func (taskLauncher *TaskLauncher) appendTurnRouterCallRecords(taskRunID string, callRecords []agentcontract.LLMCallRecord) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	for _, callRecord := range callRecords {
		taskLauncher.taskRunService.AppendTaskEvent(taskRunID, "llm.call", marshalToolResult(callRecord))
	}
}

func (taskLauncher *TaskLauncher) launchRoutedTask(ctx context.Context, request TaskLaunchRequest) (TaskLaunchResult, []agentcontract.LLMCallRecord, error) {
	request.RequesterEmail = taskLauncher.resolveRequesterEmail(request)
	request.PersonAccess = requesterPersonAccessForTaskLaunch(request)
	normalizedProfileName := normalizeProfileName(request.ProfileName)
	activeCircleRequest := withResolvedActiveCircle(ToolCatalogRequest{
		Prompt:                  request.Prompt,
		ConversationChannelName: request.ConversationChannelName,
		PersonAccess:            request.PersonAccess,
		ActiveCircleID:          request.ActiveCircleID,
		ActiveCircleConflict:    request.ActiveCircleConflict,
	})
	request.ActiveCircleID = activeCircleRequest.ActiveCircleID
	request.ActiveCircleConflict = activeCircleRequest.ActiveCircleConflict
	request.ArtifactManifest = taskLauncher.conversationArtifactManifest(request, normalizedProfileName)
	turnDecision, routingOutcome := taskLauncher.routedTurnDecision(ctx, request, normalizedProfileName)
	routerCallRecords := routingOutcome.CallRecords
	if routingOutcome.DidElapse {
		return TaskLaunchResult{
			TurnResult:            taskLauncher.completeIntakeElapsed(ctx, request, normalizedProfileName),
			NormalizedProfileName: normalizedProfileName,
		}, routerCallRecords, nil
	}
	if routingOutcome.Error != nil {
		return TaskLaunchResult{
			TurnResult:            taskLauncher.completeTurnRouterFailure(ctx, request, normalizedProfileName, routingOutcome),
			NormalizedProfileName: normalizedProfileName,
		}, routerCallRecords, nil
	}
	request.PrecomputedTurnDecision = turnDecision
	openedTaskRun := taskLauncher.openTaskRunForLaunch(request)
	request.ExistingTaskRunID = openedTaskRun.TaskRunID
	request.IsTaskRunOpenedForThisTurn = openedTaskRun.IsOpenedByHost
	request.VisibleContext = taskLauncher.visibleContextWithArtifactManifest(request.VisibleContext, request.ArtifactManifest)
	execution := &taskLaunchExecution{
		Launcher:              taskLauncher,
		Request:               request,
		NormalizedProfileName: normalizedProfileName,
	}
	launchRecords := []launchStepRecord{}
	_, record := runLaunchStep(ctx, execution, provisionRequesterWorkspaceLaunchStep{})
	launchRecords = append(launchRecords, record)
	if record.Error != "" {
		return taskLauncher.completeLaunchFailure(ctx, request, normalizedProfileName, nil, record.StepName, launchRecords, errorFromStepRecord(record)), routerCallRecords, nil
	}
	toolSet, record := runLaunchStep(ctx, execution, buildToolSetLaunchStep{})
	launchRecords = append(launchRecords, record)
	toolNames := toolSet.ListToolNames()
	registryAudit, record := runLaunchStep(ctx, execution, auditToolRegistryLaunchStep{ToolSet: toolSet})
	launchRecords = append(launchRecords, record)
	if record.Error != "" {
		return taskLauncher.completeLaunchFailure(ctx, request, normalizedProfileName, toolNames, record.StepName, launchRecords, errorFromStepRecord(record)), routerCallRecords, nil
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
	turnResult, record := runLaunchStep(ctx, execution, runTurnLaunchStep{
		MemoryFacts:       memoryResult.Facts,
		ToolSet:           toolSet,
		ConversationScope: conversationScope,
		CarriedOutCalls:   carriedOutCalls,
	})
	launchRecords = append(launchRecords, record)
	taskLauncher.closeAbandonedLaunchTaskRun(openedTaskRun, turnResult.TaskRun.TaskRunID, request.RequesterPersonID)
	if record.Error != "" {
		if taskRunID := strings.TrimSpace(turnResult.TaskRun.TaskRunID); taskRunID != "" {
			request.ExistingTaskRunID = taskRunID
		}
		return taskLauncher.completeLaunchFailure(ctx, request, normalizedProfileName, toolNames, record.StepName, launchRecords, errorFromStepRecord(record)), routerCallRecords, nil
	}
	launchedToolNames := turnResult.ToolNames
	if len(launchedToolNames) == 0 {
		launchedToolNames = toolNames
	}
	if turnResult.TaskRun.TaskRunID != "" {
		taskLauncher.appendLaunchStepRecords(turnResult.TaskRun.TaskRunID, launchRecords)
		if memoryResult.Error != "" {
			taskLauncher.taskRunService.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "memory.pinned_load_failed", memoryResult.Error)
		} else {
			taskLauncher.taskRunService.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "memory.pinned_load_succeeded", marshalToolResult(map[string]any{
				"factCount":       len(memoryResult.Facts),
				"pinnedFactCount": memoryResult.PinnedFactCount,
				"graphFactCount":  memoryResult.GraphFactCount,
			}))
		}
		taskLauncher.taskRunService.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "agent.task_launched", marshalTaskLaunchEvent(request, normalizedProfileName, launchedToolNames, registryAudit, len(memoryResult.Facts)))
		taskLauncher.appendAmbientDutyLaunchEvent(turnResult.TaskRun.TaskRunID, request)
		taskLauncher.taskRunService.AppendTaskEvent(turnResult.TaskRun.TaskRunID, "agent.conversation_scope", marshalToolResult(conversationScope))
	}
	return TaskLaunchResult{
		TurnResult:            turnResult,
		MemoryFacts:           memoryResult.Facts,
		ToolNames:             launchedToolNames,
		NormalizedProfileName: normalizedProfileName,
	}, routerCallRecords, nil
}

type provisionRequesterWorkspaceLaunchStep struct{}

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
	pinnedMemoryFacts, errorValue := execution.Launcher.toolCatalogBuilder.LoadPinnedMemory(ctx, TaskPinnedMemoryRequest{
		RequesterPersonID: execution.Request.RequesterPersonID,
	})
	if errorValue != nil {
		return launchMemoryResult{Error: errorValue.Error()}, nil
	}
	graphMemoryFacts := searchLaunchGraphMemory(ctx, execution)
	return launchMemoryResult{
		Facts:           appendMemoryFacts(pinnedMemoryFacts, graphMemoryFacts),
		PinnedFactCount: len(pinnedMemoryFacts),
		GraphFactCount:  len(graphMemoryFacts),
	}, nil
}

const launchGraphMemorySearchTimeout = 8 * time.Second

func searchLaunchGraphMemory(ctx context.Context, execution *taskLaunchExecution) []memory.MemoryFact {
	toolCatalogBuilder := execution.Launcher.toolCatalogBuilder
	request := execution.Request
	if !toolCatalogBuilder.canSearchGraphMemory() || strings.TrimSpace(request.Prompt) == "" {
		return nil
	}
	catalogRequest := execution.Launcher.toolCatalogRequestForLaunch(request, execution.NormalizedProfileName)
	searchContext, cancelSearch := context.WithTimeout(ctx, launchGraphMemorySearchTimeout)
	defer cancelSearch()
	graphMemoryFacts, errorValue := toolCatalogBuilder.SearchMemory(searchContext, TaskMemoryRequest{
		Query:                     request.Prompt,
		RequesterPersonID:         request.RequesterPersonID,
		ConversationID:            request.ConversationID,
		PersonAccess:              request.PersonAccess,
		MemoryNamespaces:          searchMemoryNamespaces(catalogRequest),
		AccessibleConversationIDs: request.AccessibleConversationIDs,
	})
	if errorValue != nil {
		return nil
	}
	return graphMemoryFacts
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
	turnResult, errorValue := execution.Launcher.harness.RunTurn(ctx, turnRequest)
	execution.Launcher.recordModelVisibleContext(turnResult.TaskRun.TaskRunID, turnRequest)
	return turnResult, errorValue
}

func runLaunchStep[T any](ctx context.Context, execution *taskLaunchExecution, step taskLaunchStep[T]) (T, launchStepRecord) {
	result, errorValue := runLaunchStepRecoveringFromPanic(ctx, execution, step)
	if errorValue != nil {
		return result, launchStepRecord{StepName: step.Name(), Status: "error", Error: errorValue.Error()}
	}
	return result, launchStepRecord{StepName: step.Name(), Status: "result"}
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
	turnRequest := agentcontract.AgentTurnRequest{
		ArtifactManifest: request.ArtifactManifest,
		TurnStartedAt:    request.TurnStartedAt,
		// The appliance keeps the clock of the company it runs for. Without it the
		// agent is told the date is unknown and made to read a shell to find out,
		// on every request that turns on what day it is.
		EnvironmentNow:             request.TurnStartedAt,
		RequesterPersonID:          request.RequesterPersonID,
		RequesterEmail:             request.RequesterEmail,
		RequesterName:              request.RequesterName,
		RequesterPlatformUserID:    request.RequesterPlatformUserID,
		SourceReference:            request.SourceReference,
		IsApprovalContinuation:     request.IsApprovalContinuation,
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
		PrecomputedTurnDecision:    request.PrecomputedTurnDecision,
		IsPrecomputedDecisionExact: request.IsPrecomputedDecisionExact,
		SkipSkillSelection:         request.SkipSkillSelection,
		MemoryFacts:                bluecollarMemoryFacts(memoryFacts),
		ToolSet:                    toolSet,
		PinnedToolNames:            append([]string{}, request.PinnedToolNames...),
		PinnedSkillNames:           append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:          taskLauncher.toolCatalogBuilder.WorkspaceRootPath(),
		WorkspaceDefaultPath:       conversationScope.DefaultDirectoryPath,
		WorkspaceGuidance:          workspaceGuidance(taskLauncher.toolCatalogBuilder.WorkspaceRootPath()),
		AgentIdentity:              taskLauncher.agentIdentity(),
		CheckpointSender:           request.CheckpointSender,
	}
	turnRequest.HostInstruction = hostInstructionForRequest(turnRequest)
	return turnRequest
}

// A missing artifact service must reach the harness as an absent store, not as a
// non-nil port holding a nil pointer.
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
			FileHint:    artifact.FileHint,
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
	taskLauncher.taskRunService.AppendTaskEvent(taskRunID, "agent.ambient_duty_launch", marshalToolResult(map[string]any{
		"dutyName":   ambientDuty.Name,
		"confidence": ambientDuty.Confidence,
	}))
}

func (taskLauncher *TaskLauncher) appendLaunchStepRecords(taskRunID string, records []launchStepRecord) {
	for _, record := range records {
		eventName := "agent.launch_step." + record.Status
		taskLauncher.taskRunService.AppendTaskEvent(taskRunID, eventName, marshalToolResult(record))
	}
}

func (taskLauncher *TaskLauncher) toolCatalogRequestForLaunch(request TaskLaunchRequest, profileName string) ToolCatalogRequest {
	return ToolCatalogRequest{
		ProfileName: profileName,
		Prompt:      request.Prompt,
		ToolCallGate: taskLauncher.approvalGate.TurnGate(approvalgate.TurnContext{
			RequesterPersonID: request.RequesterPersonID,
			RequesterEmail:    request.RequesterEmail,
			ResponseLanguage:  request.ResponseLanguage,
			Prompt:            request.Prompt,
		}),
		VisibleContext:             request.VisibleContext,
		RequesterPersonID:          request.RequesterPersonID,
		RequesterName:              request.RequesterName,
		RequesterEmail:             request.RequesterEmail,
		RequesterPlatformUserID:    request.RequesterPlatformUserID,
		TaskSource:                 request.Source,
		IsScheduledRun:             request.Source == TaskLaunchSourceScheduled,
		IsApprovalContinuation:     request.IsApprovalContinuation,
		ConversationID:             request.ConversationID,
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
		MemoryNamespaces:           request.MemoryNamespaces,
		AccessibleConversationIDs:  request.AccessibleConversationIDs,
		InputParts:                 append([]agentcontract.AgentPart{}, request.InputParts...),
		ScheduledRun:               request.ScheduledRun,
		RegisteredToolNameCeiling:  registeredToolNameCeilingForLaunch(request),
	}
}

func registeredToolNameCeilingForLaunch(request TaskLaunchRequest) []string {
	duty, isKnownDuty := agentcontract.StandingDutyByName(request.AmbientDuty.Name)
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

// bluecollarMemoryFacts converts recalled facts into the loop's own shape. The
// loop carries its own type so it never depends on the service that stores them;
// this single call is where the two meet.
func bluecollarMemoryFacts(facts []memory.MemoryFact) []agentcontract.MemoryFact {
	converted := make([]agentcontract.MemoryFact, 0, len(facts))
	for _, fact := range facts {
		converted = append(converted, agentcontract.MemoryFact{
			FactID:            fact.FactID,
			ScopeType:         fact.ScopeType,
			NamespaceID:       fact.NamespaceID,
			Content:           fact.Content,
			Score:             fact.Score,
			SourceEpisodeID:   fact.SourceEpisodeID,
			SourceKind:        fact.SourceKind,
			ValidAt:           fact.ValidAt,
			SecurityLevelRank: fact.SecurityLevelRank,
			RequiredClasses:   append([]string{}, fact.RequiredClasses...),
		})
	}
	return converted
}

type routingOutcome struct {
	Error       error
	DidElapse   bool
	CallRecords []agentcontract.LLMCallRecord
}

func (taskLauncher *TaskLauncher) routedTurnDecision(ctx context.Context, request TaskLaunchRequest, profileName string) (*agentcontract.TurnDecision, routingOutcome) {
	if request.PrecomputedTurnDecision != nil || taskLauncher.turnRouter == nil {
		return request.PrecomputedTurnDecision, routingOutcome{}
	}
	routingContext, cancel := taskLauncher.intakeRoutingContext(ctx, request)
	defer cancel()
	callLedger := &agentcontract.TurnRouterCallLedger{}
	turnDecision, errorValue := taskLauncher.turnRouter.PlanObserved(routingContext, agentcontract.AgentRequest{
		RequesterPersonID: request.RequesterPersonID,
		ConversationID:    request.ConversationID,
		Prompt:            request.Prompt,
		ResponseLanguage:  request.ResponseLanguage,
		VisibleContext:    request.VisibleContext,
		ScheduledRun:      request.ScheduledRun,
		ActiveGoal:        request.ActiveGoal,
		PriorTask:         request.PriorTask,
		ToolSet:           taskLauncher.toolCatalogBuilder.BuildToolSet(taskLauncher.toolCatalogRequestForLaunch(request, profileName)),
	}, callLedger)
	if errorValue != nil {
		workDeadline := taskLauncher.intakeWorkDeadline(request)
		didElapse := !workDeadline.IsZero() && workDeadline.Before(time.Now())
		return nil, routingOutcome{Error: errorValue, DidElapse: didElapse, CallRecords: callLedger.Records}
	}
	return &turnDecision, routingOutcome{CallRecords: callLedger.Records}
}

func (taskLauncher *TaskLauncher) completeTurnRouterFailure(ctx context.Context, request TaskLaunchRequest, profileName string, outcome routingOutcome) agentcontract.AgentTurnResult {
	if taskLauncher.launchFailureCompleter == nil {
		return agentcontract.AgentTurnResult{}
	}
	turnResult := taskLauncher.launchFailureCompleter.CompleteLaunchFailure(ctx, taskLauncher.agentTurnRequestForLaunch(request, profileName, nil, nil, ConversationResourceScope{}), "routing", "turn_router", outcome.Error)
	taskLauncher.appendUnroutedLaunchAudit(turnResult.TaskRun.TaskRunID, request, profileName)
	return turnResult
}

func (taskLauncher *TaskLauncher) intakeWorkDeadline(request TaskLaunchRequest) time.Time {
	if taskLauncher.intakeBudget.MaxElapsedSecond <= 0 {
		return time.Time{}
	}
	turnStartedAt := request.TurnStartedAt
	if turnStartedAt.IsZero() {
		turnStartedAt = time.Now()
	}
	return turnStartedAt.Add(time.Duration(taskLauncher.intakeBudget.MaxElapsedSecond) * time.Second)
}

func (taskLauncher *TaskLauncher) intakeRoutingContext(ctx context.Context, request TaskLaunchRequest) (context.Context, context.CancelFunc) {
	workDeadline := taskLauncher.intakeWorkDeadline(request)
	if workDeadline.IsZero() {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, workDeadline)
}

func (taskLauncher *TaskLauncher) completeIntakeElapsed(ctx context.Context, request TaskLaunchRequest, profileName string) agentcontract.AgentTurnResult {
	intakeElapsedCompleter, isAvailable := taskLauncher.launchFailureCompleter.(IntakeElapsedCompleter)
	if !isAvailable {
		return agentcontract.AgentTurnResult{}
	}
	turnResult := intakeElapsedCompleter.CompleteIntakeElapsed(ctx, taskLauncher.agentTurnRequestForLaunch(request, profileName, nil, nil, ConversationResourceScope{}), launchfailure.IntakeLimit{
		TaskLevel:         taskLauncher.intakeBudget.TaskLevel,
		MaxIterationCount: taskLauncher.intakeBudget.MaxIterationCount,
		MaxToolCallCount:  taskLauncher.intakeBudget.MaxToolCallCount,
		MaxElapsedSecond:  taskLauncher.intakeBudget.MaxElapsedSecond,
		TurnStartedAt:     request.TurnStartedAt,
		WorkDeadline:      taskLauncher.intakeWorkDeadline(request),
	})
	taskLauncher.appendUnroutedLaunchAudit(turnResult.TaskRun.TaskRunID, request, profileName)
	return turnResult
}

func (taskLauncher *TaskLauncher) appendUnroutedLaunchAudit(taskRunID string, request TaskLaunchRequest, profileName string) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	taskLauncher.taskRunService.AppendTaskEvent(taskRunID, "agent.task_launched", marshalTaskLaunchEvent(request, profileName, nil, ToolRegistryAudit{}, 0))
}

func workspaceGuidance(workspaceRootPath string) []string {
	return []string{
		"Do all document work — build, edit, and deliver — directly in ~/documents/; save finished documents (Word, PDF, Excel, slides) as ~/documents/<name>.<ext> so a later edit or delete task finds them with ls ~/documents.",
		"Circle-shared files live under " + filepath.Join(workspaceRootPath, "circles") + "/<circleID> when the requester belongs to that circle.",
		filepath.Join(workspaceRootPath, ".blueclaw") + " is service-owned runtime state and is normally not writable from terminal tools.",
	}
}
