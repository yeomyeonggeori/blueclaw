package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"github.com/yeomyeonggeori/bluememo"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type IngressGate interface {
	IsPaused() bool
}

type TaskIntakeGate interface {
	IsQuiesced() bool
}

type ConnectorEventRepository interface {
	TryInsertConnectorEvent(PlatformInboundEvent) (bool, ConnectorRuntimeResult, error)
	SaveConnectorResult(PlatformInboundEvent, ConnectorRuntimeResult) error
}

type ConnectorQueueRepository interface {
	TryEnqueueConnectorEvent(PlatformInboundEvent) (bool, ConnectorRuntimeResult, error)
	ClaimPendingConnectorEvents(int, time.Duration) ([]QueuedConnectorEvent, error)
	MarkConnectorEventSucceeded(PlatformInboundEvent, ConnectorRuntimeResult) error
	MarkConnectorEventFailed(QueuedConnectorEvent, error, time.Time) error
	ReleaseConnectorEventClaim(QueuedConnectorEvent, time.Time) error
}

type ConnectorOutboxRepository interface {
	EnqueueConnectorReply(PlatformInboundEvent, ReplyTarget, OutboundReply) (string, error)
	ClaimPendingConnectorReplies(int, time.Duration) ([]QueuedConnectorReply, error)
	MarkConnectorReplySent(QueuedConnectorReply, string) error
	MarkConnectorReplyFailed(QueuedConnectorReply, error, time.Time) error
}

type PlatformAdapter interface {
	Name() string
	ParseHTTPEvent(context.Context, *http.Request) (HTTPParseResult, error)
	ParseRealtimeEvent(context.Context, []byte, string) (PlatformInboundEvent, bool, error)
	ResolveIdentity(context.Context, string) (identity.PlatformAccountIdentity, error)
	StartProgress(context.Context, ReplyTarget) error
	StopProgress(context.Context, ReplyTarget) error
	SendReply(context.Context, ReplyTarget, OutboundReply) (string, error)
	FetchHistory(context.Context, string, int) (VisibleContext, error)
}

type ReplyEditingAdapter interface {
	EditReply(context.Context, ReplyTarget, string, string) error
}

type ReplyDeletingAdapter interface {
	DeleteReply(context.Context, ReplyTarget, string) error
}

type InputAttachmentImportingAdapter interface {
	ImportInputAttachments(context.Context, InputAttachmentImportRequest) (InputAttachmentImportResult, error)
}

type InputAttachmentImportRequest struct {
	MessageID           string            `json:"messageID"`
	TargetDirectoryPath string            `json:"targetDirectoryPath"`
	InputAttachments    []InputAttachment `json:"inputAttachments"`
}

type InputAttachmentImportResult struct {
	InputParts       []agentcontract.AgentPart `json:"inputParts,omitempty"`
	InputAttachments []InputAttachment         `json:"inputAttachments,omitempty"`
}

type MessageReactionAdapter interface {
	AddReaction(context.Context, ReactionTarget) error
}

type MessageReactionRemovalAdapter interface {
	RemoveReaction(context.Context, ReactionTarget) error
}

type ConnectorTransport interface {
	Name() string
	Platform() string
	Start(context.Context)
}

const connectorInboxWorkerCount = 4
const connectorOutboxWorkerCount = 2
const connectorWorkerIdleDelay = time.Second
const connectorClaimLeaseDuration = 15 * time.Minute
const connectorProgressHeartbeatInterval = 5 * time.Second
const connectorMaximumAttemptCount = 5
const connectorReplyKindSuccess = "success"
const connectorReplyKindCheckpoint = "checkpoint"

const ConnectorReplyKindProgress = "progress"
const connectorReplyKindUserNotice = "user_notice"
const connectorReplyKindPermissionNotice = "permission_notice"

type ConnectorRuntime struct {
	identityService        *identity.IdentityService
	unknownAccountResolver UnknownAccountResolver
	harness                agentcontract.Harness
	intakeDecider          IntakeDecider
	turnRouter             TurnRouter
	replyGenerator         ReplyGenerator
	launchFailureCompleter LaunchFailureCompleter
	taskRunService         *taskstate.TaskRunService
	taskEventService       *taskstate.TaskEventService
	taskLauncher           *agentruntime.TaskLauncher
	approvalGate           *approvalgate.Gate
	toolCatalogBuilder     *agentruntime.ToolCatalogBuilder
	workspaceActorFactory  security.WorkspaceActorFactory
	agentIdentityProvider  func() agentcontract.AgentIdentity
	companyProvider        func() agentcontract.CompanyContext
	companyLocaleProvider  func() string
	workspaceID            string
	adminTaskLinkBaseURL   string
	logger                 *slog.Logger

	mutex                   sync.Mutex
	retryMutex              sync.Mutex
	adapterByPlatform       map[string]PlatformAdapter
	processedResults        map[string]ConnectorRuntimeResult
	eventRepository         ConnectorEventRepository
	ingressGate             IngressGate
	taskIntakeGate          TaskIntakeGate
	taskWaitTokenRepository task.TaskWaitTokenRepository
	conversationLocks       map[string]*sync.Mutex
	pendingRequests         *pendingRequestStore
	sentAttachmentSources   *sentAttachmentSourceStore
	started                 bool
	inboxHeartbeats         []time.Time
	outboxHeartbeats        []time.Time
}

