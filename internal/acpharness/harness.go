package acpharness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/toolcallprogress"
	"github.com/yeomyeonggeori/blueclaw/internal/toolcatalogtrust"
	"github.com/yeomyeonggeori/blueclaw/internal/turnbriefing"
	"github.com/yeomyeonggeori/blueclaw/internal/turnoutcome"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const ToolCatalogServerName = "blueclaw"

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
	permissionAsker        approvalgate.HarnessPermissionAsker
	toolCatalogTrust       toolcatalogtrust.Trust
	requesterProcessRunner RequesterProcessRunner
	workspaceRootPath      string
	outcomeClassifier      turnoutcome.Classifier

	toolCatalogBridgeCommandPath string
	instructionBundleLoader      func() agentcontract.InstructionBundle
	toolAudience                 mcpserver.ToolAudience
	promptMetaProvider           func(agentcontract.AgentTurnRequest) map[string]any
	checkpointMarkerKey          string
	ledgerExchange               *ledgerExchange
	turnResultMetaKey            string
	steerExtension               *steerExtension
	carriesTurnContextToTools    bool
	includesHostInstruction      bool
}

func New(agentProcess AgentProcess, toolCatalogPublisher ToolCatalogPublisher, taskRunStore taskstate.TaskRunStore) *Harness {
	return &Harness{agentProcess: agentProcess, toolCatalogPublisher: toolCatalogPublisher, taskRunStore: taskRunStore, toolAudience: mcpserver.ToolAudienceSelfEquipped}
}

func (harness *Harness) UseToolAudience(toolAudience mcpserver.ToolAudience) {
	harness.toolAudience = toolAudience
}

func (harness *Harness) UsePromptMeta(promptMetaProvider func(agentcontract.AgentTurnRequest) map[string]any) {
	harness.promptMetaProvider = promptMetaProvider
}

func (harness *Harness) UseCheckpointMarker(metaKey string) {
	harness.checkpointMarkerKey = metaKey
}

func (harness *Harness) UseHostInstruction() {
	harness.includesHostInstruction = true
}

func (harness *Harness) UseOutcomeClassifier(outcomeClassifier turnoutcome.Classifier) {
	harness.outcomeClassifier = outcomeClassifier
}

func (harness *Harness) UseToolCatalogTrust(toolCatalogTrust toolcatalogtrust.Trust) {
	harness.toolCatalogTrust = toolCatalogTrust
}

func (harness *Harness) UsePermissionAsker(permissionAsker approvalgate.HarnessPermissionAsker) {
	harness.permissionAsker = permissionAsker
}

func approvalRequestOf(request agentcontract.AgentTurnRequest) mcpserver.ApprovalRequest {
	return mcpserver.ApprovalRequest{
		RequesterPersonID: request.RequesterPersonID,
		TaskRunID:         request.ExistingTaskRunID,
		Prompt:            request.Prompt,
		ResponseLanguage:  request.ResponseLanguage,
		Platform:          request.Platform,
		ConversationID:    request.ConversationID,
		ReplyTargetID:     request.OriginReplyTargetID,
	}
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
		ToolAudience:          harness.toolAudience,
		TurnContext:           harness.turnContextForToolCalls(ctx),
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	defer revokeToolCatalog()

	agentInput, agentOutput, waitForAgent, errorValue := harness.startAgent(WithTurnRequest(ctx, request), request)
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	defer func() { _ = waitForAgent() }()

	turnObserver := harness.newSessionObserver(ctx, request)
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
		Meta:       harness.toolCatalogTrust.SessionMeta,
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	harness.advanceTaskRun(request)
	turnControl := harness.newTurnControl(ctx, connection, newSession.SessionId, request, turnObserver.isMirroring)
	defer turnControl.stop()
	turnObserver.onToolCallEnded = turnControl.toolCallEnded
	promptResponse, errorValue := turnControl.converse(ctx, acp.PromptRequest{
		SessionId: newSession.SessionId,
		Prompt:    harness.promptBlocksForTurn(request, initializeResponse.AgentCapabilities.PromptCapabilities.Image),
		Meta:      harness.promptMetaForTurn(request),
	})
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, errorValue
	}
	if carriedTurnResult, isCarried := harness.carriedTurnResult(promptResponse); isCarried {
		return harness.settledTurnResult(request, carriedTurnResult), nil
	}
	return harness.turnResult(ctx, request, turnObserver, succeededToolRecorder, promptResponse.StopReason), nil
}

