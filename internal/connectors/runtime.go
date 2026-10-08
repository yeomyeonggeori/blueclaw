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

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
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
const connectorReplyKindApprovalQuestion = "approval_question"
const connectorReplyKindDeliveryFailureNotice = "delivery_failure_notice"

type ConnectorRuntime struct {
	identityService        *identity.IdentityService
	unknownAccountResolver UnknownAccountResolver
	harness                agentcontract.Harness
	gatewayDecider         inboundengagement.Decider
	recordTasklessLLMCall  func(subjects []string, record agentcontract.LLMCallRecord)
	replyGenerator         ReplyGenerator
	launchFailureCompleter LaunchFailureCompleter
	noticeLanguageModel    model.LanguageModelProvider
	taskRunService         *taskstate.TaskRunService
	taskEventService       *taskstate.TaskEventService
	taskLauncher           *agentruntime.TaskLauncher
	approvalGate           *approvalgate.Gate
	approvalReplyReader    approvalreply.Reader
	askingThreads          *askingThreads
	directMessageOpener    DirectMessageOpener
	directMessageAccounts  DirectMessageAccounts
	toolCatalogBuilder     *agentruntime.ToolCatalogBuilder
	workspaceActorFactory  security.WorkspaceActorFactory
	agentIdentityProvider  func() agentcontract.AgentIdentity
	companyProvider        func() agentcontract.CompanyContext
	companyLocaleProvider  func() string
	workspaceID            string
	adminTaskLinkBaseURL   string
	logger                 *slog.Logger

	mutex                 sync.Mutex
	retryMutex            sync.Mutex
	adapterByPlatform     map[string]PlatformAdapter
	processedResults      map[string]ConnectorRuntimeResult
	eventRepository       ConnectorEventRepository
	ingressGate           IngressGate
	taskIntakeGate        TaskIntakeGate
	conversationLocks     map[string]*sync.Mutex
	pendingRequests       *pendingRequestStore
	sentAttachmentSources *sentAttachmentSourceStore
	started               bool
	inboxHeartbeats       []time.Time
	outboxHeartbeats      []time.Time
}

func NewConnectorRuntime(identityService *identity.IdentityService, harness agentcontract.Harness, taskRunService *taskstate.TaskRunService, taskEventService *taskstate.TaskEventService, logger *slog.Logger) *ConnectorRuntime {
	if logger == nil {
		logger = slog.Default()
	}
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, agentruntime.DefaultAllowedToolNames())

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
		askingThreads:         &askingThreads{expiry: approvalExpiry},
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

func (connectorRuntime *ConnectorRuntime) Start(ctx context.Context) {
	if errorValue := connectorRuntime.restorePendingRequests(); errorValue != nil {
		connectorRuntime.logger.Error("connector.requests.restore_failed", slog.String("error", errorValue.Error()))
		return
	}
	connectorRuntime.reawaitPendingHolds(ctx)
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

func (connectorRuntime *ConnectorRuntime) HandleHTTPEvent(ctx context.Context, platform string, request *http.Request) (ConnectorRuntimeResult, error) {
	adapter, errorValue := connectorRuntime.findAdapter(platform)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}

	parseResult, errorValue := adapter.ParseHTTPEvent(ctx, request)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".ingress.malformed", slog.String("source", "http"), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{}, errorValue
	}
	if !parseResult.HasEvent {
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "no_event"}, nil
	}

	parseResult.Event.Platform = platform
	parseResult.Event.Source = "http"
	return connectorRuntime.HandleInboundEvent(detachedConnectorContext(ctx), adapter, parseResult.Event)
}

func (connectorRuntime *ConnectorRuntime) HandleInboundEvent(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, error) {
	event.Platform = adapter.Name()
	if !connectorRuntime.waitForIngressGate(ctx) {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.deferred", slog.String("messageID", event.MessageID), slog.String("reason", "backup_prepare_active"))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: "backup_prepare_active"}, nil
	}
	if reason := malformedEventReason(event); reason != "" {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".ingress.malformed", slog.String("source", event.Source), slog.String("reason", reason))
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), Ignored: true, Reason: reason}, nil
	}

	if queueRepository := connectorRuntime.queueRepository(); queueRepository != nil {
		return connectorRuntime.enqueueInboundEvent(event, queueRepository)
	}
	if connectorRuntime.eventRepository != nil {
		return ConnectorRuntimeResult{}, errors.New("connector queue repository is required when connector event repository is configured")
	}

	return connectorRuntime.handleInboundEventImmediately(ctx, adapter, event)
}