func NewConnectorRuntime(identityService *identity.IdentityService, harness agentcontract.Harness, taskRunService *taskstate.TaskRunService, taskEventService *taskstate.TaskEventService, logger *slog.Logger) *ConnectorRuntime {
	if logger == nil {
		logger = slog.Default()
	}
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, connectorRuntimeDefaultAllowedToolNames())

	return &ConnectorRuntime{
		identityService:       identityService,
		harness:               harness,
		taskRunService:        taskRunService,
		taskEventService:      taskEventService,
		toolCatalogBuilder:    toolCatalogBuilder,
		logger:                logger,
		adapterByPlatform:     map[string]PlatformAdapter{},
		processedResults:      map[string]ConnectorRuntimeResult{},
		conversationLocks:     map[string]*sync.Mutex{},
		pendingRequests:       newPendingRequestStore(),
		sentAttachmentSources: newSentAttachmentSourceStore(),
	}
}

func (connectorRuntime *ConnectorRuntime) RegisterAdapter(adapter PlatformAdapter) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	connectorRuntime.adapterByPlatform[adapter.Name()] = adapter
}

var ingressGateWaitBudget = 25 * time.Second
var ingressGatePollInterval = 500 * time.Millisecond

func (connectorRuntime *ConnectorRuntime) waitForIngressGate(ctx context.Context) bool {
	if connectorRuntime.ingressGate == nil || !connectorRuntime.ingressGate.IsPaused() {
		return true
	}
	deadline := time.Now().Add(ingressGateWaitBudget)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(ingressGatePollInterval):
		}
		if !connectorRuntime.ingressGate.IsPaused() {
			return true
		}
	}
	return false
}

func (connectorRuntime *ConnectorRuntime) planTurn(ctx context.Context, taskRunID string, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	if connectorRuntime.turnRouter == nil {
		return agentcontract.TurnDecision{}, errors.New("connector runtime has no turn router configured")
	}
	if request.TurnStartedAt.IsZero() {
		request.TurnStartedAt = time.Now()
	}
	if request.EnvironmentNow.IsZero() {
		request.EnvironmentNow = request.TurnStartedAt
	}
	if request.Company.IsEmpty() {
		request.Company = connectorRuntime.company()
	}
	callLedger := &agentcontract.IntakeCallLedger{}
	turnDecision, errorValue := connectorRuntime.turnRouter.PlanObserved(ctx, request, callLedger)
	if trimmedTaskRunID := strings.TrimSpace(taskRunID); trimmedTaskRunID != "" && connectorRuntime.taskRunService != nil {
		for _, callRecord := range callLedger.Records {
			connectorRuntime.taskRunService.AppendTaskEvent(trimmedTaskRunID, agentcontract.TaskEventLLMCall, agentruntime.MarshalBody(callRecord))
		}
	}
	return turnDecision, errorValue
}

func (connectorRuntime *ConnectorRuntime) Start(ctx context.Context) {
	if errorValue := connectorRuntime.restorePendingRequests(); errorValue != nil {
		connectorRuntime.logger.Error("connector.requests.restore_failed", slog.String("error", errorValue.Error()))
		return
	}
	if connectorRuntime.queueRepository() != nil {
		connectorRuntime.prepareConnectorWorkers("inbox", connectorInboxWorkerCount)
		for index := 0; index < connectorInboxWorkerCount; index++ {
			go connectorRuntime.runConnectorInboxWorker(ctx, index)
		}
	}
	if connectorRuntime.outboxRepository() != nil {
		connectorRuntime.prepareConnectorWorkers("outbox", connectorOutboxWorkerCount)
		for index := 0; index < connectorOutboxWorkerCount; index++ {
			go connectorRuntime.runConnectorOutboxWorker(ctx, index)
		}
	}
	connectorRuntime.mutex.Lock()
	connectorRuntime.started = true
	connectorRuntime.mutex.Unlock()
}

func (connectorRuntime *ConnectorRuntime) HandleHTTPEvent(ctx context.Context, platform string, request *http.Request) (ConnectorRuntimeResult, *HTTPResponse, error) {
	adapter, errorValue := connectorRuntime.findAdapter(platform)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, nil, errorValue
	}

	parseResult, errorValue := adapter.ParseHTTPEvent(ctx, request)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".ingress.malformed", slog.String("source", "http"), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{}, nil, errorValue
	}
	if parseResult.ImmediateResponse != nil {
		return ConnectorRuntimeResult{Handled: true, Platform: platform}, parseResult.ImmediateResponse, nil
	}
	if !parseResult.HasEvent {
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "no_event"}, nil, nil
	}

	parseResult.Event.Platform = platform
	parseResult.Event.Source = "http"
	result, errorValue := connectorRuntime.HandleInboundEvent(detachedConnectorContext(ctx), adapter, parseResult.Event)
	return result, nil, errorValue
}

func (connectorRuntime *ConnectorRuntime) HandleRealtimeEvent(ctx context.Context, platform string, payload []byte, source string) (ConnectorRuntimeResult, error) {
	adapter, errorValue := connectorRuntime.findAdapter(platform)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}

	event, hasEvent, errorValue := adapter.ParseRealtimeEvent(ctx, payload, source)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".realtime.malformed", slog.String("source", source), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{}, errorValue
	}
	if !hasEvent {
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "no_event"}, nil
	}

	event.Platform = platform
	event.Source = source
	return connectorRuntime.HandleInboundEvent(ctx, adapter, event)
}