func (harness *Harness) advanceTaskRun(request agentcontract.AgentTurnRequest) {
	if harness.turnResultMetaKey == "" || harness.taskRunStore == nil || strings.TrimSpace(request.ExistingTaskRunID) == "" {
		return
	}
	harness.taskRunStore.AdvanceTaskRun(request.ExistingTaskRunID, request.ProfileName)
}

func (harness *Harness) toolCatalogServer(agentCapabilities acp.McpCapabilities, endpointURL string, bearerToken string) (acp.McpServer, error) {
	if agentCapabilities.Http {
		return acp.McpServer{Http: &acp.McpServerHttpInline{
			Type:    "http",
			Name:    ToolCatalogServerName,
			Url:     endpointURL,
			Headers: []acp.HttpHeader{{Name: "Authorization", Value: "Bearer " + bearerToken}},
		}}, nil
	}
	bridgeCommandPath := strings.TrimSpace(harness.toolCatalogBridgeCommandPath)
	if bridgeCommandPath == "" {
		return acp.McpServer{}, errors.New("this agent takes tool catalogs over stdio, which every agent must, and no catalog bridge command is configured; running it without a catalog would answer from no tools at all")
	}
	return acp.McpServer{Stdio: &acp.McpServerStdio{
		Name:    ToolCatalogServerName,
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
	taskRun := agentcontract.TaskRun{Status: taskStatusForStopReason(stopReason)}
	failureReason := ""
	if stopReason == acp.StopReasonEndTurn {
		taskRun.Status, failureReason = harness.outcomeForEndedTurn(ctx, request, finishMessage, succeededToolRecorder.SucceededToolNames())
	}
	if harness.taskRunStore != nil && strings.TrimSpace(request.ExistingTaskRunID) != "" {
		if existingTaskRun, isFound := harness.taskRunStore.FindTaskRun(request.ExistingTaskRunID); isFound {
			taskRun = existingTaskRun
		}
	}
	if taskRun.Status != agentcontract.TaskStatusCompleted && strings.TrimSpace(taskRun.FailureReason) == "" {
		taskRun.FailureReason = failureReason
	}
	return agentcontract.AgentTurnResult{
		TaskRun:       taskRun,
		FinishMessage: finishMessage,
		UserNotice:    userNoticeOf(taskRun, finishMessage),
		ToolNames:     calledToolNames,
		Attachments:   succeededToolRecorder.StagedAttachments(),
	}
}

func userNoticeOf(taskRun agentcontract.TaskRun, finishMessage string) string {
	isWaiting := taskRun.Status == agentcontract.TaskStatusWaitingApproval || taskRun.Status == agentcontract.TaskStatusWaitingUserInput
	if isWaiting && strings.TrimSpace(finishMessage) == "" {
		return taskRun.FailureReason
	}
	return finishMessage
}

func (harness *Harness) outcomeForEndedTurn(ctx context.Context, request agentcontract.AgentTurnRequest, finishMessage string, calledToolNames []string) (agentcontract.TaskStatus, string) {
	if !harness.outcomeClassifier.IsConfigured() {
		return agentcontract.TaskStatusCompleted, ""
	}
	verdict, errorValue := harness.outcomeClassifier.Classify(ctx, request.Prompt, finishMessage, calledToolNames)
	if errorValue != nil {
		return agentcontract.TaskStatusFailed, "the runtime could not determine what this turn achieved: " + errorValue.Error()
	}
	return verdict.Status, verdict.Reason
}

func taskStatusForStopReason(stopReason acp.StopReason) agentcontract.TaskStatus {
	switch stopReason {
	case acp.StopReasonEndTurn:
		return agentcontract.TaskStatusCompleted
	case acp.StopReasonCancelled:
		return agentcontract.TaskStatusCancelled
	case acp.StopReasonRefusal:
		return agentcontract.TaskStatusBlocked
	default:
		return agentcontract.TaskStatusFailed
	}
}

type sessionObserver struct {
	mutex               sync.Mutex
	messageSegments     []string
	toolNames           []string
	taskRunStore        taskstate.TaskRunStore
	taskRunID           string
	toolCallObserver    toolcallprogress.Observer
	permissionAsker     approvalgate.HarnessPermissionAsker
	approvalRequest     mcpserver.ApprovalRequest
	ledgerMirror        ledgerMirror
	isMirroring         *mirroringFlag
	onToolCallEnded     func()
	checkpointMarkerKey string
	checkpointSender    agentcontract.AgentCheckpointSender
}

func (harness *Harness) newSessionObserver(ctx context.Context, request agentcontract.AgentTurnRequest) *sessionObserver {
	return &sessionObserver{
		taskRunStore:        harness.taskRunStore,
		taskRunID:           request.ExistingTaskRunID,
		toolCallObserver:    toolcallprogress.ObserverFrom(ctx),
		permissionAsker:     harness.permissionAsker,
		approvalRequest:     approvalRequestOf(request),
		isMirroring:         &mirroringFlag{},
		ledgerMirror:        ledgerMirror{exchange: harness.ledgerExchange, taskRunStore: harness.taskRunStore, taskRunID: request.ExistingTaskRunID},
		checkpointMarkerKey: harness.checkpointMarkerKey,
		checkpointSender:    request.CheckpointSender,
	}
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

func (observer *sessionObserver) forwardToolCall(update acp.SessionUpdate) {
	if observer.toolCallObserver == nil || (update.ToolCall == nil && update.ToolCallUpdate == nil) {
		return
	}
	observer.toolCallObserver(update)
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

func (observer *sessionObserver) SessionUpdate(ctx context.Context, notification acp.SessionNotification) error {
	update := notification.Update
	if !observer.mirrorLedger(update) {
		observer.forwardToolCall(update)
	}
	observer.routeCheckpoint(ctx, update)
	observer.record(update)
	observer.noteToolCallEnded(update)
	return nil
}

func (observer *sessionObserver) noteToolCallEnded(update acp.SessionUpdate) {
	toolCallUpdate := update.ToolCallUpdate
	if observer.onToolCallEnded == nil || toolCallUpdate == nil || toolCallUpdate.Status == nil {
		return
	}
	if *toolCallUpdate.Status == acp.ToolCallStatusCompleted || *toolCallUpdate.Status == acp.ToolCallStatusFailed {
		observer.onToolCallEnded()
	}
}

func (observer *sessionObserver) mirrorLedger(update acp.SessionUpdate) bool {
	if !observer.ledgerMirror.isActive() {
		return false
	}
	record, isRecorded := ledgerRecordOfUpdate(update)
	if !isRecorded {
		return false
	}
	observer.isMirroring.enter()
	defer observer.isMirroring.leave()
	observer.ledgerMirror.take(record)
	return true
}

func (observer *sessionObserver) routeCheckpoint(ctx context.Context, update acp.SessionUpdate) {
	thought := update.AgentThoughtChunk
	if observer.checkpointSender == nil || observer.checkpointMarkerKey == "" || thought == nil || thought.Content.Text == nil {
		return
	}
	marker, isMarked := thought.Meta[observer.checkpointMarkerKey].(map[string]any)
	if !isMarked {
		return
	}
	toolName, _ := marker["toolName"].(string)
	_ = observer.checkpointSender(ctx, agentcontract.AgentCheckpoint{TaskRunID: observer.taskRunID, Message: thought.Content.Text.Text, ToolName: toolName})
}

func (observer *sessionObserver) record(update acp.SessionUpdate) {
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	if agentMessage := update.AgentMessageChunk; agentMessage != nil && agentMessage.Content.Text != nil {
		observer.messageSegments = append(observer.messageSegments, agentMessage.Content.Text.Text)
	}
	if toolCall := update.ToolCall; toolCall != nil {
		observer.toolNames = append(observer.toolNames, toolNameOf(*toolCall))
	}
}

func toolNameOf(toolCall acp.SessionUpdateToolCall) string {
	if record, isRecorded := ledgerRecordOfMeta(toolCall.Meta); isRecorded {
		if toolName, isRequest := agentcontract.ToolTaskEventToolName(record.Name, agentcontract.ToolTaskEventRequestedSuffix); isRequest {
			return toolName
		}
	}
	return toolCall.Title
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

func (observer *sessionObserver) RequestPermission(ctx context.Context, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if observer.permissionAsker == nil {
		return observer.allowWithoutAsking(request), nil
	}
	return observer.askThePerson(ctx, request), nil
}

func (observer *sessionObserver) allowWithoutAsking(request acp.RequestPermissionRequest) acp.RequestPermissionResponse {
	for _, allowedKind := range []acp.PermissionOptionKind{acp.PermissionOptionKindAllowAlways, acp.PermissionOptionKindAllowOnce} {
		for _, permissionOption := range request.Options {
			if permissionOption.Kind != allowedKind {
				continue
			}
			observer.recordPermissionDecision(agentcontract.TaskEventHarnessToolPermitted, request.ToolCall, permissionOption.Kind)
			return selectedResponse(permissionOption.OptionId)
		}
	}
	observer.recordPermissionDecision(agentcontract.TaskEventHarnessToolRefused, request.ToolCall, "")
	return cancelledResponse()
}

func (observer *sessionObserver) askThePerson(ctx context.Context, request acp.RequestPermissionRequest) acp.RequestPermissionResponse {
	question := ""
	if request.ToolCall.Title != nil {
		question = strings.TrimSpace(*request.ToolCall.Title)
	}
	if question == "" || len(request.Options) == 0 {
		observer.recordPermissionDecision(agentcontract.TaskEventHarnessToolRefused, request.ToolCall, "")
		return cancelledResponse()
	}
	outcome, status := observer.permissionAsker.AskHarnessPermission(ctx, observer.approvalRequestFor(request.ToolCall, question), approvalgate.HarnessPermissionQuestion{
		Text:     question,
		ToolCall: request.ToolCall,
		Options:  request.Options,
	})
	if status != approvalgate.AskAnswered || outcome.Selected == nil {
		observer.recordPermissionDecision(agentcontract.TaskEventHarnessToolRefused, request.ToolCall, "")
		return cancelledResponse()
	}
	observer.recordSelectedOption(request, outcome.Selected.OptionId)
	return selectedResponse(outcome.Selected.OptionId)
}

func (observer *sessionObserver) recordSelectedOption(request acp.RequestPermissionRequest, optionID acp.PermissionOptionId) {
	for _, permissionOption := range request.Options {
		if permissionOption.OptionId != optionID {
			continue
		}
		if isAllowKind(permissionOption.Kind) {
			observer.recordPermissionDecision(agentcontract.TaskEventHarnessToolPermitted, request.ToolCall, permissionOption.Kind)
			return
		}
	}
	observer.recordPermissionDecision(agentcontract.TaskEventHarnessToolRefused, request.ToolCall, "")
}

func isAllowKind(optionKind acp.PermissionOptionKind) bool {
	return optionKind == acp.PermissionOptionKindAllowOnce || optionKind == acp.PermissionOptionKindAllowAlways
}

func (observer *sessionObserver) approvalRequestFor(toolCall acp.ToolCallUpdate, question string) mcpserver.ApprovalRequest {
	approvalRequest := observer.approvalRequest
	approvalRequest.ToolName = question
	if toolInput, errorValue := json.Marshal(toolCall.RawInput); errorValue == nil && toolCall.RawInput != nil {
		approvalRequest.ToolInput = toolInput
	}
	return approvalRequest
}

func selectedResponse(optionID acp.PermissionOptionId) acp.RequestPermissionResponse {
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Selected: &acp.RequestPermissionOutcomeSelected{Outcome: "selected", OptionId: optionID},
	}}
}

func cancelledResponse() acp.RequestPermissionResponse {
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Cancelled: &acp.RequestPermissionOutcomeCancelled{Outcome: "cancelled"},
	}}
}

func (harness *Harness) promptMetaForTurn(request agentcontract.AgentTurnRequest) map[string]any {
	promptMeta := map[string]any{}
	if len(request.CarriedOutCalls) > 0 {
		promptMeta[agentcontract.CarriedOutCallMetaKey] = request.CarriedOutCalls
	}
	if harness.promptMetaProvider != nil {
		maps.Copy(promptMeta, harness.promptMetaProvider(request))
	}
	maps.Copy(promptMeta, harness.ledgerExchange.replayMeta(harness.taskRunStore, request))
	if len(promptMeta) == 0 {
		return nil
	}
	return promptMeta
}

func (harness *Harness) promptForTurn(request agentcontract.AgentTurnRequest) string {
	sections := []string{}
	if preamble := turnbriefing.Preamble(request, harness.instructionPrompt()); preamble != "" {
		sections = append(sections, preamble)
	}
	if hostInstruction := harness.hostInstruction(request); hostInstruction != "" {
		sections = append(sections, hostInstruction)
	}
	sections = append(sections, request.Prompt)
	if harness.taskRunStore != nil && strings.TrimSpace(request.ExistingTaskRunID) != "" {
		if declinedCallNote := approvalgate.DeclinedCallNote(harness.taskRunStore.ListTaskEvent(request.ExistingTaskRunID)); declinedCallNote != "" {
			sections = append(sections, declinedCallNote)
		}
	}
	return strings.Join(sections, "\n\n")
}

func (harness *Harness) hostInstruction(request agentcontract.AgentTurnRequest) string {
	if !harness.includesHostInstruction {
		return ""
	}
	return strings.TrimSpace(request.HostInstruction)
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

func (harness *Harness) UseTurnContextOnToolCalls() {
	harness.carriesTurnContextToTools = true
}

func (harness *Harness) turnContextForToolCalls(ctx context.Context) context.Context {
	if !harness.carriesTurnContextToTools {
		return nil
	}
	return ctx
}
