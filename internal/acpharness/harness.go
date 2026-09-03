package acpharness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/turnbriefing"
	"github.com/yeomyeonggeori/blueclaw/internal/turnoutcome"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const toolCatalogServerName = "blueclaw"

type ToolCatalogPublisher interface {
	PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (endpointURL string, bearerToken string, revoke func(), errorValue error)
}

type AgentProcess interface {
	Start(ctx context.Context) (input io.Writer, output io.Reader, wait func() error, errorValue error)
}

type RequesterAgentProcess interface {
	StartAsRequester(ctx context.Context, processStarter security.WorkspaceProcessStarter, workspaceRootPath string) (input io.Writer, output io.Reader, wait func() error, errorValue error)
}

type RequesterProcessRunner interface {
	Requester(context.Context, security.WorkspaceActorRequest) (security.WorkspaceActor, error)
}

type Harness struct {
	agentProcess           AgentProcess
	toolCatalogPublisher   ToolCatalogPublisher
	taskRunStore           taskstate.TaskRunStore
	requesterProcessRunner RequesterProcessRunner
	workspaceRootPath      string
	outcomeClassifier      turnoutcome.Classifier

	toolCatalogBridgeCommandPath string
	instructionBundleLoader      func() agentcontract.InstructionBundle
}

func New(agentProcess AgentProcess, toolCatalogPublisher ToolCatalogPublisher, taskRunStore taskstate.TaskRunStore) *Harness {
	return &Harness{agentProcess: agentProcess, toolCatalogPublisher: toolCatalogPublisher, taskRunStore: taskRunStore}
}

func (harness *Harness) UseOutcomeClassifier(outcomeClassifier turnoutcome.Classifier) {
	harness.outcomeClassifier = outcomeClassifier
}

func (harness *Harness) UseToolCatalogBridge(commandPath string) {
	harness.toolCatalogBridgeCommandPath = commandPath
}

func (harness *Harness) UseRequesterProcessRunner(requesterProcessRunner RequesterProcessRunner, workspaceRootPath string) {
	harness.requesterProcessRunner = requesterProcessRunner
	harness.workspaceRootPath = workspaceRootPath
}

func (harness *Harness) startAgent(ctx context.Context, request agentcontract.AgentTurnRequest) (io.Writer, io.Reader, func() error, error) {
	if harness.requesterProcessRunner == nil {
		return harness.agentProcess.Start(ctx)
	}
	requesterAgentProcess, isRequesterAware := harness.agentProcess.(RequesterAgentProcess)
	if !isRequesterAware {
		return nil, nil, nil, errors.New("this agent process cannot run as the requester, and an agent that brings tools of its own may not run as the service account")
	}
	workspaceRootPath := harness.workspaceRootPath
	if strings.TrimSpace(workspaceRootPath) == "" {
		workspaceRootPath = request.WorkspaceRootPath
	}
	requesterActor, errorValue := harness.requesterProcessRunner.Requester(ctx, security.WorkspaceActorRequest{
		PersonAccess:      policy.PersonAccess{PersonID: request.RequesterPersonID},
		WorkspaceRootPath: workspaceRootPath,
	})
	if errorValue != nil {
		return nil, nil, nil, errorValue
	}
	processStarter, canStartProcess := requesterActor.(security.WorkspaceProcessStarter)
	if !canStartProcess {
		return nil, nil, nil, errors.New("this workspace actor cannot start a long-lived process, so the agent has no requester identity to run inside")
	}
	return requesterAgentProcess.StartAsRequester(ctx, processStarter, request.WorkspaceRootPath)
}