func (connectorRuntime *ConnectorRuntime) HandleInboundEvent(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, error) {
	event.Platform = adapter.Name()
	if !connectorRuntime.waitForIngressGate(ctx) {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.deferred", slog.String("messageID", event.MessageID), slog.String("reason", "backup_prepare_active"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "backup_prepare_active"}, nil
	}
	if strings.TrimSpace(event.MessageID) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_message_id"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_message_id"}, nil
	}
	if strings.TrimSpace(event.ConversationID) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_conversation_id"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_conversation_id"}, nil
	}
	if strings.TrimSpace(event.SenderID) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_sender_id"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_sender_id"}, nil
	}
	if strings.TrimSpace(event.ReplyTargetID) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_reply_target_id"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_reply_target_id"}, nil
	}
	if strings.TrimSpace(event.Prompt) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_prompt"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_prompt"}, nil
	}
	if event.Context.HasMoreBefore && strings.TrimSpace(event.Context.HistoryCursor) == "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", "missing_history_cursor"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "missing_history_cursor"}, nil
	}

	if queueRepository := connectorRuntime.queueRepository(); queueRepository != nil {
		return connectorRuntime.enqueueInboundEvent(event, queueRepository)
	}
	if connectorRuntime.eventRepository != nil {
		return ConnectorRuntimeResult{}, errors.New("connector queue repository is required when connector event repository is configured")
	}

	return connectorRuntime.handleInboundEventImmediately(ctx, adapter, event)
}

func (connectorRuntime *ConnectorRuntime) handleInboundEventImmediately(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, error) {
	eventKey := event.DedupeKey()
	if connectorRuntime.eventRepository != nil {
		isDuplicate, result, errorValue := connectorRuntime.eventRepository.TryInsertConnectorEvent(event)
		if errorValue != nil {
			return ConnectorRuntimeResult{}, errorValue
		}
		if isDuplicate {
			result.Handled = true
			result.Platform = adapter.Name()
			result.Duplicate = true
			connectorRuntime.logger.Info("connector."+adapter.Name()+".event.suppressed", slog.String("source", event.Source), slog.String("reason", "duplicate"), slog.String("messageID", event.MessageID))
			return result, nil
		}
		result, errorValue = connectorRuntime.processPendingInboundEvent(ctx, adapter, event, adapter.SendReply, false)
		if errorValue != nil {
			return ConnectorRuntimeResult{}, errorValue
		}
		_ = connectorRuntime.eventRepository.SaveConnectorResult(event, result)
		return result, nil
	}
	if result, isFound := connectorRuntime.findProcessedResult(eventKey); isFound {
		result.Duplicate = true
		connectorRuntime.logger.Info("connector."+adapter.Name()+".event.suppressed", slog.String("source", event.Source), slog.String("reason", "duplicate"), slog.String("messageID", event.MessageID))
		return result, nil
	}

	result, errorValue := connectorRuntime.processPendingInboundEvent(ctx, adapter, event, adapter.SendReply, false)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}

	connectorRuntime.rememberProcessedResult(eventKey, result)
	return result, nil
}

func (connectorRuntime *ConnectorRuntime) processInboundEvent(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, error) {
	return connectorRuntime.processInboundEventWithReplySender(ctx, adapter, event, adapter.SendReply)
}

func (connectorRuntime *ConnectorRuntime) processInboundEventWithReplySender(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (ConnectorRuntimeResult, error) {
	event = withInboundDecision(event)
	ctx = withConnectorEvent(ctx, event)
	if event.TaskRetry != nil {
		return connectorRuntime.processTaskRetry(ctx, adapter, event, sendReply)
	}
	turn := &inboundTurn{adapter: adapter, platform: adapter.Name(), event: event, sendReply: sendReply, stopProgress: func() {}}
	connectorRuntime.logInboundEventReceived(turn)
	if result, isHandled, errorValue := connectorRuntime.admitInboundTurn(ctx, turn); isHandled {
		return result, errorValue
	}
	defer func() {
		if turn.isProgressStarted {
			turn.stopProgress()
		}
	}()
	if shouldStartProgressBeforeAddressing(turn.event) {
		turn.startProgress(connectorRuntime.startProgressHeartbeat(ctx, turn.adapter, turn.replyTarget))
	}
	if errorValue := ctx.Err(); errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	if result, isHandled, errorValue := connectorRuntime.resolveOpenInteractions(ctx, turn); isHandled {
		return result, errorValue
	}
	connectorRuntime.resolveTurnActiveGoal(ctx, turn)
	if result, isHandled := connectorRuntime.resolveTurnAddressing(ctx, turn); isHandled {
		return result, nil
	}
	connectorRuntime.prepareTurnForLaunch(ctx, turn)
	if errorValue := ctx.Err(); errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	return connectorRuntime.launchTurn(ctx, turn)
}

func (connectorRuntime *ConnectorRuntime) shouldDeferNewTaskLaunch(isApprovalContinuation bool, hasPendingAskInteraction bool, hasActiveGoal bool) bool {
	if connectorRuntime.taskIntakeGate == nil || !connectorRuntime.taskIntakeGate.IsQuiesced() {
		return false
	}
	return !isApprovalContinuation && !hasPendingAskInteraction && !hasActiveGoal
}

func (connectorRuntime *ConnectorRuntime) appendTaskExecutionDuration(taskRunID string, duration time.Duration) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventBlueclawTaskExecutionDuration, agentruntime.MarshalBody(map[string]any{
		"durationMs": duration.Milliseconds(),
	}))
}

func choiceReplyOptions(options []AskChoiceOption) []agentcontract.ChoiceReplyOption {
	replyOptions := []agentcontract.ChoiceReplyOption{}
	for _, option := range options {
		replyOptions = append(replyOptions, agentcontract.ChoiceReplyOption{
			Key:        strings.TrimSpace(option.Key),
			Label:      strings.TrimSpace(option.Label),
			ShortLabel: strings.TrimSpace(option.ShortLabel),
			Value:      strings.TrimSpace(option.Value),
		})
	}
	return replyOptions
}