func malformedEventReason(event PlatformInboundEvent) string {
	switch {
	case strings.TrimSpace(event.MessageID) == "":
		return "missing_message_id"
	case strings.TrimSpace(event.ConversationID) == "":
		return "missing_conversation_id"
	case strings.TrimSpace(event.SenderID) == "":
		return "missing_sender_id"
	case strings.TrimSpace(event.ReplyTargetID) == "":
		return "missing_reply_target_id"
	case strings.TrimSpace(event.Prompt) == "":
		return "missing_prompt"
	case event.Context.HasMoreBefore && strings.TrimSpace(event.Context.HistoryCursor) == "":
		return "missing_history_cursor"
	}
	return ""
}

func (connectorRuntime *ConnectorRuntime) handleInboundEventImmediately(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, error) {
	eventKey := event.DedupeKey()
	if result, isFound := connectorRuntime.findProcessedResult(eventKey); isFound {
		result.Duplicate = true
		connectorRuntime.logger.Info("connector."+adapter.Name()+".event.suppressed", slog.String("source", event.Source), slog.String("reason", "duplicate"), slog.String("messageID", event.MessageID))
		return result, nil
	}

	result, errorValue := connectorRuntime.processPendingInboundEvent(ctx, adapter, event, connectorRuntime.recordingDelivery(adapter.SendReply), false)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}

	connectorRuntime.rememberProcessedResult(eventKey, result)
	return result, nil
}