func (harness *Harness) RunTurn(ctx context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if harness.agentProcess == nil || harness.toolCatalogPublisher == nil {
		return agentcontract.AgentTurnResult{}, errors.New("acp harness needs an agent process and a tool catalog publisher")
	}
	if strings.TrimSpace(request.RequesterPersonID) == "" {
		return agentcontract.AgentTurnResult{}, errors.New("acp harness refuses a turn with no requester, because tools execute as the requester")
	}
	succeededToolRecorder := &turnoutcome.SucceededToolRecorder{}
	endpointURL, bearerToken, revokeToolCatalog, errorValue := harness.toolCatalogPublisher.PublishToolCatalog(mcpserver.RequesterToolSet{
		ObserveToolInvocation: succeededToolRecorder.Observe,
		RequesterPersonID:     request.RequesterPersonID,
		TaskRunID:             request.ExistingTaskRunID,
		ToolSet:               request.ToolSet,
		ResponseLanguage:      request.ResponseLanguage,
		Prompt:                request.Prompt,
		ToolAudience:          mcpserver.ToolAudienceSelfEquipped,
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	defer revokeToolCatalog()

	agentInput, agentOutput, waitForAgent, errorValue := harness.startAgent(ctx, request)
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	defer func() { _ = waitForAgent() }()

	turnObserver := &sessionObserver{taskRunStore: harness.taskRunStore, taskRunID: request.ExistingTaskRunID}
	connection := acp.NewClientSideConnection(turnObserver, agentInput, agentOutput)
	initializeResponse, errorValue := connection.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	toolCatalogServer, errorValue := harness.toolCatalogServer(initializeResponse.AgentCapabilities.McpCapabilities, endpointURL, bearerToken)
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	newSession, errorValue := connection.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        request.WorkspaceRootPath,
		McpServers: []acp.McpServer{toolCatalogServer},
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	promptResponse, errorValue := connection.Prompt(ctx, acp.PromptRequest{
		SessionId: newSession.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock(harness.promptForTurn(request))},
		Meta:      promptMetaForTurn(request),
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	return harness.turnResult(ctx, request, turnObserver, succeededToolRecorder, promptResponse.StopReason), nil
}

func (harness *Harness) toolCatalogServer(agentCapabilities acp.McpCapabilities, endpointURL string, bearerToken string) (acp.McpServer, error) {
	if agentCapabilities.Http {
		return acp.McpServer{Http: &acp.McpServerHttpInline{
			Type:    "http",
			Name:    toolCatalogServerName,
			Url:     endpointURL,
			Headers: []acp.HttpHeader{{Name: "Authorization", Value: "Bearer " + bearerToken}},
		}}, nil
	}
	bridgeCommandPath := strings.TrimSpace(harness.toolCatalogBridgeCommandPath)
	if bridgeCommandPath == "" {
		return acp.McpServer{}, errors.New("this agent takes tool catalogs over stdio, which every agent must, and no catalog bridge command is configured; running it without a catalog would answer from no tools at all")
	}
	return acp.McpServer{Stdio: &acp.McpServerStdio{
		Name:    toolCatalogServerName,
		Command: bridgeCommandPath,
		Args:    []string{mcpserver.StdioBridgeCommand},
		Env: []acp.EnvVariable{
			{Name: mcpserver.CatalogEndpointEnvironmentName, Value: endpointURL},
			{Name: mcpserver.CatalogTokenEnvironmentName, Value: bearerToken},
		},
	}}, nil
}

func (harness *Harness) turnResult(ctx context.Context, request agentcontract.AgentTurnRequest, turnObserver *sessionObserver, succeededToolRecorder *turnoutcome.SucceededToolRecorder, stopReason acp.StopReason) agentcontract.AgentTurnResult {
	finishMessage := turnObserver.agentMessage()
	calledToolNames := turnObserver.calledToolNames()
	taskRun := taskstate.TaskRun{Status: taskStatusForStopReason(stopReason)}
	failureReason := ""
	if stopReason == acp.StopReasonEndTurn {
		taskRun.Status, failureReason = harness.outcomeForEndedTurn(ctx, request, finishMessage, succeededToolRecorder.SucceededToolNames())
	}
	if harness.taskRunStore != nil && strings.TrimSpace(request.ExistingTaskRunID) != "" {
		if existingTaskRun, isFound := harness.taskRunStore.FindTaskRun(request.ExistingTaskRunID); isFound {
			taskRun = existingTaskRun
		}
	}
	if taskRun.Status != taskstate.TaskStatusCompleted && strings.TrimSpace(taskRun.FailureReason) == "" {
		taskRun.FailureReason = failureReason
	}
	return agentcontract.AgentTurnResult{
		TaskRun:       taskRun,
		FinishMessage: finishMessage,
		UserNotice:    finishMessage,
		ToolNames:     calledToolNames,
	}
}

func (harness *Harness) outcomeForEndedTurn(ctx context.Context, request agentcontract.AgentTurnRequest, finishMessage string, calledToolNames []string) (taskstate.TaskStatus, string) {
	if !harness.outcomeClassifier.IsConfigured() {
		return taskstate.TaskStatusCompleted, ""
	}
	verdict, errorValue := harness.outcomeClassifier.Classify(ctx, request.Prompt, finishMessage, calledToolNames)
	if errorValue != nil {
		return taskstate.TaskStatusFailed, "the runtime could not determine what this turn achieved: " + errorValue.Error()
	}
	return verdict.Status, verdict.Reason
}

func taskStatusForStopReason(stopReason acp.StopReason) taskstate.TaskStatus {
	switch stopReason {
	case acp.StopReasonEndTurn:
		return taskstate.TaskStatusCompleted
	case acp.StopReasonCancelled:
		return taskstate.TaskStatusCancelled
	case acp.StopReasonRefusal:
		return taskstate.TaskStatusBlocked
	default:
		return taskstate.TaskStatusFailed
	}
}

type sessionObserver struct {
	mutex           sync.Mutex
	messageSegments []string
	toolNames       []string
	taskRunStore    taskstate.TaskRunStore
	taskRunID       string
}

func (observer *sessionObserver) recordPermissionDecision(eventName string, toolCall acp.ToolCallUpdate, grantedPermission acp.PermissionOptionKind) {
	if observer.taskRunStore == nil || strings.TrimSpace(observer.taskRunID) == "" {
		return
	}
	record := map[string]any{"toolCallID": string(toolCall.ToolCallId)}
	if toolCall.Title != nil {
		record["title"] = strings.TrimSpace(*toolCall.Title)
	}
	if toolCall.Kind != nil {
		record["kind"] = string(*toolCall.Kind)
	}
	if toolCall.RawInput != nil {
		record["rawInput"] = toolCall.RawInput
	}
	if grantedPermission != "" {
		record["permission"] = string(grantedPermission)
	}
	encodedRecord, errorValue := json.Marshal(record)
	if errorValue != nil {
		return
	}
	observer.taskRunStore.AppendTaskEvent(observer.taskRunID, eventName, string(encodedRecord))
}

func (observer *sessionObserver) agentMessage() string {
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	return strings.TrimSpace(strings.Join(observer.messageSegments, ""))
}

func (observer *sessionObserver) calledToolNames() []string {
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	return append([]string{}, observer.toolNames...)
}

func (observer *sessionObserver) SessionUpdate(_ context.Context, notification acp.SessionNotification) error {
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	if agentMessage := notification.Update.AgentMessageChunk; agentMessage != nil && agentMessage.Content.Text != nil {
		observer.messageSegments = append(observer.messageSegments, agentMessage.Content.Text.Text)
	}
	if toolCall := notification.Update.ToolCall; toolCall != nil {
		observer.toolNames = append(observer.toolNames, toolCall.Title)
	}
	return nil
}

var errFilesystemAndTerminalGoThroughTheToolCatalog = errors.New("this client does not serve fs or terminal over ACP; blueclaw's file and terminal tools are published on the MCP tool catalog, where they execute as the requester's POSIX user under the approval gate and the event ledger")

func (observer *sessionObserver) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errFilesystemAndTerminalGoThroughTheToolCatalog
}

func (observer *sessionObserver) RequestPermission(_ context.Context, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	for _, allowedKind := range []acp.PermissionOptionKind{acp.PermissionOptionKindAllowAlways, acp.PermissionOptionKindAllowOnce} {
		for _, permissionOption := range request.Options {
			if permissionOption.Kind != allowedKind {
				continue
			}
			observer.recordPermissionDecision(taskstate.TaskEventHarnessToolPermitted, request.ToolCall, permissionOption.Kind)
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
				Selected: &acp.RequestPermissionOutcomeSelected{Outcome: "selected", OptionId: permissionOption.OptionId},
			}}, nil
		}
	}
	observer.recordPermissionDecision(taskstate.TaskEventHarnessToolRefused, request.ToolCall, "")
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Cancelled: &acp.RequestPermissionOutcomeCancelled{Outcome: "cancelled"},
	}}, nil
}