func confirmationWasRejected(decision agentcontract.TurnDecision) bool {
	return decision.Approval != nil && *decision.Approval == agentcontract.ApprovalSignalReject
}

func precomputedTurnDecisionForLaunch(decision agentcontract.TurnDecision, hasDecision bool) *agentcontract.TurnDecision {
	if !hasDecision {
		return nil
	}
	return &decision
}

func (connectorRuntime *ConnectorRuntime) cancelPendingConfirmation(event PlatformInboundEvent, approval pendingApproval, decision agentcontract.TurnDecision) {
	_, _ = connectorRuntime.taskRunService.CancelTaskRunWithReason(approval.TaskRun.TaskRunID, approval.TaskRun.RequesterPersonID, "confirmation.replaced")
	connectorRuntime.taskRunService.AppendTaskEvent(approval.TaskRun.TaskRunID, agentcontract.TaskEventConfirmationReplaced, agentruntime.MarshalBody(map[string]string{
		"messageID": event.MessageID,
		"route":     string(decision.Route),
		"reason":    strings.TrimSpace(decision.Reason),
	}))
}

func (connectorRuntime *ConnectorRuntime) appendAskResolvedEvent(interaction AskInteraction, event PlatformInboundEvent, decision agentcontract.TurnDecision) {
	connectorRuntime.taskRunService.AppendTaskEvent(interaction.TaskRunID, agentcontract.TaskEventAskResolved, agentruntime.MarshalBody(map[string]any{
		"interactionID": strings.TrimSpace(interaction.InteractionID),
		"kind":          strings.TrimSpace(interaction.Kind),
		"messageID":     strings.TrimSpace(event.MessageID),
		"choices":       append([]string{}, decision.Choices...),
		"route":         strings.TrimSpace(string(decision.Route)),
		"reason":        strings.TrimSpace(decision.Reason),
	}))
}

func (connectorRuntime *ConnectorRuntime) handleRejectedConfirmation(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, replyTarget ReplyTarget, approval pendingApproval, decision agentcontract.ConfirmationReplyDecision, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (ConnectorRuntimeResult, error) {
	_, _ = connectorRuntime.taskRunService.CancelTaskRunWithReason(approval.TaskRun.TaskRunID, approval.TaskRun.RequesterPersonID, "confirmation.rejected")
	connectorRuntime.taskRunService.AppendTaskEvent(approval.TaskRun.TaskRunID, agentcontract.TaskEventConfirmationRejected, agentruntime.MarshalBody(map[string]string{
		"messageID": event.MessageID,
		"reason":    decision.Reason,
	}))
	reply, errorValue := connectorRuntime.replyGenerator.GenerateReply(ctx, rejectedConfirmationReplyPrompt(event.Prompt, approval.ResponseLanguage))
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".confirmation.reject_reply_failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: approval.TaskRun.TaskRunID, Reason: "confirmation_rejected"}, nil
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply})
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: approval.TaskRun.TaskRunID, Reason: "reply_failed"}, nil
	}
	connectorRuntime.logger.Info("connector."+adapter.Name()+".confirmation.rejected", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("replyDispatchID", dispatchID))
	return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: approval.TaskRun.TaskRunID, Reason: "confirmation_rejected", ReplyDispatchID: dispatchID}, nil
}

func rejectedConfirmationReplyPrompt(reply string, responseLanguage string) string {
	return strings.Join([]string{
		connectorResponseLanguageInstruction(responseLanguage),
		"The user rejected a pending confirmation. Write one brief user-facing reply saying the pending action has been cancelled.",
		"Latest user reply: " + strings.TrimSpace(reply),
	}, "\n")
}

func connectorResponseLanguageInstruction(responseLanguage string) string {
	if toolcontract.ResolveResponseLanguage(responseLanguage) == toolcontract.ResponseLanguageEnglish {
		return "Write in English."
	}
	return "Write in Korean."
}

func (connectorRuntime *ConnectorRuntime) completeApprovedPendingTask(pendingTaskRun task.TaskRun, continuationTaskRunID string, finishMessage string) {
	result := strings.TrimSpace(finishMessage)
	if result == "" {
		result = "Approved and continued in task " + continuationTaskRunID + "."
	}
	connectorRuntime.taskRunService.AppendTaskEvent(pendingTaskRun.TaskRunID, agentcontract.TaskEventApprovalContinued, agentruntime.MarshalBody(map[string]string{
		"continuationTaskRunID": continuationTaskRunID,
		"result":                result,
	}))
	_, _ = connectorRuntime.taskRunService.CompleteTaskRun(pendingTaskRun.TaskRunID, result)
}

func connectorReplyEventBody(event PlatformInboundEvent, reply OutboundReply, outboxID string, dispatchID string, reason string) map[string]string {
	return map[string]string{
		"taskRunID":  strings.TrimSpace(reply.TaskRunID),
		"replyKind":  strings.TrimSpace(reply.ReplyKind),
		"outboxID":   strings.TrimSpace(outboxID),
		"dispatchID": strings.TrimSpace(dispatchID),
		"messageID":  strings.TrimSpace(event.MessageID),
		"reason":     strings.TrimSpace(reason),
	}
}

func (connectorRuntime *ConnectorRuntime) appendConnectorReplyEvent(taskRunID string, name string, body map[string]string) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, name, agentruntime.MarshalBody(body))
}