func (connectorRuntime *ConnectorRuntime) processInboundEventWithReplySender(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (ConnectorRuntimeResult, error) {
	event = withGatewayDecision(event)
	ctx = withConnectorEvent(ctx, event)
	if event.TaskRetry != nil {
		return connectorRuntime.processTaskRetry(ctx, adapter, event, sendReply)
	}
	turn := &inboundTurn{adapter: adapter, platform: adapter.Name(), event: event, sendReply: sendReply}
	ctx = withWaitHandoff(ctx, waitHandoffFrom(ctx).pausingProgressOf(connectorRuntime, ctx, turn))
	connectorRuntime.logInboundEventReceived(turn)
	if result, isHandled, errorValue := connectorRuntime.admitInboundTurn(ctx, turn); isHandled {
		return result, errorValue
	}
	defer turn.endProgress()
	if shouldStartProgressBeforeAddressing(turn.event) {
		connectorRuntime.showTurnProgress(ctx, turn)
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

func (connectorRuntime *ConnectorRuntime) shouldDeferNewTaskLaunch(hasPendingAskInteraction bool, hasActiveGoal bool) bool {
	if connectorRuntime.taskIntakeGate == nil || !connectorRuntime.taskIntakeGate.IsQuiesced() {
		return false
	}
	return !hasPendingAskInteraction && !hasActiveGoal
}

func (connectorRuntime *ConnectorRuntime) appendTaskExecutionDuration(taskRunID string, duration time.Duration) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, task.TaskEventBlueclawTaskExecutionDuration, agentruntime.MarshalBody(map[string]any{
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

func (connectorRuntime *ConnectorRuntime) appendAskResolvedEvent(interaction AskInteraction, event PlatformInboundEvent) {
	connectorRuntime.taskRunService.AppendTaskEvent(interaction.TaskRunID, agentcontract.TaskEventAskResolved, agentruntime.MarshalBody(map[string]any{
		"interactionID": strings.TrimSpace(interaction.InteractionID),
		"kind":          strings.TrimSpace(interaction.Kind),
		"messageID":     strings.TrimSpace(event.MessageID),
		"choices":       []string{},
		"route":         string(agentcontract.TurnRouteContinueTask),
		"reason":        askReplyReason,
	}))
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
		Message:     message,
		TaskRunID:   taskRunID,
		ReplyKind:   connectorReplyKindCheckpoint,
		Attachments: checkpoint.Attachments,
	}
	if message == "" && len(checkpoint.Attachments) == 0 {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplySuppressed, connectorReplyEventBody(event, reply, "", "", "missing_checkpoint_message"))
		return errors.New("missing checkpoint message")
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, reply)
	if errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplyFailed, connectorReplyEventBody(event, reply, "", "", errorValue.Error()))
		connectorRuntime.logger.Error("connector."+platform+".checkpoint.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return errorValue
	}
	connectorRuntime.logger.Info("connector."+platform+".checkpoint.sent", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("replyDispatchID", dispatchID))
	return nil
}

func (connectorRuntime *ConnectorRuntime) sendUserNoticeReply(ctx context.Context, platform string, event PlatformInboundEvent, taskRunID string, replyTarget ReplyTarget, turnResult agentcontract.AgentTurnResult, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (string, bool) {
	reply, missingReason := UserNoticeReply(turnResult, taskRunID)
	if missingReason != "" {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplySuppressed, connectorReplyEventBody(event, OutboundReply{TaskRunID: taskRunID, ReplyKind: connectorReplyKindUserNotice}, "", "", missingReason))
		connectorRuntime.logger.Info("connector."+platform+".outbound.skipped", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("reason", missingReason))
		return "", false
	}
	if taskStatusRequiresFailureNotice(turnResult.TaskRun.Status) {
		reply.Message += failureRunFooter(taskRunID, connectorRuntime.adminTaskLinkBaseURL)
	}
	reply.RecoveryActions = recoveryActionsForEvent(turnResult.RecoveryActions, event)
	interaction, _ := latestAskInteraction(taskRunID, connectorRuntime.taskRunService.ListTaskEvent(taskRunID))
	reply.Interaction = optionalAskInteraction(interaction, event.SenderID)
	dispatchID, errorValue := sendReply(ctx, replyTarget, reply)
	if errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, agentcontract.TaskEventConnectorReplyFailed, connectorReplyEventBody(event, reply, "", "", errorValue.Error()))
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return "", false
	}
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

func UserNoticeReply(turnResult agentcontract.AgentTurnResult, taskRunID string) (OutboundReply, string) {
	message, failureNotice, missingReason := userNoticeReplyMessage(turnResult)
	if missingReason != "" {
		return OutboundReply{}, missingReason
	}
	return OutboundReply{
		Message:       message,
		TaskRunID:     taskRunID,
		ReplyKind:     connectorReplyKindUserNotice,
		Attachments:   turnResult.Attachments,
		FailureNotice: failureNotice,
	}, ""
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

func replyTargetOf(event PlatformInboundEvent) ReplyTarget {
	return ReplyTarget{
		ConversationID:     event.ConversationID,
		ReplyTargetID:      event.ReplyTargetID,
		AnsweringMessageID: event.MessageID,
		DedupeKey:          event.DedupeKey(),
	}
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
	return FindTaskRunBySourceReference(connectorRuntime.taskRunService, personID, sourceReference)
}

func FindTaskRunBySourceReference(taskRunStore taskstate.TaskRunStore, personID string, sourceReference string) (task.TaskRun, bool) {
	trimmedSourceReference := strings.TrimSpace(sourceReference)
	if trimmedSourceReference == "" {
		return task.TaskRun{}, false
	}
	var selectedTaskRun task.TaskRun
	isFound := false
	for _, taskRun := range taskRunStore.ListTaskRunByPersonID(personID) {
		if !taskRunHasSourceReference(taskRunStore, taskRun.TaskRunID, trimmedSourceReference) {
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

func taskRunHasSourceReference(taskRunStore taskstate.TaskRunStore, taskRunID string, sourceReference string) bool {
	for _, taskEvent := range taskRunStore.ListTaskEvent(taskRunID) {
		if !isSourceReferenceRecord(taskEvent.Name) {
			continue
		}
		if taskEventSourceReference(taskEvent) == sourceReference {
			return true
		}
	}
	return false
}

func isSourceReferenceRecord(eventName string) bool {
	switch eventName {
	case agentcontract.TaskEventAgentTaskSource, agentcontract.TaskEventAgentTaskLaunched, task.TaskEventLaunchOrigin:
		return true
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