func promptMetaForTurn(request agentcontract.AgentTurnRequest) map[string]any {
	if len(request.CarriedOutCalls) == 0 {
		return nil
	}
	return map[string]any{agentcontract.CarriedOutCallMetaKey: request.CarriedOutCalls}
}

func (harness *Harness) promptForTurn(request agentcontract.AgentTurnRequest) string {
	sections := []string{}
	if preamble := turnbriefing.Preamble(request, harness.instructionPrompt()); preamble != "" {
		sections = append(sections, preamble)
	}
	sections = append(sections, request.Prompt)
	if harness.taskRunStore != nil && strings.TrimSpace(request.ExistingTaskRunID) != "" {
		if declinedCallNote := approvalgate.DeclinedCallNote(harness.taskRunStore.ListTaskEvent(request.ExistingTaskRunID)); declinedCallNote != "" {
			sections = append(sections, declinedCallNote)
		}
	}
	return strings.Join(sections, "\n\n")
}

func (harness *Harness) instructionPrompt() string {
	if harness.instructionBundleLoader == nil {
		return ""
	}
	return harness.instructionBundleLoader().Prompt
}

func (harness *Harness) UseInstructionBundleLoader(instructionBundleLoader func() agentcontract.InstructionBundle) {
	harness.instructionBundleLoader = instructionBundleLoader
}