func (connectorRuntime *ConnectorRuntime) sendCheckpointReply(ctx context.Context, platform string, event PlatformInboundEvent, replyTarget ReplyTarget, checkpoint agentcontract.AgentCheckpoint, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) error {
	message := strings.TrimSpace(checkpoint.Message)
	taskRunID := strings.TrimSpace(checkpoint.TaskRunID)
	reply := OutboundReply{
		Message:   message,
		TaskRunID: taskRunID,
		ReplyKind: connectorReplyKindCheckpoint,
	}
	if message == "" {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplySuppressed, connectorReplyEventBody(event, reply, "", "", "missing_checkpoint_message"))
		return errors.New("missing checkpoint message")
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, reply)
	if errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplyFailed, connectorReplyEventBody(event, reply, "", "", errorValue.Error()))
		connectorRuntime.logger.Error("connector."+platform+".checkpoint.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return errorValue
	}
	if connectorRuntime.outboxRepository() == nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplySent, connectorReplyEventBody(event, reply, "", dispatchID, ""))
	}
	connectorRuntime.logger.Info("connector."+platform+".checkpoint.sent", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("replyDispatchID", dispatchID))
	return nil
}

func (connectorRuntime *ConnectorRuntime) sendUserNoticeReply(ctx context.Context, platform string, event PlatformInboundEvent, taskRunID string, replyTarget ReplyTarget, turnResult agentcontract.AgentTurnResult, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (string, bool) {
	notice, failureNotice, missingReason := userNoticeReplyMessage(turnResult)
	if missingReason != "" {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplySuppressed, connectorReplyEventBody(event, OutboundReply{TaskRunID: taskRunID, ReplyKind: connectorReplyKindUserNotice}, "", "", missingReason))
		connectorRuntime.logger.Info("connector."+platform+".outbound.skipped", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("reason", missingReason))
		return "", false
	}
	if taskStatusRequiresFailureNotice(turnResult.TaskRun.Status) {
		notice += failureRunFooter(taskRunID, connectorRuntime.adminTaskLinkBaseURL)
	}
	reply := OutboundReply{
		Message:         notice,
		TaskRunID:       taskRunID,
		ReplyKind:       connectorReplyKindUserNotice,
		RecoveryActions: recoveryActionsForEvent(turnResult.RecoveryActions, event),
		FailureNotice:   failureNotice,
	}
	interaction, _ := latestAskInteraction(taskRunID, connectorRuntime.taskRunService.ListTaskEvent(taskRunID))
	reply.Interaction = optionalAskInteraction(interaction, event.SenderID)
	dispatchID, errorValue := sendReply(ctx, replyTarget, reply)
	if errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplyFailed, connectorReplyEventBody(event, reply, "", "", errorValue.Error()))
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return "", false
	}
	if connectorRuntime.outboxRepository() == nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplySent, connectorReplyEventBody(event, reply, "", dispatchID, ""))
	}
	connectorRuntime.recordTaskWaitTokenForReply(platform, event, replyTarget, reply, dispatchID)
	connectorRuntime.logger.Info("connector."+platform+".outbound.sent", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("replyDispatchID", dispatchID), slog.String("reason", "task_not_completed"))
	return dispatchID, true
}

func failureRunFooter(taskRunID string, adminTaskLinkBaseURL string) string {
	trimmedTaskRunID := strings.TrimSpace(taskRunID)
	if trimmedTaskRunID == "" {
		return ""
	}
	shortTaskRunID := trimmedTaskRunID
	if len(shortTaskRunID) > 6 {
		shortTaskRunID = shortTaskRunID[:6]
	}
	footer := "\n\n`" + shortTaskRunID + "`"
	trimmedAdminTaskLinkBaseURL := strings.TrimRight(strings.TrimSpace(adminTaskLinkBaseURL), "/")
	if trimmedAdminTaskLinkBaseURL != "" {
		footer = "\n\n[`" + shortTaskRunID + "`](" + trimmedAdminTaskLinkBaseURL + "/tasks/" + trimmedTaskRunID + ")"
	}
	return footer
}

func userNoticeReplyMessage(turnResult agentcontract.AgentTurnResult) (string, agentcontract.FailureNotice, string) {
	if taskStatusRequiresFailureNotice(turnResult.TaskRun.Status) {
		message := turnResult.FailureNotice.SendableMessage()
		if message != "" {
			return message, turnResult.FailureNotice, ""
		}
		if fallbackMessage := strings.TrimSpace(turnResult.UserNotice); fallbackMessage != "" {
			return fallbackMessage, turnResult.FailureNotice, ""
		}
		if persistedResult := strings.TrimSpace(turnResult.TaskRun.Result); persistedResult != "" {
			return persistedResult, turnResult.FailureNotice, ""
		}
		if rawReason := strings.TrimSpace(turnResult.TaskRun.FailureReason); rawReason != "" {
			return rawReason, turnResult.FailureNotice, ""
		}
		return "", turnResult.FailureNotice, "missing_failure_notice"
	}
	message := strings.TrimSpace(turnResult.UserNotice)
	if message == "" {
		return "", agentcontract.FailureNotice{}, "missing_user_notice"
	}
	return message, agentcontract.FailureNotice{}, ""
}

func taskStatusRequiresFailureNotice(status task.TaskStatus) bool {
	return status == task.TaskStatusFailed || status == task.TaskStatusBlocked
}

func optionalAskInteraction(interaction AskInteraction, targetPlatformUserID string) *AskInteraction {
	if strings.TrimSpace(interaction.Kind) == "" {
		return nil
	}
	interaction.TargetPlatformUserID = strings.TrimSpace(targetPlatformUserID)
	return &interaction
}

func recoveryActionsForEvent(recoveryActions []toolcontract.RecoveryAction, event PlatformInboundEvent) []toolcontract.RecoveryAction {
	enrichedRecoveryActions := []toolcontract.RecoveryAction{}
	for _, recoveryAction := range recoveryActions {
		if strings.TrimSpace(recoveryAction.Kind) == "" {
			continue
		}
		if strings.TrimSpace(recoveryAction.PlatformUserID) == "" {
			recoveryAction.PlatformUserID = strings.TrimSpace(event.SenderID)
		}
		enrichedRecoveryActions = append(enrichedRecoveryActions, recoveryAction)
	}
	return enrichedRecoveryActions
}

func (connectorRuntime *ConnectorRuntime) withInitialVisibleContext(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) PlatformInboundEvent {
	if len(event.Context.Messages) > 0 {
		return event
	}
	if !event.Context.HasMoreBefore && strings.TrimSpace(event.Context.HistoryCursor) == "" {
		return event
	}
	historyCursor := firstNonEmptyString(event.Context.HistoryCursor, event.ConversationID)
	if historyCursor == "" {
		return event
	}
	visibleContext, errorValue := adapter.FetchHistory(ctx, historyCursor, 20)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".history.fetch_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return event
	}
	if strings.TrimSpace(event.Context.HistoryCursor) == "" {
		event.Context.HistoryCursor = historyCursor
	}
	if len(visibleContext.Messages) == 0 && len(visibleContext.Materials) == 0 {
		return event
	}
	event.Context.Messages = visibleContext.Messages
	event.Context.Materials = append(event.Context.Materials, visibleContext.Materials...)
	event.Context.HasMoreBefore = visibleContext.HasMoreBefore
	event.Context.HistoryCursor = firstNonEmptyString(visibleContext.HistoryCursor, event.Context.HistoryCursor)
	event.Context.ResponseLanguage = firstNonEmptyString(event.Context.ResponseLanguage, visibleContext.ResponseLanguage)
	return event
}

func (connectorRuntime *ConnectorRuntime) buildTurnToolSet(adapter PlatformAdapter, event PlatformInboundEvent, personID string, personAccess policy.PersonAccess) *toolcontract.ToolSet {
	requesterEmail := connectorRuntime.requesterEmailForEvent(personID, event)
	return connectorRuntime.toolCatalogBuilder.BuildToolSet(agentruntime.ToolCatalogRequest{
		ProfileName:                "default",
		Prompt:                     event.Prompt,
		RequesterPersonID:          personID,
		RequesterName:              connectorRuntime.requesterNameForEvent(personID, event),
		RequesterEmail:             requesterEmail,
		RequesterPlatformUserID:    event.SenderID,
		ConversationID:             event.ConversationID,
		ConversationType:           event.Context.ConversationType,
		ConversationChannelID:      event.Context.ChannelID,
		ConversationChannelName:    event.Context.ChannelName,
		ReplyTargetID:              event.ReplyTargetID,
		Platform:                   adapter.Name(),
		HistoryCursor:              event.Context.HistoryCursor,
		HistoryProvider:            connectorHistoryProvider{adapter: adapter},
		AttachmentMaterialResolver: connectorAttachmentMaterialResolver{adapter: adapter, personID: personID, event: event, sentSources: connectorRuntime.sentAttachmentSources, attachmentWriter: connectorRuntime.attachmentWriterFor(personID)},
		PersonAccess:               personAccess,
		MemoryLabel:                connectorRuntime.memoryLabel(personAccess, event),
		AccessibleConversationIDs:  []string{event.ConversationID},
		InputParts:                 append([]agentcontract.AgentPart{}, event.InputParts...),
	})
}

func (connectorRuntime *ConnectorRuntime) requesterEmailForEvent(personID string, event PlatformInboundEvent) string {
	email := strings.ToLower(strings.TrimSpace(connectorRuntime.identityService.ResolvePersonPrimaryEmail(personID)))
	if email != "" {
		return email
	}
	return strings.ToLower(strings.TrimSpace(event.Context.Sender.Email))
}

func (connectorRuntime *ConnectorRuntime) requesterNameForEvent(personID string, event PlatformInboundEvent) string {
	if name := strings.TrimSpace(event.Context.Sender.Name); name != "" {
		return name
	}
	return strings.TrimSpace(connectorRuntime.identityService.ResolvePersonDisplayName(personID))
}

func (connectorRuntime *ConnectorRuntime) currentTaskLauncher() *agentruntime.TaskLauncher {
	if connectorRuntime.taskLauncher != nil {
		return connectorRuntime.taskLauncher
	}
	taskLauncher := agentruntime.NewTaskLauncher(connectorRuntime.harness, connectorRuntime.taskRunService, connectorRuntime.toolCatalogBuilder)
	taskLauncher.UseApprovalGate(connectorRuntime.approvalGate)
	taskLauncher.UseLaunchFailureCompleter(connectorRuntime.launchFailureCompleter)
	taskLauncher.UseTurnRouter(connectorRuntime.turnRouter)
	taskLauncher.UseRequesterEmailResolver(connectorRuntime.identityService)
	taskLauncher.UseAgentIdentityProvider(connectorRuntime.agentIdentityProvider)
	return taskLauncher
}

type connectorHistoryProvider struct {
	adapter PlatformAdapter
}

func (historyProvider connectorHistoryProvider) FetchHistory(ctx context.Context, historyCursor string, limit int) (agentcontract.VisibleContext, error) {
	visibleContext, errorValue := historyProvider.adapter.FetchHistory(ctx, historyCursor, limit)
	if errorValue != nil {
		return agentcontract.VisibleContext{}, errorValue
	}
	return visibleContext.ToAgentVisibleContext(), nil
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

func detachedConnectorContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func (connectorRuntime *ConnectorRuntime) memoryLabel(personAccess policy.PersonAccess, event PlatformInboundEvent) bluememo.SecurityLabel {
	channelPolicy, isFound := connectorRuntime.identityService.ResolveConversationPolicy(event.Platform, event.ConversationID)
	if isPrivateConversationID(event.ConversationID) {
		isFound = false
	}
	return memory.LabelForConversation(personAccess, channelPolicy, isFound)
}

func isPrivateConversationID(conversationID string) bool {
	return strings.HasPrefix(strings.TrimSpace(conversationID), "dm:")
}

func (connectorRuntime *ConnectorRuntime) authorizeSender(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (senderAuthorization, error) {
	personID, isFound := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(adapter.Name(), event.SenderID)
	if isFound {
		return senderAuthorization{PersonID: personID, IsAllowed: true, Platform: adapter.Name()}, nil
	}

	platformAccountIdentity, errorValue := adapter.ResolveIdentity(ctx, event.SenderID)
	if errorValue != nil {
		return senderAuthorization{Platform: adapter.Name()}, errorValue
	}
	platformAccountIdentity.Platform = adapter.Name()
	platformAccountIdentity.ExternalUserID = event.SenderID
	connectorRuntime.identityService.RememberPlatformAccount(platformAccountIdentity)

	directoryUnreachable := false
	personID, isFound = connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(adapter.Name(), event.SenderID)
	if !isFound {
		personID, isFound, directoryUnreachable = connectorRuntime.askTheHostAboutUnknownAccount(ctx, adapter.Name(), event.SenderID, event.MessageID, platformAccountIdentity)
	}
	return senderAuthorization{
		PersonID:             personID,
		IsAllowed:            isFound,
		Platform:             adapter.Name(),
		PlatformAccountEmail: platformAccountIdentity.Email,
		DirectoryUnreachable: directoryUnreachable,
	}, nil
}

func (connectorRuntime *ConnectorRuntime) askTheHostAboutUnknownAccount(ctx context.Context, platform string, externalUserID string, messageID string, platformAccountIdentity identity.PlatformAccountIdentity) (string, bool, bool) {
	if connectorRuntime.unknownAccountResolver == nil {
		return "", false, false
	}
	isKnown, errorValue := connectorRuntime.unknownAccountResolver.ResolveUnknownAccount(ctx, platform, externalUserID, platformAccountIdentity.Email)
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".directory.unreachable",
			slog.String("messageID", messageID),
			slog.String("email", platformAccountIdentity.Email),
			slog.String("error", errorValue.Error()))
		return "", false, true
	}
	if !isKnown {
		connectorRuntime.logger.Info("connector."+platform+".directory.answered",
			slog.String("messageID", messageID),
			slog.String("email", platformAccountIdentity.Email),
			slog.Bool("known", false))
		return "", false, false
	}
	connectorRuntime.identityService.RememberPlatformAccount(platformAccountIdentity)
	personID, isFound := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(platform, externalUserID)
	if !isFound {
		connectorRuntime.logger.Error("connector."+platform+".directory.answered",
			slog.String("messageID", messageID),
			slog.String("email", platformAccountIdentity.Email),
			slog.Bool("known", true),
			slog.String("error", "the host carries this address and this agent carries no person under it"))
		return "", false, true
	}
	return personID, isFound, false
}

func (connectorRuntime *ConnectorRuntime) buildReplyTarget(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ReplyTarget, error) {
	_ = ctx
	_ = adapter

	return ReplyTarget{
		ConversationID:     event.ConversationID,
		ReplyTargetID:      event.ReplyTargetID,
		AnsweringMessageID: event.MessageID,
		DedupeKey:          event.DedupeKey(),
	}, nil
}

func (connectorRuntime *ConnectorRuntime) startNarrating(ctx context.Context, adapter PlatformAdapter, replyTarget ReplyTarget) *turnNarrator {
	narrator := newTurnNarrator(adapter, replyTarget)
	if narrator == nil || connectorRuntime.taskEventService == nil {
		return nil
	}
	stopObserving := connectorRuntime.taskEventService.RegisterTurnObserver(func(rawTurnEvent taskstate.RawTurnEvent) {
		narrator.observe(ctx, rawTurnEvent)
	})
	narrator.stopObserving = stopObserving
	return narrator
}

func (connectorRuntime *ConnectorRuntime) startProgress(ctx context.Context, adapter PlatformAdapter, replyTarget ReplyTarget) func() {
	return connectorRuntime.startProgressHeartbeat(ctx, adapter, replyTarget)
}

func shouldStartProgressBeforeAddressing(event PlatformInboundEvent) bool {
	return !isMultiPersonConversation(event) || event.Context.Addressing.BotMentioned
}

func (connectorRuntime *ConnectorRuntime) startProgressHeartbeat(ctx context.Context, adapter PlatformAdapter, replyTarget ReplyTarget) func() {
	platform := adapter.Name()
	connectorRuntime.logger.Info("connector."+platform+".progress.started", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID))
	if errorValue := adapter.StartProgress(ctx, replyTarget); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".progress.start_failed", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID), slog.String("error", errorValue.Error()))
	}

	progressContext, stopHeartbeat := context.WithCancel(ctx)
	go connectorRuntime.refreshProgressUntilStopped(progressContext, adapter, replyTarget)

	return func() {
		stopHeartbeat()
		stopContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if errorValue := adapter.StopProgress(stopContext, replyTarget); errorValue != nil {
			connectorRuntime.logger.Warn("connector."+platform+".progress.stop_failed", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID), slog.String("error", errorValue.Error()))
		}
		connectorRuntime.logger.Info("connector."+platform+".progress.stopped", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID))
	}
}

func (connectorRuntime *ConnectorRuntime) refreshProgressUntilStopped(ctx context.Context, adapter PlatformAdapter, replyTarget ReplyTarget) {
	ticker := time.NewTicker(connectorProgressHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if errorValue := adapter.StartProgress(ctx, replyTarget); errorValue != nil {
				connectorRuntime.logger.Warn("connector."+adapter.Name()+".progress.refresh_failed", slog.String("conversationID", replyTarget.ConversationID), slog.String("replyTargetID", replyTarget.ReplyTargetID), slog.String("error", errorValue.Error()))
			}
		}
	}
}

func (connectorRuntime *ConnectorRuntime) findAdapter(platform string) (PlatformAdapter, error) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	adapter, isFound := connectorRuntime.adapterByPlatform[platform]
	if !isFound {
		return nil, errors.New("connector adapter not registered: " + platform)
	}
	return adapter, nil
}

func (connectorRuntime *ConnectorRuntime) conversationLock(name string) *sync.Mutex {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()
	lock, isFound := connectorRuntime.conversationLocks[name]
	if isFound {
		return lock
	}
	lock = &sync.Mutex{}
	connectorRuntime.conversationLocks[name] = lock
	return lock
}

func (connectorRuntime *ConnectorRuntime) findProcessedResult(eventKey string) (ConnectorRuntimeResult, bool) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	result, isFound := connectorRuntime.processedResults[eventKey]
	return result, isFound
}

func (connectorRuntime *ConnectorRuntime) rememberProcessedResult(eventKey string, result ConnectorRuntimeResult) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	connectorRuntime.processedResults[eventKey] = result
}

type connectorEventContextKey struct{}

func withConnectorEvent(ctx context.Context, event PlatformInboundEvent) context.Context {
	return context.WithValue(ctx, connectorEventContextKey{}, event)
}

func connectorEventFromContext(ctx context.Context) (PlatformInboundEvent, bool) {
	event, isFound := ctx.Value(connectorEventContextKey{}).(PlatformInboundEvent)
	return event, isFound
}

func (connectorRuntime *ConnectorRuntime) suppressDuplicateSourceTaskIfNeeded(platform string, event PlatformInboundEvent, personID string) (ConnectorRuntimeResult, bool) {
	sourceReference := event.DedupeKey()
	taskRun, isFound := connectorRuntime.findTaskRunBySourceReference(personID, sourceReference)
	if !isFound {
		return ConnectorRuntimeResult{}, false
	}
	connectorRuntime.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventConnectorDuplicateSourceSuppressed, agentruntime.MarshalBody(map[string]string{
		"messageID":       event.MessageID,
		"sourceReference": sourceReference,
	}))
	connectorRuntime.logger.Info("connector."+platform+".event.suppressed", slog.String("source", event.Source), slog.String("reason", "duplicate_source_reference"), slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRun.TaskRunID))
	return ConnectorRuntimeResult{Handled: true, Platform: platform, Duplicate: true, Reason: "duplicate_source_reference", TaskRunID: taskRun.TaskRunID}, true
}

func (connectorRuntime *ConnectorRuntime) findTaskRunBySourceReference(personID string, sourceReference string) (task.TaskRun, bool) {
	trimmedSourceReference := strings.TrimSpace(sourceReference)
	if trimmedSourceReference == "" {
		return task.TaskRun{}, false
	}
	var selectedTaskRun task.TaskRun
	isFound := false
	for _, taskRun := range connectorRuntime.taskRunService.ListTaskRunByPersonID(personID) {
		if !connectorRuntime.taskRunHasSourceReference(taskRun.TaskRunID, trimmedSourceReference) {
			continue
		}
		if isFound && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		isFound = true
	}
	return selectedTaskRun, isFound
}

func (connectorRuntime *ConnectorRuntime) taskRunHasSourceReference(taskRunID string, sourceReference string) bool {
	for _, taskEvent := range connectorRuntime.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name != agentcontract.TaskEventAgentTaskSource && taskEvent.Name != agentcontract.TaskEventAgentTaskLaunched {
			continue
		}
		if taskEventSourceReference(taskEvent) == sourceReference {
			return true
		}
	}
	return false
}

func taskEventSourceReference(taskEvent task.TaskEvent) string {
	var document struct {
		SourceReference string `json:"sourceReference"`
	}
	if json.Unmarshal([]byte(taskEvent.Body), &document) != nil {
		return ""
	}
	return strings.TrimSpace(document.SourceReference)
}

func (connectorRuntime *ConnectorRuntime) grantApprovalScopeForTask(taskRunID string) {
	scope := pendingApprovalScope(connectorRuntime.taskRunService.ListTaskEvent(taskRunID))
	if scope == "" {
		return
	}
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalScopeGranted, agentruntime.MarshalBody(map[string]string{"scope": scope}))
}

func pendingApprovalScope(taskEvents []agentcontract.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		if taskEvents[index].Name != agentcontract.TaskEventAskRequested {
			continue
		}
		var body struct {
			ApprovalScope string `json:"approvalScope"`
		}
		if json.Unmarshal([]byte(taskEvents[index].Body), &body) != nil {
			continue
		}
		return strings.TrimSpace(body.ApprovalScope)
	}
	return ""
}
