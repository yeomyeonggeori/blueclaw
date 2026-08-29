package connectors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
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
}

type ConnectorOutboxRepository interface {
	EnqueueConnectorReply(PlatformInboundEvent, ReplyTarget, OutboundReply) (string, error)
	ClaimPendingConnectorReplies(int, time.Duration) ([]QueuedConnectorReply, error)
	MarkConnectorReplySent(QueuedConnectorReply, string) error
	MarkConnectorReplyFailed(QueuedConnectorReply, error, time.Time) error
}

type PlatformInboundEvent struct {
	Platform         string                    `json:"-"`
	Source           string                    `json:"-"`
	ConversationID   string                    `json:"conversationID"`
	MessageID        string                    `json:"messageID"`
	SenderID         string                    `json:"senderID"`
	ReplyTargetID    string                    `json:"replyTargetID"`
	Prompt           string                    `json:"prompt"`
	InputParts       []agentcontract.AgentPart `json:"inputParts,omitempty"`
	ResponseLanguage string                    `json:"responseLanguage,omitempty"`
	Context          VisibleContext            `json:"context"`
	RawReceivedAt    time.Time                 `json:"-"`
	LegacyFields     map[string]interface{}    `json:"legacyFields,omitempty"`
}

type ReplyTarget struct {
	ConversationID string `json:"conversationID"`
	ReplyTargetID  string `json:"replyTargetID"`
	// AnsweringMessageID is the message this reply answers. The thread says where
	// the reply belongs; this says what it is a reply to, so somebody who wrote
	// deep in a thread does not find the answer at the top of it.
	AnsweringMessageID string `json:"answeringMessageID,omitempty"`
	DedupeKey          string `json:"dedupeKey"`
}

type ReactionTarget struct {
	Platform       string `json:"platform"`
	ConversationID string `json:"conversationID"`
	MessageID      string `json:"messageID"`
	EmojiName      string `json:"emojiName"`
	Reason         string `json:"reason"`
}

type OutboundReply struct {
	Message         string                        `json:"message"`
	TaskRunID       string                        `json:"taskRunID,omitempty"`
	ReplyKind       string                        `json:"replyKind,omitempty"`
	RawEventID      string                        `json:"rawEventID,omitempty"`
	OutboxID        string                        `json:"outboxID,omitempty"`
	Attachments     []toolcontract.FileAttachment `json:"attachments,omitempty"`
	RecoveryActions []toolcontract.RecoveryAction `json:"recoveryActions,omitempty"`
	FailureNotice   agentcontract.FailureNotice   `json:"failureNotice,omitempty"`
	Interaction     *AskInteraction               `json:"interaction,omitempty"`
}

type outboundReplyDocument struct {
	Message         string                        `json:"message"`
	TaskRunID       string                        `json:"taskRunID,omitempty"`
	ReplyKind       string                        `json:"replyKind,omitempty"`
	RawEventID      string                        `json:"rawEventID,omitempty"`
	OutboxID        string                        `json:"outboxID,omitempty"`
	Attachments     []outboundReplyAttachment     `json:"attachments,omitempty"`
	RecoveryActions []toolcontract.RecoveryAction `json:"recoveryActions,omitempty"`
	FailureNotice   agentcontract.FailureNotice   `json:"failureNotice,omitempty"`
	Interaction     *AskInteraction               `json:"interaction,omitempty"`
}

type outboundReplyAttachment struct {
	DevicePath    string `json:"devicePath"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	Title         string `json:"title,omitempty"`
	ContentBase64 string `json:"contentBase64,omitempty"`
}

type AskInteraction struct {
	InteractionID        string            `json:"interactionID"`
	TaskRunID            string            `json:"taskRunID"`
	Kind                 string            `json:"kind"`
	Message              string            `json:"message,omitempty"`
	Question             string            `json:"question,omitempty"`
	Options              []AskChoiceOption `json:"options,omitempty"`
	RecommendedOptionKey string            `json:"recommendedOptionKey,omitempty"`
	SelectionMode        string            `json:"selectionMode,omitempty"`
	ResponseLanguage     string            `json:"responseLanguage,omitempty"`
	TargetPlatformUserID string            `json:"targetPlatformUserID,omitempty"`
}

type AskChoiceOption struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	ShortLabel string `json:"shortLabel,omitempty"`
	Value      string `json:"value,omitempty"`
}

func (reply OutboundReply) MarshalJSON() ([]byte, error) {
	document := outboundReplyDocument{
		Message:         reply.Message,
		TaskRunID:       reply.TaskRunID,
		ReplyKind:       reply.ReplyKind,
		RawEventID:      reply.RawEventID,
		OutboxID:        reply.OutboxID,
		Attachments:     outboundReplyAttachments(reply.Attachments),
		RecoveryActions: reply.RecoveryActions,
		FailureNotice:   reply.FailureNotice,
		Interaction:     reply.Interaction,
	}
	return json.Marshal(document)
}

func (reply *OutboundReply) UnmarshalJSON(documentBytes []byte) error {
	var document outboundReplyDocument
	if errorValue := json.Unmarshal(documentBytes, &document); errorValue != nil {
		return errorValue
	}
	reply.Message = document.Message
	reply.TaskRunID = document.TaskRunID
	reply.ReplyKind = document.ReplyKind
	reply.RawEventID = document.RawEventID
	reply.OutboxID = document.OutboxID
	reply.Attachments = fileAttachmentsFromOutboundReplyAttachments(document.Attachments)
	reply.RecoveryActions = append([]toolcontract.RecoveryAction{}, document.RecoveryActions...)
	reply.FailureNotice = document.FailureNotice
	reply.Interaction = document.Interaction
	return nil
}

func outboundReplyAttachments(attachments []toolcontract.FileAttachment) []outboundReplyAttachment {
	replyAttachments := []outboundReplyAttachment{}
	for _, attachment := range attachments {
		replyAttachments = append(replyAttachments, outboundReplyAttachment{
			DevicePath:    attachment.DevicePath,
			Filename:      attachment.Filename,
			ContentType:   attachment.ContentType,
			SizeBytes:     attachment.SizeBytes,
			Title:         attachment.Title,
			ContentBase64: attachment.ContentBase64,
		})
	}
	return replyAttachments
}

func fileAttachmentsFromOutboundReplyAttachments(attachments []outboundReplyAttachment) []toolcontract.FileAttachment {
	fileAttachments := []toolcontract.FileAttachment{}
	for _, attachment := range attachments {
		fileAttachments = append(fileAttachments, toolcontract.FileAttachment{
			DevicePath:    attachment.DevicePath,
			Filename:      attachment.Filename,
			ContentType:   attachment.ContentType,
			SizeBytes:     attachment.SizeBytes,
			Title:         attachment.Title,
			ContentBase64: attachment.ContentBase64,
		})
	}
	return fileAttachments
}

type QueuedConnectorEvent struct {
	Event        PlatformInboundEvent
	AttemptCount int
}

type QueuedConnectorReply struct {
	OutboxID     string
	RawEventID   string
	Platform     string
	ReplyTarget  ReplyTarget
	Reply        OutboundReply
	AttemptCount int
}

type VisibleContext struct {
	Messages         []VisibleContextMessage `json:"messages"`
	HasMoreBefore    bool                    `json:"hasMoreBefore"`
	HistoryCursor    string                  `json:"historyCursor"`
	ResponseLanguage string                  `json:"responseLanguage,omitempty"`
	Sender           VisibleContextSender    `json:"sender,omitempty"`
	ConversationType string                  `json:"conversationType,omitempty"`
	ChannelID        string                  `json:"channelID,omitempty"`
	ChannelName      string                  `json:"channelName,omitempty"`
	Addressing       AddressingMetadata      `json:"addressing,omitempty"`
	AttachmentsOnly  bool                    `json:"attachmentsOnly,omitempty"`
	// MessagesOpenOtherExchanges says the messages are how other conversations in
	// the same place opened, rather than the conversation being continued.
	MessagesOpenOtherExchanges bool              `json:"messagesOpenOtherExchanges,omitempty"`
	InputAttachments           []InputAttachment `json:"inputAttachments,omitempty"`
	Materials                  []InputAttachment `json:"materials,omitempty"`
}

type InputAttachment struct {
	Platform    string `json:"platform,omitempty"`
	FileID      string `json:"fileID,omitempty"`
	URL         string `json:"url,omitempty"`
	MessageID   string `json:"messageID,omitempty"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	Path        string `json:"path,omitempty"`
	// ContentBase64 carries the fetched file from the bridge that could reach the
	// platform to the workspace it belongs in. It lives only for that hop: the
	// file is written here and the field is cleared before anything records it.
	ContentBase64 string `json:"contentBase64,omitempty"`
	IsAvailable   bool   `json:"isAvailable,omitempty"`
	ErrorCode     string `json:"errorCode,omitempty"`
	Message       string `json:"message,omitempty"`
}

type AddressingMetadata struct {
	BotMentioned         bool `json:"botMentioned,omitempty"`
	OtherPersonMentioned bool `json:"otherPersonMentioned,omitempty"`
}

type VisibleContextSender struct {
	Platform    string `json:"platform,omitempty"`
	SenderID    string `json:"senderID,omitempty"`
	Handle      string `json:"handle,omitempty"`
	Email       string `json:"email,omitempty"`
	Name        string `json:"name,omitempty"`
	CallingName string `json:"callingName,omitempty"`
}

type VisibleContextMessage struct {
	Speaker            string            `json:"speaker"`
	SpeakerCallingName string            `json:"speakerCallingName,omitempty"`
	SpeakerHandle      string            `json:"speakerHandle,omitempty"`
	Text               string            `json:"text"`
	SentAt             time.Time         `json:"sentAt,omitempty"`
	InputAttachments   []InputAttachment `json:"inputAttachments,omitempty"`
}

type HTTPParseResult struct {
	Event             PlatformInboundEvent
	HasEvent          bool
	ImmediateResponse *HTTPResponse
}

type HTTPResponse struct {
	StatusCode  int
	ContentType string
	Body        []byte
}

type ConnectorRuntimeResult struct {
	Handled         bool   `json:"handled"`
	Platform        string `json:"platform"`
	Duplicate       bool   `json:"duplicate"`
	Ignored         bool   `json:"ignored"`
	Reason          string `json:"reason,omitempty"`
	TaskRunID       string `json:"taskRunID,omitempty"`
	ReplyDispatchID string `json:"replyDispatchID,omitempty"`
}

// This runtime matches an inbound account against the people this device carries. Whether
// someone was invited is decided elsewhere — the account directory the device is projected
// from — so a refusal here states the match that failed and stops there. Saying "not invited"
// asserts a fact this process cannot read, and it was wrong for the ordinary case: a person
// who is invited, whose messenger account presents an address their record does not carry.
const UnmatchedAccountReply = "This Intern Kim could not match your account to anyone it knows about. Ask the administrator to check it."

// A lookup that never answered established nothing about the sender, so the reply says
// that instead of that nobody knows them. Sending somebody to an administrator over a
// lookup that failed wastes both their time on a record that is already correct.
const DirectoryUnreachableReply = "This Intern Kim could not reach the directory just now, so it cannot tell whose account this is. As far as it knows there is nothing wrong with your account. Try again in a moment, and tell the administrator if it keeps happening."

// The sender already knows their own address, so naming it discloses nothing to them and
// turns an unanswerable message into one an administrator can act on. Which people this
// device carries stays unsaid, because whoever is asking may be from outside the company.
func unmatchedAccountReplyFor(authorization senderAuthorization) string {
	if authorization.DirectoryUnreachable {
		return DirectoryUnreachableReply
	}
	platformAccountEmail := strings.TrimSpace(authorization.PlatformAccountEmail)
	if platformAccountEmail == "" {
		return UnmatchedAccountReply + " Your account presents no email address, and that is what this Intern Kim matches on."
	}
	return fmt.Sprintf("%s Your account presents %s, and no one here is on file under that address — either it is recorded under a different one, or this %s account has not reached this Intern Kim yet.",
		UnmatchedAccountReply, platformAccountEmail, strings.TrimSpace(authorization.Platform))
}

// senderAuthorization is what the runtime actually established about an inbound sender, so a
// refusal can state that and nothing further.
type senderAuthorization struct {
	PersonID             string
	IsAllowed            bool
	Platform             string
	PlatformAccountEmail string
	// DirectoryUnreachable separates a directory that said no from one that never
	// answered. Told they are not on file, somebody goes to an administrator who
	// finds their record exactly where it belongs, and nothing anywhere says the
	// lookup is what failed.
	DirectoryUnreachable bool
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

// A platform that can change a message it already sent lets one message carry a
// turn from start to finish: what the agent is doing while it works, and its
// answer when it is done.
type ReplyEditingAdapter interface {
	EditReply(context.Context, ReplyTarget, string, string) error
}

// A platform that can take back a message it already sent lets the narration be
// scaffolding: it comes down once the answer stands as a message of its own.
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

// What a turn is doing while it does it. It is written for somebody watching,
// and it is not something anyone said, so it is not read back as context.
const ConnectorReplyKindProgress = "progress"
const connectorReplyKindUserNotice = "user_notice"
const connectorReplyKindPermissionNotice = "permission_notice"

type ConnectorRuntime struct {
	identityService        *identity.IdentityService
	unknownAccountResolver UnknownAccountResolver
	harness                agentcontract.Harness
	intakeClassifier       IntakeClassifier
	turnRouter             TurnRouter
	replyGenerator         ReplyGenerator
	launchFailureCompleter LaunchFailureCompleter
	taskRunService         *taskstate.TaskRunService
	taskEventService       *taskstate.TaskEventService
	taskLauncher           *agentruntime.TaskLauncher
	approvalGate           *approvalgate.Gate
	toolCatalogBuilder     *agentruntime.ToolCatalogBuilder
	workspaceActorFactory  security.WorkspaceActorFactory
	memoryService          *memory.MemoryService
	agentIdentityProvider  func() agentcontract.AgentIdentity
	workspaceID            string
	adminTaskLinkBaseURL   string
	logger                 *slog.Logger

	mutex                   sync.Mutex
	adapterByPlatform       map[string]PlatformAdapter
	processedResults        map[string]ConnectorRuntimeResult
	eventRepository         ConnectorEventRepository
	ingressGate             IngressGate
	taskIntakeGate          TaskIntakeGate
	taskWaitTokenRepository task.TaskWaitTokenRepository
	conversationLocks       map[string]*sync.Mutex
	sentAttachmentSources   *sentAttachmentSourceStore
	started                 bool
	inboxHeartbeats         []time.Time
	outboxHeartbeats        []time.Time
}

type ConnectorRuntimeHealth struct {
	Started                     bool      `json:"started"`
	HasEventRepository          bool      `json:"hasEventRepository"`
	HasQueueRepository          bool      `json:"hasQueueRepository"`
	HasOutboxRepository         bool      `json:"hasOutboxRepository"`
	RegisteredPlatforms         []string  `json:"registeredPlatforms"`
	MattermostAdapterRegistered bool      `json:"mattermostAdapterRegistered"`
	InboxWorkerCount            int       `json:"inboxWorkerCount"`
	OutboxWorkerCount           int       `json:"outboxWorkerCount"`
	InboxWorkersAlive           bool      `json:"inboxWorkersAlive"`
	OutboxWorkersAlive          bool      `json:"outboxWorkersAlive"`
	LastInboxHeartbeatAt        time.Time `json:"lastInboxHeartbeatAt,omitempty"`
	LastOutboxHeartbeatAt       time.Time `json:"lastOutboxHeartbeatAt,omitempty"`
	Passed                      bool      `json:"passed"`
}

type pendingApproval struct {
	TaskRun                 task.TaskRun
	IntentPrompt            string
	ApprovalQuestion        string
	ResponseLanguage        string
	ContinuationInstruction string
	ActiveGoal              agentcontract.ActiveGoal
}

type inboundTaskWaitResolution struct {
	TaskWaitToken      task.TaskWaitToken
	HasTaskWaitToken   bool
	IsAmbiguous        bool
	AmbiguousTaskWaits []task.TaskWaitToken
	Reason             string
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
		sentAttachmentSources: newSentAttachmentSourceStore(),
	}
}

func (connectorRuntime *ConnectorRuntime) RegisterAdapter(adapter PlatformAdapter) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	connectorRuntime.adapterByPlatform[adapter.Name()] = adapter
}

func (connectorRuntime *ConnectorRuntime) UseUnknownAccountResolver(unknownAccountResolver UnknownAccountResolver) {
	connectorRuntime.unknownAccountResolver = unknownAccountResolver
}

func (connectorRuntime *ConnectorRuntime) UseMemoryService(memoryService *memory.MemoryService) {
	connectorRuntime.memoryService = memoryService
	connectorRuntime.toolCatalogBuilder.UseMemoryService(memoryService)
}

func (connectorRuntime *ConnectorRuntime) UseAdminTaskLinkBaseURL(adminTaskLinkBaseURL string) {
	connectorRuntime.adminTaskLinkBaseURL = strings.TrimRight(strings.TrimSpace(adminTaskLinkBaseURL), "/")
}

func (connectorRuntime *ConnectorRuntime) UseWorkspaceID(workspaceID string) {
	connectorRuntime.workspaceID = strings.TrimSpace(workspaceID)
}

func (connectorRuntime *ConnectorRuntime) UseWorkspaceRootPath(workspaceRootPath string) {
	connectorRuntime.toolCatalogBuilder.UseWorkspaceRootPath(workspaceRootPath)
}

func (connectorRuntime *ConnectorRuntime) UseTerminalService(terminalService *security.ShellService) {
	connectorRuntime.toolCatalogBuilder.UseTerminalService(terminalService)
}

func (connectorRuntime *ConnectorRuntime) UseWorkspaceActorFactory(workspaceActorFactory security.WorkspaceActorFactory) {
	connectorRuntime.workspaceActorFactory = workspaceActorFactory
	connectorRuntime.toolCatalogBuilder.UseWorkspaceActorFactory(workspaceActorFactory)
}

func (connectorRuntime *ConnectorRuntime) UseTaskScheduleRepository(taskScheduleRepository task.TaskScheduleRepository) {
	connectorRuntime.toolCatalogBuilder.UseTaskScheduleRepository(taskScheduleRepository)
}

func (connectorRuntime *ConnectorRuntime) UseTaskWaitTokenRepository(taskWaitTokenRepository task.TaskWaitTokenRepository) {
	connectorRuntime.taskWaitTokenRepository = taskWaitTokenRepository
	connectorRuntime.toolCatalogBuilder.UseTaskWaitTokenRepository(taskWaitTokenRepository)
}

func (connectorRuntime *ConnectorRuntime) UseApprovalGate(approvalGate *approvalgate.Gate) {
	connectorRuntime.approvalGate = approvalGate
}

func (connectorRuntime *ConnectorRuntime) UseTaskRunService(taskRunService *task.TaskRunService) {
	connectorRuntime.toolCatalogBuilder.UseTaskRunService(taskRunService)
}

func (connectorRuntime *ConnectorRuntime) UseEventRepository(eventRepository ConnectorEventRepository) {
	connectorRuntime.eventRepository = eventRepository
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

func (connectorRuntime *ConnectorRuntime) UseIngressGate(ingressGate IngressGate) {
	connectorRuntime.ingressGate = ingressGate
}

func (connectorRuntime *ConnectorRuntime) UseTaskIntakeGate(taskIntakeGate TaskIntakeGate) {
	connectorRuntime.taskIntakeGate = taskIntakeGate
}

func (connectorRuntime *ConnectorRuntime) UseMCPRegistry(mcpRegistry *mcp.McpRegistry) {
	connectorRuntime.toolCatalogBuilder.UseMCPRegistry(mcpRegistry)
}

func (connectorRuntime *ConnectorRuntime) UseCapabilityToolDescriptors(capabilityClient capability.Client, toolDescriptors []agentruntime.CapabilityToolDescriptor) {
	connectorRuntime.toolCatalogBuilder.UseCapabilityToolDescriptors(capabilityClient, toolDescriptors)
}

func (connectorRuntime *ConnectorRuntime) UseAllowedToolNames(allowedToolNames []string) {
	trimmedToolNames := trimNonEmptyStrings(allowedToolNames)
	if len(trimmedToolNames) == 0 {
		trimmedToolNames = connectorRuntimeDefaultAllowedToolNames()
	}
	connectorRuntime.toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, trimmedToolNames)
}

func connectorRuntimeDefaultAllowedToolNames() []string {
	return append([]string{"conversation_history"}, agentruntime.DefaultAllowedToolNames()...)
}

func (connectorRuntime *ConnectorRuntime) UseAllowedToolNamesByProfile(allowedToolNamesByProfile map[string][]string, defaultAllowedToolNames []string) {
	connectorRuntime.toolCatalogBuilder.UseAllowedToolNamesByProfile(allowedToolNamesByProfile, defaultAllowedToolNames)
}

type LaunchFailureCompleter interface {
	CompleteLaunchFailure(context.Context, agentcontract.AgentTurnRequest, string, string, error) agentcontract.AgentTurnResult
}

func (connectorRuntime *ConnectorRuntime) UseLaunchFailureCompleter(launchFailureCompleter LaunchFailureCompleter) {
	connectorRuntime.launchFailureCompleter = launchFailureCompleter
}

type ReplyGenerator interface {
	GenerateReply(context.Context, string) (string, error)
	GenerateReplyWithContext(context.Context, string, agentcontract.VisibleContext, []agentcontract.MemoryFact) (string, error)
}

func (connectorRuntime *ConnectorRuntime) UseReplyGenerator(replyGenerator ReplyGenerator) {
	connectorRuntime.replyGenerator = replyGenerator
}

type TurnRouter interface {
	Plan(context.Context, agentcontract.AgentRequest) (agentcontract.TurnDecision, error)
	PlanObserved(context.Context, agentcontract.AgentRequest, *agentcontract.TurnRouterCallLedger) (agentcontract.TurnDecision, error)
}

func (connectorRuntime *ConnectorRuntime) planTurn(ctx context.Context, taskRunID string, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	if connectorRuntime.turnRouter == nil {
		return agentcontract.TurnDecision{}, errors.New("connector runtime has no turn router configured")
	}
	callLedger := &agentcontract.TurnRouterCallLedger{}
	turnDecision, errorValue := connectorRuntime.turnRouter.PlanObserved(ctx, request, callLedger)
	if trimmedTaskRunID := strings.TrimSpace(taskRunID); trimmedTaskRunID != "" && connectorRuntime.taskRunService != nil {
		for _, callRecord := range callLedger.Records {
			connectorRuntime.taskRunService.AppendTaskEvent(trimmedTaskRunID, "llm.call", marshalConnectorEventBody(callRecord))
		}
	}
	return turnDecision, errorValue
}

func (connectorRuntime *ConnectorRuntime) UseTurnRouter(turnRouter TurnRouter) {
	connectorRuntime.turnRouter = turnRouter
}

type IntakeClassifier interface {
	ClassifyAddressing(context.Context, agentcontract.AddressingClassificationRequest) (agentcontract.AddressingDecision, error)
	ClassifyActiveTaskFollowUp(context.Context, agentcontract.ActiveTaskFollowUpClassificationRequest) (bool, error)
}

func (connectorRuntime *ConnectorRuntime) UseIntakeClassifier(intakeClassifier IntakeClassifier) {
	connectorRuntime.intakeClassifier = intakeClassifier
}

func (connectorRuntime *ConnectorRuntime) UseTaskLauncher(taskLauncher *agentruntime.TaskLauncher) {
	connectorRuntime.taskLauncher = taskLauncher
}

func (connectorRuntime *ConnectorRuntime) UseAgentIdentityProvider(agentIdentityProvider func() agentcontract.AgentIdentity) {
	connectorRuntime.agentIdentityProvider = agentIdentityProvider
}

func (connectorRuntime *ConnectorRuntime) agentIdentity() agentcontract.AgentIdentity {
	if connectorRuntime.agentIdentityProvider == nil {
		return agentcontract.AgentIdentity{}
	}
	return connectorRuntime.agentIdentityProvider()
}

func (connectorRuntime *ConnectorRuntime) Start(ctx context.Context) {
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
		result, errorValue = connectorRuntime.processInboundEvent(ctx, adapter, event)
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

	result, errorValue := connectorRuntime.processInboundEvent(ctx, adapter, event)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}

	connectorRuntime.rememberProcessedResult(eventKey, result)
	return result, nil
}

func (connectorRuntime *ConnectorRuntime) enqueueInboundEvent(event PlatformInboundEvent, queueRepository ConnectorQueueRepository) (ConnectorRuntimeResult, error) {
	isDuplicate, result, errorValue := queueRepository.TryEnqueueConnectorEvent(event)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	if isDuplicate {
		result.Handled = true
		result.Platform = event.Platform
		result.Duplicate = true
		connectorRuntime.logger.Info("connector."+event.Platform+".event.suppressed", slog.String("source", event.Source), slog.String("reason", "duplicate"), slog.String("messageID", event.MessageID))
		return result, nil
	}
	return ConnectorRuntimeResult{Handled: true, Platform: event.Platform, Reason: "queued"}, nil
}

func (connectorRuntime *ConnectorRuntime) runConnectorInboxWorker(ctx context.Context, workerIndex int) {
	workerContext, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go connectorRuntime.recordConnectorWorkerHeartbeatUntilStopped(workerContext, "inbox", workerIndex)
	for ctx.Err() == nil {
		connectorRuntime.recordConnectorWorkerHeartbeat("inbox", workerIndex)
		if connectorRuntime.processNextQueuedConnectorEvent(ctx) {
			continue
		}
		sleepConnectorWorker(ctx)
	}
}

func (connectorRuntime *ConnectorRuntime) processNextQueuedConnectorEvent(ctx context.Context) bool {
	queueRepository := connectorRuntime.queueRepository()
	if queueRepository == nil {
		return false
	}
	queuedEvents, errorValue := queueRepository.ClaimPendingConnectorEvents(1, connectorClaimLeaseDuration)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.inbox.claim_failed", slog.String("error", errorValue.Error()))
		return false
	}
	if len(queuedEvents) == 0 {
		return false
	}
	connectorRuntime.processQueuedConnectorEvent(ctx, queuedEvents[0])
	return true
}

func (connectorRuntime *ConnectorRuntime) processQueuedConnectorEvent(ctx context.Context, queuedEvent QueuedConnectorEvent) {
	event := queuedEvent.Event
	adapter, errorValue := connectorRuntime.findAdapter(event.Platform)
	if errorValue != nil {
		connectorRuntime.markQueuedConnectorEventFailed(queuedEvent, errorValue)
		return
	}
	connectorRuntime.logConnectorQueueWait(event)
	lock := connectorRuntime.conversationLock(event.Platform + ":" + event.ConversationID)
	if connectorRuntime.shouldProcessBeforeConversationLock(ctx, adapter, event) {
		connectorRuntime.processQueuedConnectorEventWithAdapter(ctx, adapter, queuedEvent)
		return
	}
	lockStartedAt := time.Now()
	lock.Lock()
	connectorRuntime.logConnectorLockWait(event, time.Since(lockStartedAt))
	defer lock.Unlock()
	connectorRuntime.processQueuedConnectorEventWithAdapter(ctx, adapter, queuedEvent)
}

func (connectorRuntime *ConnectorRuntime) logConnectorQueueWait(event PlatformInboundEvent) {
	if event.RawReceivedAt.IsZero() {
		return
	}
	waitDuration := time.Since(event.RawReceivedAt)
	if waitDuration < 0 {
		return
	}
	connectorRuntime.logger.Info(
		"blueclaw.connector.queue_wait",
		slog.String("platform", event.Platform),
		slog.String("messageID", event.MessageID),
		slog.String("conversationID", event.ConversationID),
		slog.Int64("duration_ms", waitDuration.Milliseconds()),
	)
}

func (connectorRuntime *ConnectorRuntime) logConnectorLockWait(event PlatformInboundEvent, waitDuration time.Duration) {
	connectorRuntime.logger.Info(
		"blueclaw.connector.lock_wait",
		slog.String("platform", event.Platform),
		slog.String("messageID", event.MessageID),
		slog.String("conversationID", event.ConversationID),
		slog.Int64("duration_ms", waitDuration.Milliseconds()),
	)
}

func (connectorRuntime *ConnectorRuntime) processQueuedConnectorEventWithAdapter(ctx context.Context, adapter PlatformAdapter, queuedEvent QueuedConnectorEvent) {
	event := queuedEvent.Event
	if ctx.Err() != nil {
		return
	}
	result, errorValue := connectorRuntime.processInboundEventWithReplySender(ctx, adapter, event, connectorRuntime.enqueueConnectorReply)
	if ctx.Err() != nil {
		return
	}
	if errorValue != nil {
		connectorRuntime.markQueuedConnectorEventFailed(queuedEvent, errorValue)
		return
	}
	if shouldDeferQueuedConnectorEvent(result) {
		connectorRuntime.logger.Info("connector."+event.Platform+".inbox.deferred", slog.String("messageID", event.MessageID), slog.String("reason", result.Reason))
		return
	}
	if errorValue := connectorRuntime.queueRepository().MarkConnectorEventSucceeded(event, result); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+event.Platform+".inbox.mark_succeeded_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) enqueueConnectorReply(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
	event, isFound := connectorEventFromContext(ctx)
	if !isFound {
		return "", errors.New("connector event context is missing")
	}
	outboxRepository := connectorRuntime.outboxRepository()
	if outboxRepository == nil {
		if connectorRuntime.eventRepository != nil {
			return "", errors.New("connector outbox repository is required when connector event repository is configured")
		}
		adapter, errorValue := connectorRuntime.findAdapter(event.Platform)
		if errorValue != nil {
			return "", errorValue
		}
		dispatchID, errorValue := adapter.SendReply(ctx, replyTarget, reply)
		if errorValue == nil {
			connectorRuntime.sentAttachmentSources.RecordReply(event.Platform, dispatchID, reply.Attachments)
		}
		return dispatchID, errorValue
	}
	outboxID, errorValue := outboxRepository.EnqueueConnectorReply(event, replyTarget, reply)
	if errorValue == nil {
		connectorRuntime.appendConnectorReplyEvent(reply.TaskRunID, "connector.reply.enqueued", connectorReplyEventBody(event, reply, outboxID, "", ""))
	}
	return outboxID, errorValue
}

func (connectorRuntime *ConnectorRuntime) runConnectorOutboxWorker(ctx context.Context, workerIndex int) {
	workerContext, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go connectorRuntime.recordConnectorWorkerHeartbeatUntilStopped(workerContext, "outbox", workerIndex)
	for ctx.Err() == nil {
		connectorRuntime.recordConnectorWorkerHeartbeat("outbox", workerIndex)
		if connectorRuntime.processNextQueuedConnectorReply(ctx) {
			continue
		}
		sleepConnectorWorker(ctx)
	}
}

func (connectorRuntime *ConnectorRuntime) processNextQueuedConnectorReply(ctx context.Context) bool {
	outboxRepository := connectorRuntime.outboxRepository()
	if outboxRepository == nil {
		return false
	}
	queuedReplies, errorValue := outboxRepository.ClaimPendingConnectorReplies(1, connectorClaimLeaseDuration)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.outbox.claim_failed", slog.String("error", errorValue.Error()))
		return false
	}
	if len(queuedReplies) == 0 {
		return false
	}
	connectorRuntime.processQueuedConnectorReply(ctx, queuedReplies[0])
	return true
}

func (connectorRuntime *ConnectorRuntime) processQueuedConnectorReply(ctx context.Context, queuedReply QueuedConnectorReply) {
	adapter, errorValue := connectorRuntime.findAdapter(queuedReply.Platform)
	if errorValue != nil {
		connectorRuntime.markQueuedConnectorReplyFailed(queuedReply, errorValue)
		return
	}
	queuedReply.Reply.RawEventID = firstNonEmptyString(queuedReply.Reply.RawEventID, queuedReply.RawEventID)
	queuedReply.Reply.OutboxID = firstNonEmptyString(queuedReply.Reply.OutboxID, queuedReply.OutboxID)
	dispatchID, errorValue := adapter.SendReply(ctx, queuedReply.ReplyTarget, queuedReply.Reply)
	if ctx.Err() != nil {
		return
	}
	if errorValue != nil {
		connectorRuntime.markQueuedConnectorReplyFailed(queuedReply, errorValue)
		return
	}
	connectorRuntime.sentAttachmentSources.RecordReply(queuedReply.Platform, dispatchID, queuedReply.Reply.Attachments)
	if errorValue := connectorRuntime.outboxRepository().MarkConnectorReplySent(queuedReply, dispatchID); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+queuedReply.Platform+".outbox.mark_sent_failed", slog.String("outboxID", queuedReply.OutboxID), slog.String("error", errorValue.Error()))
	}
	connectorRuntime.appendConnectorReplyEvent(queuedReply.Reply.TaskRunID, "connector.reply.sent", connectorReplyEventBody(PlatformInboundEvent{MessageID: queuedReply.RawEventID}, queuedReply.Reply, queuedReply.OutboxID, dispatchID, ""))
	connectorRuntime.recordTaskWaitTokenForReply(
		queuedReply.Platform,
		PlatformInboundEvent{Platform: queuedReply.Platform, ConversationID: queuedReply.ReplyTarget.ConversationID, MessageID: queuedReply.RawEventID},
		queuedReply.ReplyTarget,
		queuedReply.Reply,
		dispatchID,
	)
}

func (connectorRuntime *ConnectorRuntime) processInboundEvent(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) (ConnectorRuntimeResult, error) {
	return connectorRuntime.processInboundEventWithReplySender(ctx, adapter, event, adapter.SendReply)
}

func (connectorRuntime *ConnectorRuntime) processInboundEventWithReplySender(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (ConnectorRuntimeResult, error) {
	ctx = withConnectorEvent(ctx, event)
	platform := adapter.Name()
	connectorRuntime.logger.Info(
		"connector."+platform+".ingress.received",
		slog.String("source", event.Source),
		slog.String("messageID", event.MessageID),
		slog.String("conversationID", event.ConversationID),
		slog.String("senderID", event.SenderID),
		slog.String("replyTargetID", event.ReplyTargetID),
		slog.Bool("hasMoreBefore", event.Context.HasMoreBefore),
	)

	replyTarget, errorValue := connectorRuntime.buildReplyTarget(ctx, adapter, event)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	authorization, errorValue := connectorRuntime.authorizeSender(ctx, adapter, event)
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".auth.failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{}, errorValue
	}
	personID := authorization.PersonID
	if !authorization.IsAllowed {
		if shouldIgnoreUninvitedAddressing(event) {
			connectorRuntime.logger.Info("connector."+platform+".ingress.ignored", slog.String("messageID", event.MessageID), slog.String("reason", "not_addressed_to_bot"))
			return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "not_addressed_to_bot"}, nil
		}
		refusalReason := "unmatched_account"
		if authorization.DirectoryUnreachable {
			refusalReason = "directory_unreachable"
		}
		connectorRuntime.logger.Info("connector."+platform+".auth.rejected",
			slog.String("messageID", event.MessageID),
			slog.String("reason", refusalReason),
			slog.String("senderID", event.SenderID),
			slog.String("platformAccountEmail", authorization.PlatformAccountEmail))
		dispatchID, sendError := sendReply(ctx, replyTarget, OutboundReply{Message: unmatchedAccountReplyFor(authorization), ReplyKind: connectorReplyKindPermissionNotice})
		if sendError != nil {
			connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("error", sendError.Error()))
			return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: refusalReason}, nil
		}
		connectorRuntime.logger.Info("connector."+platform+".outbound.sent", slog.String("messageID", event.MessageID), slog.String("replyDispatchID", dispatchID))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: refusalReason, ReplyDispatchID: dispatchID}, nil
	}

	connectorRuntime.logger.Info("connector."+platform+".auth.allowed", slog.String("messageID", event.MessageID), slog.String("personID", personID))
	if result, isHandled := connectorRuntime.suppressDuplicateSourceTaskIfNeeded(platform, event, personID); isHandled {
		return result, nil
	}
	if result, isHandled := connectorRuntime.handleTaskControlIfRequested(ctx, platform, adapter, event, replyTarget, personID, sendReply); isHandled {
		return result, nil
	}
	stopProgress := func() {}
	isProgressStarted := false
	defer func() {
		if isProgressStarted {
			stopProgress()
		}
	}()
	if shouldStartProgressBeforeAddressing(event) {
		stopProgress = connectorRuntime.startProgressHeartbeat(ctx, adapter, replyTarget)
		isProgressStarted = true
	}
	personAccess := connectorRuntime.identityService.ResolvePersonAccess(personID)
	requesterEmail := connectorRuntime.requesterEmailForEvent(personID, event)
	taskWaitResolution := connectorRuntime.resolveInboundTaskWait(personID, platform, event)
	engagedAckEmojiName := connectorRuntime.applyEngagedAckReaction(ctx, platform, adapter, event,
		event.Context.Addressing.BotMentioned || taskWaitResolution.HasTaskWaitToken || taskWaitResolution.IsAmbiguous)
	if taskWaitResolution.IsAmbiguous {
		return connectorRuntime.handleAmbiguousTaskWait(ctx, platform, adapter, event, replyTarget, personID, requesterEmail, personAccess, taskWaitResolution, engagedAckEmojiName, sendReply)
	}
	pendingApproval, turnDecision, hasPendingConfirmation, errorValue := connectorRuntime.resolveConfirmationReply(ctx, platform, personID, event, taskWaitResolution)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	isApprovalContinuation := hasPendingConfirmation && turnDecision.Approval != nil && agentcontract.IsApprovingSignal(*turnDecision.Approval)
	if hasPendingConfirmation {
		connectorRuntime.resolveTaskWaitToken(taskWaitResolution)
	}
	if hasPendingConfirmation && !isApprovalContinuation && shouldStopAfterPendingConfirmation(turnDecision) {
		return connectorRuntime.handleRejectedConfirmation(ctx, platform, adapter, event, replyTarget, pendingApproval, agentcontract.ConfirmationReplyDecision{Decision: string(agentcontract.ApprovalSignalReject), Reason: turnDecision.Reason}, sendReply)
	}
	if hasPendingConfirmation && !isApprovalContinuation && turnDecision.Route == agentcontract.TurnRouteAnswerQuestion {
		return connectorRuntime.handlePendingConfirmationQuestion(ctx, platform, adapter, event, replyTarget, pendingApproval, turnDecision, sendReply)
	}
	didSupersedePendingConfirmation := false
	if hasPendingConfirmation && !isApprovalContinuation {
		connectorRuntime.cancelPendingConfirmation(event, pendingApproval, turnDecision)
		didSupersedePendingConfirmation = true
	}
	pendingAskInteraction, hasPendingAskInteraction := connectorRuntime.findPendingAskInteraction(personID, platform, event, taskWaitResolution)
	previousPrompt := event.Prompt
	event, askTurnDecision, hasAskTurnDecision, errorValue := connectorRuntime.resolveAskReply(ctx, platform, personID, event, taskWaitResolution)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	didSupersedePendingAsk := false
	if hasPendingAskInteraction && askReplySupersedesInteraction(askTurnDecision, hasAskTurnDecision) {
		connectorRuntime.supersedePendingAskInteraction(event, pendingAskInteraction, askTurnDecision)
		connectorRuntime.resolveTaskWaitToken(taskWaitResolution)
		pendingAskInteraction = AskInteraction{}
		hasPendingAskInteraction = false
		taskWaitResolution = inboundTaskWaitResolution{}
		didSupersedePendingAsk = true
	} else if hasPendingAskInteraction && askReplyConsumesInteraction(pendingAskInteraction, previousPrompt, event, askTurnDecision, hasAskTurnDecision) {
		connectorRuntime.appendAskResolvedEvent(pendingAskInteraction, event, askTurnDecision)
		connectorRuntime.resolveTaskWaitToken(taskWaitResolution)
	}
	activeGoal, hasActiveGoal := connectorRuntime.findActiveGoal(personID, platform, event, taskWaitResolution)
	if !isApprovalContinuation && hasActiveGoal && turnDecision.Route == agentcontract.TurnRouteStartTask {
		activeGoal = agentcontract.ActiveGoal{}
		hasActiveGoal = false
	}
	if isApprovalContinuation {
		event = approvedContinuationEvent(event, pendingApproval)
		activeGoal = pendingApprovalActiveGoal(pendingApproval, event.Prompt)
		hasActiveGoal = true
	}
	if engagedAckEmojiName == "" {
		engagedAckEmojiName = connectorRuntime.applyEngagedAckReaction(ctx, platform, adapter, event,
			isApprovalContinuation || hasPendingAskInteraction || hasActiveGoal)
	}
	event = connectorRuntime.withInitialVisibleContext(ctx, adapter, event)
	addressingLaunch := connectorRuntime.resolveInboundEngagement(ctx, platform, event)
	if addressingLaunch.ReactionEmoji != "" {
		if engagedAckEmojiName != "" && engagedAckEmojiName != addressingLaunch.ReactionEmoji {
			connectorRuntime.clearEngagedAckReaction(ctx, platform, adapter, event, engagedAckEmojiName)
			engagedAckEmojiName = ""
		}
		connectorRuntime.addAddressingReaction(ctx, platform, adapter, event, addressingLaunch.ReactionEmoji)
	}
	if !addressingLaunch.ShouldLaunch {
		reason := firstNonEmptyString(addressingLaunch.IgnoreReason, "addressing_react_only")
		connectorRuntime.logger.Info("connector."+platform+".ingress.ignored", slog.String("messageID", event.MessageID), slog.String("reason", reason))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: reason}, nil
	}
	if connectorRuntime.shouldDeferNewTaskLaunch(isApprovalContinuation, hasPendingAskInteraction, hasActiveGoal) {
		connectorRuntime.logger.Info("connector."+platform+".ingress.deferred", slog.String("messageID", event.MessageID), slog.String("reason", "task_intake_quiesced"))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "task_intake_quiesced"}, nil
	}
	if !isApprovalContinuation && !hasPendingAskInteraction && !didSupersedePendingAsk && !didSupersedePendingConfirmation {
		busyResult, errorValue := connectorRuntime.handleBusyMessageIfNeeded(ctx, platform, event, replyTarget, personID, sendReply)
		if errorValue != nil {
			return ConnectorRuntimeResult{}, errorValue
		}
		if busyResult.isHandled {
			return busyResult.connectorResult, nil
		}
		if busyResult.clearActiveGoal {
			activeGoal = agentcontract.ActiveGoal{}
			hasActiveGoal = false
		}
	}
	if !isProgressStarted {
		stopProgress = connectorRuntime.startProgressHeartbeat(ctx, adapter, replyTarget)
		isProgressStarted = true
	}
	event = connectorRuntime.withAttachmentMaterials(ctx, adapter, event, personID)
	priorTask := agentcontract.PriorTaskContext{}
	if !isApprovalContinuation && !hasPendingAskInteraction && !hasActiveGoal {
		priorTask, _ = connectorRuntime.findPriorTaskContext(personID, event)
	}

	connectorRuntime.logger.Info("connector."+platform+".agent.started", slog.String("messageID", event.MessageID))
	precomputedTurnDecision := precomputedTurnDecisionForLaunch(turnDecision, hasPendingConfirmation, askTurnDecision, hasAskTurnDecision)
	taskStartedAt := time.Now()
	conversationTurn := ConversationTurn{
		Platform:                  platform,
		Adapter:                   adapter,
		Event:                     event,
		ReplyTarget:               replyTarget,
		RequesterPersonID:         personID,
		RequesterEmail:            requesterEmail,
		PersonAccess:              personAccess,
		IsApprovalContinuation:    isApprovalContinuation,
		ActiveGoal:                activeGoal,
		HasActiveGoal:             hasActiveGoal,
		PriorTask:                 priorTask,
		PrecomputedTurnDecision:   precomputedTurnDecision,
		AmbientDuty:               addressingLaunch.AmbientDuty,
		CheckpointSender:          connectorRuntime.checkpointSenderForTurn(platform, event, replyTarget, sendReply),
		AccessibleConversationIDs: []string{event.ConversationID},
		IsBlockedContinuation:     activeGoal.Status == agentcontract.ActiveGoalStatusBlocked && hasActiveGoal,
	}
	narrator := connectorRuntime.startNarrating(ctx, adapter, replyTarget)
	defer narrator.stop()
	sendReply = narrator.takeOverSending(sendReply)
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, connectorRuntime.buildTaskLaunchRequest(conversationTurn))
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".agent.failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		failureTurnResult := connectorRuntime.launchFailureCompleter.CompleteLaunchFailure(ctx, agentcontract.AgentTurnRequest{
			RequesterPersonID: personID,
			RequesterEmail:    requesterEmail,
			Platform:          platform,
			ConversationID:    event.ConversationID,
			Prompt:            event.Prompt,
			ResponseLanguage:  event.Context.ResponseLanguage,
		}, "launch", "connector_launch", errorValue)
		return connectorRuntime.dispatchTaskReply(ctx, platform, adapter, event, replyTarget, failureTurnResult, engagedAckEmojiName, sendReply)
	}
	turnResult := launchResult.TurnResult
	if addressingLaunch.SuppressReply {
		turnResult.ReplySuppressionReason = "ambient_duty_no_reply"
	}
	taskRunID := turnResult.TaskRun.TaskRunID
	taskDuration := time.Since(taskStartedAt)
	connectorRuntime.logger.Info("connector."+platform+".agent.completed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.Int64("duration_ms", taskDuration.Milliseconds()))
	connectorRuntime.appendTaskExecutionDuration(taskRunID, taskDuration)
	return connectorRuntime.dispatchTaskReply(ctx, platform, adapter, event, replyTarget, turnResult, engagedAckEmojiName, sendReply)
}

func (connectorRuntime *ConnectorRuntime) shouldDeferNewTaskLaunch(isApprovalContinuation bool, hasPendingAskInteraction bool, hasActiveGoal bool) bool {
	if connectorRuntime.taskIntakeGate == nil || !connectorRuntime.taskIntakeGate.IsQuiesced() {
		return false
	}
	return !isApprovalContinuation && !hasPendingAskInteraction && !hasActiveGoal
}

func shouldDeferQueuedConnectorEvent(result ConnectorRuntimeResult) bool {
	return result.Ignored && result.Reason == "task_intake_quiesced"
}

const engagedAckReactionEmojiName = "eyes"

func (connectorRuntime *ConnectorRuntime) applyEngagedAckReaction(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, isEngaged bool) string {
	if !isEngaged || !isMultiPersonConversation(event) {
		return ""
	}
	reactionAdapter, isSupported := adapter.(MessageReactionAdapter)
	if !isSupported {
		return ""
	}
	target := ReactionTarget{
		Platform:       platform,
		ConversationID: event.ConversationID,
		MessageID:      event.MessageID,
		EmojiName:      engagedAckReactionEmojiName,
		Reason:         "engaged_ack",
	}
	if errorValue := reactionAdapter.AddReaction(ctx, target); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".reaction.failed", slog.String("messageID", event.MessageID), slog.String("emojiName", target.EmojiName), slog.String("error", errorValue.Error()))
		return ""
	}
	return engagedAckReactionEmojiName
}

func (connectorRuntime *ConnectorRuntime) clearEngagedAckReaction(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, emojiName string) {
	if strings.TrimSpace(emojiName) == "" {
		return
	}
	removalAdapter, isSupported := adapter.(MessageReactionRemovalAdapter)
	if !isSupported {
		return
	}
	target := ReactionTarget{
		Platform:       platform,
		ConversationID: event.ConversationID,
		MessageID:      event.MessageID,
		EmojiName:      emojiName,
		Reason:         "engaged_ack_cleared",
	}
	if errorValue := removalAdapter.RemoveReaction(ctx, target); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".reaction.remove_failed", slog.String("messageID", event.MessageID), slog.String("emojiName", emojiName), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) addAddressingReaction(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, reactionEmojiName string) {
	reactionAdapter, isSupported := adapter.(MessageReactionAdapter)
	if !isSupported {
		return
	}
	target := ReactionTarget{
		Platform:       platform,
		ConversationID: event.ConversationID,
		MessageID:      event.MessageID,
		EmojiName:      consumeReactionEmojiName(reactionEmojiName),
		Reason:         "addressing_ack",
	}
	if errorValue := reactionAdapter.AddReaction(ctx, target); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".reaction.failed", slog.String("messageID", event.MessageID), slog.String("emojiName", target.EmojiName), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) addConsumeReaction(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, taskRunID string, reactionEmojiName string) string {
	reactionAdapter, isSupported := adapter.(MessageReactionAdapter)
	if !isSupported {
		connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, "connector.reaction.skipped", marshalConnectorEventBody(map[string]string{
			"messageID": event.MessageID,
			"reason":    "reaction_adapter_unavailable",
		}))
		return "consume_no_reaction_adapter"
	}
	target := ReactionTarget{
		Platform:       platform,
		ConversationID: event.ConversationID,
		MessageID:      event.MessageID,
		EmojiName:      consumeReactionEmojiName(reactionEmojiName),
		Reason:         "consume",
	}
	if errorValue := reactionAdapter.AddReaction(ctx, target); errorValue != nil {
		connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, "connector.reaction.failed", marshalConnectorEventBody(map[string]string{
			"messageID": event.MessageID,
			"emojiName": target.EmojiName,
			"error":     errorValue.Error(),
		}))
		connectorRuntime.logger.Warn("connector."+platform+".reaction.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return "consume_reaction_failed"
	}
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, "connector.reaction.sent", marshalConnectorEventBody(map[string]string{
		"messageID": event.MessageID,
		"emojiName": target.EmojiName,
		"reason":    target.Reason,
	}))
	return "consume_reacted"
}

func consumeReactionEmojiName(reactionEmojiName string) string {
	reactionEmojiName = strings.TrimSpace(reactionEmojiName)
	if reactionEmojiName == "" {
		return agentcontract.DefaultReactionEmojiName
	}
	return reactionEmojiName
}

func (connectorRuntime *ConnectorRuntime) appendTaskExecutionDuration(taskRunID string, duration time.Duration) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, "blueclaw.task.execution_duration", marshalConnectorEventBody(map[string]any{
		"durationMs": duration.Milliseconds(),
	}))
}

func (connectorRuntime *ConnectorRuntime) resolveConfirmationReply(ctx context.Context, platform string, personID string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (pendingApproval, agentcontract.TurnDecision, bool, error) {
	approval, isFound := connectorRuntime.findPendingApproval(personID, platform, event, taskWaitResolution)
	if !isFound {
		return pendingApproval{}, agentcontract.TurnDecision{}, false, nil
	}
	decision, errorValue := connectorRuntime.classifiedConfirmationDecision(ctx, platform, personID, event, approval)
	if errorValue != nil {
		return pendingApproval{}, agentcontract.TurnDecision{}, false, errorValue
	}
	decision.Approval = approvalSignalSurvivingRoute(decision.Approval, decision.Route)
	approvalgate.RecordRequesterDecision(connectorRuntime.taskRunService, approval.TaskRun.TaskRunID, decision.Approval, "chat_reply")
	if decision.Approval != nil && *decision.Approval == agentcontract.ApprovalSignalApproveTask {
		connectorRuntime.grantApprovalScopeForTask(approval.TaskRun.TaskRunID)
	}
	return approval, decision, true, nil
}

func approvalSignalSurvivingRoute(approvalSignal *agentcontract.ApprovalSignal, route agentcontract.TurnRoute) *agentcontract.ApprovalSignal {
	if approvalSignal == nil || !agentcontract.IsApprovingSignal(*approvalSignal) {
		return approvalSignal
	}
	if route != agentcontract.TurnRouteReviseTask && route != agentcontract.TurnRouteStartTask {
		return approvalSignal
	}
	unclearSignal := agentcontract.ApprovalSignalUnclear
	return &unclearSignal
}

func (connectorRuntime *ConnectorRuntime) classifiedConfirmationDecision(ctx context.Context, platform string, personID string, event PlatformInboundEvent, approval pendingApproval) (agentcontract.TurnDecision, error) {
	decision, errorValue := connectorRuntime.planTurn(ctx, approval.TaskRun.TaskRunID, agentcontract.AgentRequest{
		RequesterPersonID: personID,
		ConversationID:    event.ConversationID,
		Prompt:            event.Prompt,
		ResponseLanguage:  responseLanguageForEvent(event),
		VisibleContext:    event.Context.ToAgentVisibleContext(),
		PendingConfirmation: agentcontract.PendingConfirmationContext{
			TaskRunID: approval.TaskRun.TaskRunID,
			Prompt:    approval.IntentPrompt,
			Question:  approval.ApprovalQuestion,
		},
		TurnStartedAt: time.Now(),
	})
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
	}
	connectorRuntime.taskRunService.AppendTaskEvent(approval.TaskRun.TaskRunID, "confirmation.reply_classified", marshalConnectorEventBody(map[string]any{
		"messageID":   event.MessageID,
		"route":       decision.Route,
		"approval":    decision.Approval,
		"reason":      decision.Reason,
		"replyPrompt": strings.TrimSpace(event.Prompt),
	}))
	if decision.Approval != nil && agentcontract.IsApprovingSignal(*decision.Approval) {
		connectorRuntime.logger.Info("connector."+platform+".confirmation.accepted", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID))
	}
	return decision, nil
}

func (connectorRuntime *ConnectorRuntime) resolveAskReply(ctx context.Context, platform string, personID string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (PlatformInboundEvent, agentcontract.TurnDecision, bool, error) {
	pendingInteraction, isFound := connectorRuntime.findPendingAskInteraction(personID, platform, event, taskWaitResolution)
	if !isFound {
		return event, agentcontract.TurnDecision{}, false, nil
	}
	if pendingInteraction.Kind == "ask_input" {
		decision, errorValue := connectorRuntime.planTurn(ctx, pendingInteraction.TaskRunID, agentcontract.AgentRequest{
			RequesterPersonID: personID,
			ConversationID:    event.ConversationID,
			Prompt:            event.Prompt,
			ResponseLanguage:  responseLanguageForEvent(event),
			VisibleContext:    event.Context.ToAgentVisibleContext(),
			PendingInput: agentcontract.PendingInputContext{
				TaskRunID:     pendingInteraction.TaskRunID,
				Question:      pendingInteraction.Question,
				SelectionMode: pendingInteraction.SelectionMode,
				Options:       choiceReplyOptions(pendingInteraction.Options),
			},
			TurnStartedAt: time.Now(),
		})
		return event, decision, true, errorValue
	}
	if pendingInteraction.Kind != "ask_choice_single" && pendingInteraction.Kind != "ask_choice_multiple" {
		return event, agentcontract.TurnDecision{}, false, nil
	}
	decision, errorValue := connectorRuntime.planTurn(ctx, pendingInteraction.TaskRunID, agentcontract.AgentRequest{
		RequesterPersonID: personID,
		ConversationID:    event.ConversationID,
		Prompt:            event.Prompt,
		ResponseLanguage:  responseLanguageForEvent(event),
		VisibleContext:    event.Context.ToAgentVisibleContext(),
		PendingChoice: agentcontract.PendingChoiceContext{
			TaskRunID:     pendingInteraction.TaskRunID,
			Question:      pendingInteraction.Question,
			SelectionMode: pendingInteraction.SelectionMode,
			Options:       choiceReplyOptions(pendingInteraction.Options),
		},
		TurnStartedAt: time.Now(),
	})
	if errorValue != nil {
		return event, agentcontract.TurnDecision{}, false, errorValue
	}
	connectorRuntime.taskRunService.AppendTaskEvent(pendingInteraction.TaskRunID, "ask.reply_classified", marshalConnectorEventBody(map[string]any{
		"messageID": event.MessageID,
		"choices":   decision.Choices,
		"route":     decision.Route,
		"reason":    decision.Reason,
	}))
	if len(decision.Choices) == 0 {
		return event, decision, true, nil
	}
	event.Prompt = resolvedChoicePrompt(pendingInteraction, decision.Choices)
	return event, decision, true, nil
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

func resolvedChoicePrompt(interaction AskInteraction, keys []string) string {
	values := []string{}
	for _, key := range keys {
		values = append(values, resolvedChoiceKeyText(interaction, key))
	}
	return "User selected: " + strings.Join(trimNonEmptyConnectorStrings(values), ", ")
}

func shouldStopAfterPendingConfirmation(decision agentcontract.TurnDecision) bool {
	if decision.Approval != nil && *decision.Approval == agentcontract.ApprovalSignalApprove {
		return false
	}
	switch decision.Route {
	case agentcontract.TurnRouteAnswerQuestion, agentcontract.TurnRouteStartTask, agentcontract.TurnRouteReviseTask:
		return false
	default:
		return true
	}
}

func precomputedTurnDecisionForLaunch(confirmationDecision agentcontract.TurnDecision, hasConfirmationDecision bool, askDecision agentcontract.TurnDecision, hasAskDecision bool) *agentcontract.TurnDecision {
	if hasConfirmationDecision {
		return &confirmationDecision
	}
	if hasAskDecision {
		return &askDecision
	}
	return nil
}

func (connectorRuntime *ConnectorRuntime) cancelPendingConfirmation(event PlatformInboundEvent, approval pendingApproval, decision agentcontract.TurnDecision) {
	_, _ = connectorRuntime.taskRunService.CancelTaskRunWithReason(approval.TaskRun.TaskRunID, approval.TaskRun.RequesterPersonID, "confirmation.replaced")
	connectorRuntime.taskRunService.AppendTaskEvent(approval.TaskRun.TaskRunID, "confirmation.replaced", marshalConnectorEventBody(map[string]string{
		"messageID": event.MessageID,
		"route":     string(decision.Route),
		"reason":    strings.TrimSpace(decision.Reason),
	}))
}

func resolvedChoiceKeyText(interaction AskInteraction, choiceKey string) string {
	normalizedChoiceKey := strings.TrimSpace(choiceKey)
	for _, option := range interaction.Options {
		if strings.TrimSpace(option.Key) != normalizedChoiceKey {
			continue
		}
		value := strings.TrimSpace(option.Value)
		if value == "" {
			value = strings.TrimSpace(option.Label)
		}
		if value == "" {
			value = normalizedChoiceKey
		}
		return normalizedChoiceKey + " / " + value
	}
	return normalizedChoiceKey
}

func askReplyConsumesInteraction(interaction AskInteraction, previousPrompt string, event PlatformInboundEvent, decision agentcontract.TurnDecision, hasDecision bool) bool {
	if !hasDecision {
		return false
	}
	switch strings.TrimSpace(interaction.Kind) {
	case "ask_input":
		return decision.Route == agentcontract.TurnRouteContinueTask || decision.Route == agentcontract.TurnRouteReviseTask || decision.Route == agentcontract.TurnRouteStartTask
	default:
		return strings.TrimSpace(event.Prompt) != strings.TrimSpace(previousPrompt)
	}
}

func askReplySupersedesInteraction(decision agentcontract.TurnDecision, hasDecision bool) bool {
	return hasDecision && decision.Route == agentcontract.TurnRouteStartTask
}

func (connectorRuntime *ConnectorRuntime) supersedePendingAskInteraction(event PlatformInboundEvent, interaction AskInteraction, decision agentcontract.TurnDecision) {
	taskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(interaction.TaskRunID)
	if isFound {
		_, _ = connectorRuntime.taskRunService.CancelTaskRunWithReason(taskRun.TaskRunID, taskRun.RequesterPersonID, "superseded_by_new_message")
		connectorRuntime.resolveOpenTaskWaitsForTaskRun(taskRun.RequesterPersonID, event.Platform, taskRun.OriginConversationID, taskRun.TaskRunID)
	}
	connectorRuntime.taskRunService.AppendTaskEvent(interaction.TaskRunID, "ask.superseded_by_message", marshalConnectorEventBody(map[string]string{
		"interactionID":   strings.TrimSpace(interaction.InteractionID),
		"messageID":       strings.TrimSpace(event.MessageID),
		"route":           strings.TrimSpace(string(decision.Route)),
		"reason":          strings.TrimSpace(decision.Reason),
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
}

func (connectorRuntime *ConnectorRuntime) appendAskResolvedEvent(interaction AskInteraction, event PlatformInboundEvent, decision agentcontract.TurnDecision) {
	connectorRuntime.taskRunService.AppendTaskEvent(interaction.TaskRunID, "ask.resolved", marshalConnectorEventBody(map[string]any{
		"interactionID": strings.TrimSpace(interaction.InteractionID),
		"kind":          strings.TrimSpace(interaction.Kind),
		"messageID":     strings.TrimSpace(event.MessageID),
		"choices":       append([]string{}, decision.Choices...),
		"route":         strings.TrimSpace(string(decision.Route)),
		"reason":        strings.TrimSpace(decision.Reason),
	}))
}

func trimNonEmptyConnectorStrings(values []string) []string {
	trimmedValues := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			trimmedValues = append(trimmedValues, trimmedValue)
		}
	}
	return trimmedValues
}

func (connectorRuntime *ConnectorRuntime) resolveInboundTaskWait(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	if connectorRuntime.taskWaitTokenRepository == nil {
		return inboundTaskWaitResolution{}
	}
	if resolution := connectorRuntime.findInboundTaskWaitByPayload(personID, event); resolution.HasTaskWaitToken {
		return resolution
	}
	if resolution := connectorRuntime.findInboundTaskWaitByReplyTarget(personID, platform, event); resolution.HasTaskWaitToken {
		return resolution
	}
	if resolution := connectorRuntime.findInboundTaskWaitByThreadRoot(personID, platform, event); resolution.HasTaskWaitToken {
		return resolution
	}
	if resolution := connectorRuntime.findInboundTaskWaitByDispatchID(personID, platform, event); resolution.HasTaskWaitToken {
		return resolution
	}
	return connectorRuntime.findSingleInboundTaskWait(personID, platform, event)
}

func (connectorRuntime *ConnectorRuntime) findInboundTaskWaitByPayload(personID string, event PlatformInboundEvent) inboundTaskWaitResolution {
	if waitID := firstNonEmptyString(legacyString(event.LegacyFields, "waitID"), legacyString(event.LegacyFields, "wait_id")); waitID != "" {
		return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
			return connectorRuntime.taskWaitTokenRepository.FindOpenByWaitID(waitID)
		}, personID, "payload_wait_id")
	}
	taskRunID := legacyString(event.LegacyFields, "taskRunID")
	interactionID := legacyString(event.LegacyFields, "interactionID")
	if taskRunID == "" || interactionID == "" {
		return inboundTaskWaitResolution{}
	}
	return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
		return connectorRuntime.taskWaitTokenRepository.FindOpenByPersonTaskRunAndInteraction(personID, taskRunID, interactionID)
	}, personID, "payload_task_interaction")
}

func (connectorRuntime *ConnectorRuntime) findInboundTaskWaitByReplyTarget(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	replyTargetID := strings.TrimSpace(event.ReplyTargetID)
	if replyTargetID == "" {
		return inboundTaskWaitResolution{}
	}
	return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
		return connectorRuntime.taskWaitTokenRepository.FindOpenByPersonConversationAndReplyTarget(personID, platform, event.ConversationID, replyTargetID)
	}, personID, "reply_target_id")
}

func (connectorRuntime *ConnectorRuntime) findInboundTaskWaitByThreadRoot(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	threadRootID := eventThreadRootID(event)
	if threadRootID == "" {
		return inboundTaskWaitResolution{}
	}
	return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
		return connectorRuntime.taskWaitTokenRepository.FindOpenByPersonConversationAndThreadRoot(personID, platform, event.ConversationID, threadRootID)
	}, personID, "thread_root_id")
}

func (connectorRuntime *ConnectorRuntime) findInboundTaskWaitByDispatchID(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	dispatchID := firstNonEmptyString(legacyString(event.LegacyFields, "dispatchID"), legacyString(event.LegacyFields, "postID"))
	if dispatchID == "" {
		return inboundTaskWaitResolution{}
	}
	return connectorRuntime.findOpenTaskWait(func() (task.TaskWaitToken, bool, error) {
		return connectorRuntime.taskWaitTokenRepository.FindOpenByPersonConversationAndDispatchID(personID, platform, event.ConversationID, dispatchID)
	}, personID, "dispatch_id")
}

func (connectorRuntime *ConnectorRuntime) findSingleInboundTaskWait(personID string, platform string, event PlatformInboundEvent) inboundTaskWaitResolution {
	taskWaitTokens, errorValue := connectorRuntime.taskWaitTokenRepository.FindOpenByPersonAndConversation(personID, platform, event.ConversationID)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".wait.lookup_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return inboundTaskWaitResolution{}
	}
	switch len(taskWaitTokens) {
	case 0:
		return inboundTaskWaitResolution{}
	case 1:
		return inboundTaskWaitResolution{TaskWaitToken: taskWaitTokens[0], HasTaskWaitToken: true, Reason: "single_open_wait"}
	default:
		return inboundTaskWaitResolution{IsAmbiguous: true, AmbiguousTaskWaits: taskWaitTokens, Reason: "multiple_open_waits"}
	}
}

func (connectorRuntime *ConnectorRuntime) findOpenTaskWait(find func() (task.TaskWaitToken, bool, error), personID string, reason string) inboundTaskWaitResolution {
	taskWaitToken, isFound, errorValue := find()
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.wait.lookup_failed", slog.String("reason", reason), slog.String("error", errorValue.Error()))
		return inboundTaskWaitResolution{}
	}
	if !isFound || taskWaitToken.PersonID != strings.TrimSpace(personID) {
		return inboundTaskWaitResolution{}
	}
	return inboundTaskWaitResolution{TaskWaitToken: taskWaitToken, HasTaskWaitToken: true, Reason: reason}
}

func eventThreadRootID(event PlatformInboundEvent) string {
	if eventIsThreadReply(event) {
		return strings.TrimSpace(event.ReplyTargetID)
	}
	return ""
}

func (connectorRuntime *ConnectorRuntime) resolveTaskWaitToken(taskWaitResolution inboundTaskWaitResolution) {
	if connectorRuntime.taskWaitTokenRepository == nil || !taskWaitResolution.HasTaskWaitToken {
		return
	}
	if errorValue := connectorRuntime.taskWaitTokenRepository.ResolveTaskWait(taskWaitResolution.TaskWaitToken.WaitID, time.Now().UTC()); errorValue != nil {
		connectorRuntime.logger.Warn("connector.wait.resolve_failed", slog.String("waitID", taskWaitResolution.TaskWaitToken.WaitID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) resolveOpenTaskWaitsForTaskRun(personID string, platform string, conversationID string, taskRunID string) {
	if connectorRuntime.taskWaitTokenRepository == nil {
		return
	}
	taskWaitTokens, errorValue := connectorRuntime.taskWaitTokenRepository.FindOpenByPersonAndConversation(personID, platform, conversationID)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.wait.lookup_failed", slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return
	}
	for _, taskWaitToken := range taskWaitTokens {
		if taskWaitToken.TaskRunID != strings.TrimSpace(taskRunID) {
			continue
		}
		if errorValue := connectorRuntime.taskWaitTokenRepository.ResolveTaskWait(taskWaitToken.WaitID, time.Now().UTC()); errorValue != nil {
			connectorRuntime.logger.Warn("connector.wait.resolve_failed", slog.String("waitID", taskWaitToken.WaitID), slog.String("error", errorValue.Error()))
		}
	}
}

func (connectorRuntime *ConnectorRuntime) handleAmbiguousTaskWait(
	ctx context.Context,
	platform string,
	adapter PlatformAdapter,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	personID string,
	requesterEmail string,
	personAccess policy.PersonAccess,
	taskWaitResolution inboundTaskWaitResolution,
	engagedAckEmojiName string,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (ConnectorRuntimeResult, error) {
	turnDecision := ambiguousTaskWaitTurnDecision(taskWaitResolution.AmbiguousTaskWaits, responseLanguageForEvent(event))
	conversationTurn := ConversationTurn{
		Platform:                  platform,
		Adapter:                   adapter,
		Event:                     event,
		ReplyTarget:               replyTarget,
		RequesterPersonID:         personID,
		RequesterEmail:            requesterEmail,
		PersonAccess:              personAccess,
		PrecomputedTurnDecision:   &turnDecision,
		CheckpointSender:          connectorRuntime.checkpointSenderForTurn(platform, event, replyTarget, sendReply),
		AccessibleConversationIDs: []string{event.ConversationID},
	}
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, connectorRuntime.buildTaskLaunchRequest(conversationTurn))
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	return connectorRuntime.dispatchTaskReply(ctx, platform, adapter, event, replyTarget, launchResult.TurnResult, engagedAckEmojiName, sendReply)
}

func ambiguousTaskWaitTurnDecision(taskWaitTokens []task.TaskWaitToken, responseLanguage string) agentcontract.TurnDecision {
	return agentcontract.TurnDecision{
		Route:                  agentcontract.TurnRouteClarify,
		Classification:         agentcontract.IntakeClassificationNeedsConfirmation,
		TaskShape:              agentcontract.TaskShapeApprovalGatedTask,
		TaskLevel:              agentcontract.TaskLevelLow,
		ResponseLanguage:       responseLanguage,
		Reason:                 "ambiguous_wait_resolution",
		ClarificationOptions:   taskWaitClarificationOptions(taskWaitTokens),
		ExpectedResults:        []agentcontract.ExpectedResult{{ID: "wait-disambiguation", Type: "message", Description: "ask_choice", Required: true, AcceptanceHints: []string{"ask_choice"}}},
		RequestedOutputFormats: nil,
	}
}

func taskWaitClarificationOptions(taskWaitTokens []task.TaskWaitToken) []agentcontract.ClarificationOption {
	options := []agentcontract.ClarificationOption{}
	for index, taskWaitToken := range taskWaitTokens {
		taskRunLabel := strings.TrimSpace(taskWaitToken.TaskRunID)
		if len(taskRunLabel) > 8 {
			taskRunLabel = taskRunLabel[:8]
		}
		options = append(options, agentcontract.ClarificationOption{
			Key:   string(rune('A' + index)),
			Label: taskRunLabel,
			Value: taskWaitToken.WaitID,
		})
	}
	return options
}

func (connectorRuntime *ConnectorRuntime) findPendingAskInteraction(personID string, _ string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (AskInteraction, bool) {
	if taskWaitResolution.HasTaskWaitToken {
		return connectorRuntime.findPendingAskInteractionByTaskRunID(taskWaitResolution.TaskWaitToken.TaskRunID)
	}
	taskRuns := connectorRuntime.taskRunService.ListTaskRunByPersonID(personID)
	var selectedInteraction AskInteraction
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range taskRuns {
		if taskRun.Status != task.TaskStatusWaitingUserInput {
			continue
		}
		if taskRun.OriginConversationID != event.ConversationID {
			continue
		}
		interaction, isFound := latestAskInteraction(taskRun.TaskRunID, connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID))
		if !isFound {
			continue
		}
		if isSelected && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		selectedInteraction = interaction
		isSelected = true
	}
	return selectedInteraction, isSelected
}

func (connectorRuntime *ConnectorRuntime) findPendingAskInteractionByTaskRunID(taskRunID string) (AskInteraction, bool) {
	taskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if !isFound || taskRun.Status != task.TaskStatusWaitingUserInput {
		return AskInteraction{}, false
	}
	return latestAskInteraction(taskRun.TaskRunID, connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID))
}

func (connectorRuntime *ConnectorRuntime) findPendingApproval(personID string, _ string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (pendingApproval, bool) {
	if taskWaitResolution.HasTaskWaitToken {
		return connectorRuntime.findPendingApprovalByTaskRunID(taskWaitResolution.TaskWaitToken.TaskRunID)
	}
	taskRuns := connectorRuntime.taskRunService.ListTaskRunByPersonID(personID)
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range taskRuns {
		if taskRun.Status != task.TaskStatusWaitingApproval {
			continue
		}
		if taskRun.OriginConversationID != event.ConversationID {
			continue
		}
		if time.Since(taskRun.UpdatedAt) > 24*time.Hour {
			continue
		}
		if isSelected && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		isSelected = true
	}
	if !isSelected {
		return pendingApproval{}, false
	}
	return connectorRuntime.pendingApprovalForTaskRun(selectedTaskRun), true
}

func (connectorRuntime *ConnectorRuntime) findPendingApprovalByTaskRunID(taskRunID string) (pendingApproval, bool) {
	taskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if !isFound || taskRun.Status != task.TaskStatusWaitingApproval {
		return pendingApproval{}, false
	}
	return connectorRuntime.pendingApprovalForTaskRun(taskRun), true
}

func (connectorRuntime *ConnectorRuntime) pendingApprovalForTaskRun(selectedTaskRun task.TaskRun) pendingApproval {
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(selectedTaskRun.TaskRunID)
	approvalQuestion := latestApprovalQuestion(taskEvents)
	responseLanguage := latestApprovalResponseLanguage(taskEvents)
	continuationInstruction := latestConfirmationContinuationInstruction(taskEvents)
	activeGoal := latestActiveGoal(taskEvents)
	return pendingApproval{
		TaskRun:                 selectedTaskRun,
		IntentPrompt:            strings.TrimSpace(selectedTaskRun.Prompt),
		ApprovalQuestion:        approvalQuestion,
		ResponseLanguage:        responseLanguage,
		ContinuationInstruction: continuationInstruction,
		ActiveGoal:              activeGoal,
	}
}

func (connectorRuntime *ConnectorRuntime) findActiveGoal(personID string, _ string, event PlatformInboundEvent, taskWaitResolution inboundTaskWaitResolution) (agentcontract.ActiveGoal, bool) {
	if taskWaitResolution.HasTaskWaitToken {
		return connectorRuntime.findActiveGoalByTaskRunID(taskWaitResolution.TaskWaitToken.TaskRunID)
	}
	taskRuns := connectorRuntime.taskRunService.ListTaskRunByPersonID(personID)
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range taskRuns {
		taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
		if !taskRunCanContinueGoal(taskRun, taskEvents) {
			continue
		}
		if taskRun.OriginConversationID != event.ConversationID {
			continue
		}
		if time.Since(taskRun.UpdatedAt) > 24*time.Hour {
			continue
		}
		if isSelected && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		isSelected = true
	}
	if !isSelected {
		return agentcontract.ActiveGoal{}, false
	}
	return connectorRuntime.activeGoalForTaskRun(selectedTaskRun), true
}

func (connectorRuntime *ConnectorRuntime) findActiveGoalByTaskRunID(taskRunID string) (agentcontract.ActiveGoal, bool) {
	taskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return agentcontract.ActiveGoal{}, false
	}
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
	if !taskRunCanContinueGoal(taskRun, taskEvents) {
		return agentcontract.ActiveGoal{}, false
	}
	return connectorRuntime.activeGoalForTaskRun(taskRun), true
}

func (connectorRuntime *ConnectorRuntime) findPriorTaskContext(personID string, event PlatformInboundEvent) (agentcontract.PriorTaskContext, bool) {
	taskRuns := connectorRuntime.taskRunService.ListTaskRunByPersonID(personID)
	var selectedTaskRun task.TaskRun
	var selectedContext agentcontract.PriorTaskContext
	isSelected := false
	for _, taskRun := range taskRuns {
		if !taskRunCanProvidePriorContext(taskRun) {
			continue
		}
		if taskRun.OriginConversationID != event.ConversationID {
			continue
		}
		if !taskRunMatchesReplyTarget(taskRun, event) {
			continue
		}
		if time.Since(taskRun.UpdatedAt) > 72*time.Hour {
			continue
		}
		taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
		context := priorTaskContextForTaskRun(taskRun, taskEvents)
		if isSelected && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		selectedContext = context
		isSelected = true
	}
	return selectedContext, isSelected
}

func (connectorRuntime *ConnectorRuntime) activeGoalForTaskRun(selectedTaskRun task.TaskRun) agentcontract.ActiveGoal {
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(selectedTaskRun.TaskRunID)
	activeGoal := latestActiveGoal(taskEvents)
	if strings.TrimSpace(activeGoal.TaskRunID) == "" {
		activeGoal.TaskRunID = selectedTaskRun.TaskRunID
	}
	if strings.TrimSpace(activeGoal.GoalID) == "" {
		activeGoal.GoalID = selectedTaskRun.TaskRunID
	}
	if strings.TrimSpace(activeGoal.OriginalInstruction) == "" {
		activeGoal.OriginalInstruction = selectedTaskRun.Prompt
	}
	if activeGoal.Status == "" {
		activeGoal.Status = activeGoalStatusForTaskRun(selectedTaskRun)
	}
	return activeGoal
}

func taskRunCanProvidePriorContext(taskRun task.TaskRun) bool {
	switch taskRun.Status {
	case task.TaskStatusBlocked, task.TaskStatusFailed, task.TaskStatusCompleted:
		return true
	default:
		return false
	}
}

func taskRunMatchesReplyTarget(taskRun task.TaskRun, event PlatformInboundEvent) bool {
	eventReplyTargetID := strings.TrimSpace(event.ReplyTargetID)
	taskReplyTargetID := strings.TrimSpace(taskRun.OriginReplyTargetID)
	if eventReplyTargetID != "" {
		return taskReplyTargetID == eventReplyTargetID
	}
	return taskReplyTargetID == ""
}

func priorTaskContextForTaskRun(taskRun task.TaskRun, taskEvents []task.TaskEvent) agentcontract.PriorTaskContext {
	activeGoal := latestActiveGoal(taskEvents)
	intakeDecision := latestIntakeDecision(taskEvents)
	requestedOutputFormats := appendUniqueConnectorStrings([]string{}, intakeDecision.RequestedOutputFormats...)
	requestedOutputFormats = appendUniqueConnectorStrings(requestedOutputFormats, outputFormatsFromAttachmentSuffixes(activeGoal.OutcomeContract.RequiredAttachmentSuffixes)...)
	return agentcontract.PriorTaskContext{
		TaskRunID:              strings.TrimSpace(taskRun.TaskRunID),
		Status:                 string(taskRun.Status),
		Prompt:                 strings.TrimSpace(taskRun.Prompt),
		Result:                 strings.TrimSpace(taskRun.Result),
		FailureReason:          strings.TrimSpace(taskRun.FailureReason),
		OutcomeContract:        activeGoal.OutcomeContract,
		RequestedOutputFormats: requestedOutputFormats,
	}
}

func outputFormatsFromAttachmentSuffixes(suffixes []string) []string {
	formats := []string{}
	for _, suffix := range suffixes {
		format := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(suffix)), ".")
		switch format {
		case "html", "pptx", "pdf", "txt", "docx", "xlsx", "csv":
			formats = appendUniqueConnectorStrings(formats, format)
		}
	}
	return formats
}

func (connectorRuntime *ConnectorRuntime) withPersistedIntakeState(taskRunID string, decision agentcontract.TurnDecision) agentcontract.TurnDecision {
	if decision.Route != agentcontract.TurnRouteContinueTask {
		return decision
	}
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRunID)
	return decision.WithRestoredIntakeState(latestIntakeDecision(taskEvents))
}

func latestIntakeDecision(taskEvents []task.TaskEvent) agentcontract.IntakeDecision {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "agent.intake" {
			continue
		}
		var decision agentcontract.IntakeDecision
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &decision); errorValue != nil {
			continue
		}
		return decision
	}
	return agentcontract.IntakeDecision{}
}

func appendUniqueConnectorStrings(values []string, candidates ...string) []string {
	result := append([]string{}, values...)
	seen := map[string]bool{}
	for _, value := range result {
		seen[strings.TrimSpace(value)] = true
	}
	for _, candidate := range candidates {
		trimmedCandidate := strings.TrimSpace(candidate)
		if trimmedCandidate == "" || seen[trimmedCandidate] {
			continue
		}
		seen[trimmedCandidate] = true
		result = append(result, trimmedCandidate)
	}
	return result
}

func taskRunCanContinueGoal(taskRun task.TaskRun, taskEvents []task.TaskEvent) bool {
	switch taskRun.Status {
	case task.TaskStatusWaitingUserInput, task.TaskStatusWaitingApproval:
		return true
	case task.TaskStatusBlocked:
		return taskRunHasLimitStop(taskEvents) || taskRunHasRecoverableArtifactDelivery(taskEvents)
	default:
		return false
	}
}

func taskRunHasLimitStop(taskEvents []task.TaskEvent) bool {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name == "agent.limit_stop" {
			return true
		}
	}
	return false
}

func taskRunHasRecoverableArtifactDelivery(taskEvents []task.TaskEvent) bool {
	activeGoal := latestActiveGoal(taskEvents)
	return outcomeContractRequiresFileAttachment(activeGoal.OutcomeContract)
}

func outcomeContractRequiresFileAttachment(contract agentcontract.OutcomeContract) bool {
	if len(contract.RequiredAttachmentSuffixes) > 0 {
		return true
	}
	if toolNamesContain(contract.RequiredEvidenceTools, "file_attach") {
		return true
	}
	if toolNameGroupsContain(contract.RequiredEvidenceAnyOf, "file_attach") {
		return true
	}
	for _, result := range contract.ExpectedResults {
		if result.Required && result.Type == agentcontract.ExpectedResultTypeFile {
			return true
		}
	}
	return contract.ArtifactRequirement == agentcontract.ArtifactRequirementRequired && agentcontract.OutcomeContractHasRequirements(contract)
}

func toolNamesContain(toolNames []string, expectedToolName string) bool {
	for _, toolName := range toolNames {
		if strings.TrimSpace(toolName) == expectedToolName {
			return true
		}
	}
	return false
}

func toolNameGroupsContain(toolNameGroups [][]string, expectedToolName string) bool {
	for _, toolNameGroup := range toolNameGroups {
		if toolNamesContain(toolNameGroup, expectedToolName) {
			return true
		}
	}
	return false
}

func latestActiveGoal(taskEvents []task.TaskEvent) agentcontract.ActiveGoal {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if !strings.HasPrefix(taskEvent.Name, "agent.goal.") {
			continue
		}
		var activeGoal agentcontract.ActiveGoal
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &activeGoal); errorValue != nil {
			return agentcontract.ActiveGoal{RestoreError: "latest active goal event is invalid: " + errorValue.Error()}
		}
		return activeGoal
	}
	return activeGoalFromConfirmationPlan(taskEvents)
}

func activeGoalFromConfirmationPlan(taskEvents []task.TaskEvent) agentcontract.ActiveGoal {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "confirmation.plan_created" {
			continue
		}
		var executionPlan agentcontract.ExecutionPlan
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &executionPlan); errorValue != nil {
			continue
		}
		return agentcontract.ActiveGoal{
			OriginalInstruction: strings.TrimSpace(executionPlan.OriginalInstruction),
			CurrentObjective:    strings.TrimSpace(executionPlan.Summary),
			MissingInformation:  append([]string{}, executionPlan.MissingInformation...),
			Status:              agentcontract.ActiveGoalStatusWaitingUserInput,
		}
	}
	return agentcontract.ActiveGoal{}
}

func activeGoalStatusForTaskRun(taskRun task.TaskRun) agentcontract.ActiveGoalStatus {
	switch taskRun.Status {
	case task.TaskStatusWaitingApproval:
		return agentcontract.ActiveGoalStatusWaitingApproval
	case task.TaskStatusWaitingUserInput:
		return agentcontract.ActiveGoalStatusWaitingUserInput
	case task.TaskStatusBlocked:
		return agentcontract.ActiveGoalStatusBlocked
	default:
		return agentcontract.ActiveGoalStatusActive
	}
}

func approvedContinuationEvent(event PlatformInboundEvent, approval pendingApproval) PlatformInboundEvent {
	event.ResponseLanguage = toolcontract.ResolveResponseLanguage(event.ResponseLanguage, approval.ResponseLanguage)
	return event
}

func pendingApprovalActiveGoal(approval pendingApproval, approvalReply string) agentcontract.ActiveGoal {
	activeGoal := approval.ActiveGoal
	activeGoal.GoalID = firstNonEmptyString(activeGoal.GoalID, approval.TaskRun.TaskRunID)
	activeGoal.TaskRunID = firstNonEmptyString(activeGoal.TaskRunID, approval.TaskRun.TaskRunID)
	activeGoal.OriginalInstruction = firstNonEmptyString(activeGoal.OriginalInstruction, approval.IntentPrompt)
	approvedAction := firstNonEmptyString(activeGoal.CurrentObjective, approval.ContinuationInstruction, approval.IntentPrompt)
	executionDirective := "The user already approved this action; perform it now and do not call ask_confirm again."
	activeGoal.CurrentObjective = strings.TrimSpace(approvedAction + " " + executionDirective)
	activeGoal.KnownContext = append(activeGoal.KnownContext, "The user approved the pending action in the latest message: "+strings.TrimSpace(approvalReply))
	activeGoal.Status = agentcontract.ActiveGoalStatusActive
	return activeGoal
}

func activeGoalForLaunch(activeGoal agentcontract.ActiveGoal, hasActiveGoal bool) agentcontract.ActiveGoal {
	if !hasActiveGoal {
		return agentcontract.ActiveGoal{}
	}
	return activeGoal
}

func pendingConfirmationTaskRunID(approval pendingApproval, isApprovalContinuation bool) string {
	if !isApprovalContinuation {
		return ""
	}
	return strings.TrimSpace(approval.TaskRun.TaskRunID)
}

func existingGoalTaskRunID(approval pendingApproval, isApprovalContinuation bool, activeGoal agentcontract.ActiveGoal, hasActiveGoal bool) string {
	if isApprovalContinuation {
		return pendingConfirmationTaskRunID(approval, true)
	}
	if !hasActiveGoal {
		return ""
	}
	return strings.TrimSpace(activeGoal.TaskRunID)
}

func (connectorRuntime *ConnectorRuntime) handleRejectedConfirmation(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, replyTarget ReplyTarget, approval pendingApproval, decision agentcontract.ConfirmationReplyDecision, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (ConnectorRuntimeResult, error) {
	_, _ = connectorRuntime.taskRunService.CancelTaskRunWithReason(approval.TaskRun.TaskRunID, approval.TaskRun.RequesterPersonID, "confirmation.rejected")
	connectorRuntime.taskRunService.AppendTaskEvent(approval.TaskRun.TaskRunID, "confirmation.rejected", marshalConnectorEventBody(map[string]string{
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

func (connectorRuntime *ConnectorRuntime) handlePendingConfirmationQuestion(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, replyTarget ReplyTarget, approval pendingApproval, decision agentcontract.TurnDecision, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (ConnectorRuntimeResult, error) {
	connectorRuntime.cancelPendingConfirmation(event, approval, decision)
	reply, errorValue := connectorRuntime.replyGenerator.GenerateReplyWithContext(ctx, event.Prompt, event.Context.ToAgentVisibleContext(), nil)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".confirmation.question_reply_failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: approval.TaskRun.TaskRunID, Reason: "confirmation_question"}, nil
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply})
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: approval.TaskRun.TaskRunID, Reason: "reply_failed"}, nil
	}
	connectorRuntime.logger.Info("connector."+adapter.Name()+".confirmation.question_answered", slog.String("messageID", event.MessageID), slog.String("taskRunID", approval.TaskRun.TaskRunID), slog.String("replyDispatchID", dispatchID))
	return ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: approval.TaskRun.TaskRunID, Reason: "confirmation_question", ReplyDispatchID: dispatchID}, nil
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

func latestApprovalQuestion(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "confirmation.requested" {
			continue
		}
		var approvalRequest struct {
			UserFacingMessage string `json:"userFacingMessage"`
			Message           string `json:"message"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &approvalRequest); errorValue != nil {
			continue
		}
		question := firstNonEmptyString(approvalRequest.UserFacingMessage, approvalRequest.Message)
		if strings.TrimSpace(question) != "" {
			return strings.TrimSpace(question)
		}
	}
	return ""
}

func latestApprovalResponseLanguage(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "confirmation.requested" {
			continue
		}
		var approvalRequest struct {
			ResponseLanguage string `json:"responseLanguage"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &approvalRequest); errorValue != nil {
			continue
		}
		if responseLanguage := toolcontract.NormalizeResponseLanguage(approvalRequest.ResponseLanguage); responseLanguage != "" {
			return responseLanguage
		}
	}
	return ""
}

func latestAskInteraction(taskRunID string, taskEvents []task.TaskEvent) (AskInteraction, bool) {
	resolvedInteractionIDs := map[string]bool{}
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name == "ask.resolved" {
			interactionID := askResolvedInteractionID(taskEvent)
			if interactionID != "" {
				resolvedInteractionIDs[interactionID] = true
			}
			continue
		}
		if taskEvent.Name != "ask.requested" {
			continue
		}
		var interaction struct {
			AskInteraction
			Choices []string `json:"choices,omitempty"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &interaction); errorValue != nil {
			continue
		}
		interaction.AskInteraction.TaskRunID = firstNonEmptyString(interaction.TaskRunID, taskRunID)
		interaction.AskInteraction.InteractionID = firstNonEmptyString(interaction.InteractionID, taskEvent.TaskEventID)
		if resolvedInteractionIDs[strings.TrimSpace(interaction.InteractionID)] {
			continue
		}
		legacyKind := strings.TrimSpace(interaction.Kind)
		interaction.AskInteraction.Kind = normalizedAskInteractionKind(legacyKind)
		if len(interaction.Options) == 0 && len(interaction.Choices) > 0 {
			interaction.AskInteraction.Options = askOptionsFromLegacyChoices(interaction.Choices)
		}
		if interaction.Kind == "ask_input" && strings.TrimSpace(interaction.SelectionMode) == "" && len(interaction.Options) > 0 {
			interaction.AskInteraction.SelectionMode = askInputSelectionMode(legacyKind)
		}
		if strings.TrimSpace(interaction.Question) == "" {
			interaction.Question = strings.TrimSpace(interaction.Message)
		}
		if strings.TrimSpace(interaction.Message) == "" {
			interaction.Message = strings.TrimSpace(interaction.Question)
		}
		if strings.TrimSpace(interaction.Kind) != "" {
			return interaction.AskInteraction, true
		}
	}
	return AskInteraction{}, false
}

func askResolvedInteractionID(taskEvent task.TaskEvent) string {
	var resolution struct {
		InteractionID string `json:"interactionID"`
	}
	if errorValue := json.Unmarshal([]byte(taskEvent.Body), &resolution); errorValue != nil {
		return ""
	}
	return strings.TrimSpace(resolution.InteractionID)
}

func latestAskInteractionID(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name == "ask.requested" {
			return strings.TrimSpace(taskEvent.TaskEventID)
		}
	}
	return ""
}

func latestAskPromptDispatchID(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "connector.reply.sent" {
			continue
		}
		var replyEvent struct {
			ReplyKind  string `json:"replyKind"`
			DispatchID string `json:"dispatchID"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &replyEvent); errorValue != nil {
			continue
		}
		if strings.TrimSpace(replyEvent.ReplyKind) == connectorReplyKindUserNotice && strings.TrimSpace(replyEvent.DispatchID) != "" {
			return strings.TrimSpace(replyEvent.DispatchID)
		}
	}
	return ""
}

func legacyString(fields map[string]interface{}, key string) string {
	value, _ := fields[key].(string)
	return strings.TrimSpace(value)
}

func legacyBool(fields map[string]interface{}, key string) bool {
	value, _ := fields[key].(bool)
	return value
}

func askOptionsFromLegacyChoices(choices []string) []AskChoiceOption {
	options := []AskChoiceOption{}
	for index, choice := range choices {
		trimmedChoice := strings.TrimSpace(choice)
		if trimmedChoice == "" {
			continue
		}
		options = append(options, AskChoiceOption{
			Key:   strconv.Itoa(index + 1),
			Label: trimmedChoice,
			Value: trimmedChoice,
		})
	}
	return options
}

func askInputSelectionMode(legacyKind string) string {
	if strings.TrimSpace(legacyKind) == "choice_multiple" {
		return "multiple"
	}
	return "single"
}

func normalizedAskInteractionKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "confirm":
		return "ask_confirm"
	case "choice_single":
		return "ask_input"
	case "choice_multiple":
		return "ask_input"
	case "input", "input_choice":
		return "ask_input"
	default:
		return strings.TrimSpace(kind)
	}
}

func latestConfirmationContinuationInstruction(taskEvents []task.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		taskEvent := taskEvents[index]
		if taskEvent.Name != "confirmation.requested" {
			continue
		}
		var request struct {
			ContinuationInstruction string `json:"continuationInstruction"`
		}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &request); errorValue != nil {
			continue
		}
		if instruction := strings.TrimSpace(request.ContinuationInstruction); instruction != "" {
			return instruction
		}
	}
	return ""
}

func (connectorRuntime *ConnectorRuntime) completeApprovedPendingTask(pendingTaskRun task.TaskRun, continuationTaskRunID string, finishMessage string) {
	result := strings.TrimSpace(finishMessage)
	if result == "" {
		result = "Approved and continued in task " + continuationTaskRunID + "."
	}
	connectorRuntime.taskRunService.AppendTaskEvent(pendingTaskRun.TaskRunID, "approval.continued", marshalConnectorEventBody(map[string]string{
		"continuationTaskRunID": continuationTaskRunID,
		"result":                result,
	}))
	_, _ = connectorRuntime.taskRunService.CompleteTaskRun(pendingTaskRun.TaskRunID, result)
}

func marshalConnectorEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return fmt.Sprint(value)
	}
	return string(document)
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

func (connectorRuntime *ConnectorRuntime) recordTaskWaitTokenForReply(platform string, event PlatformInboundEvent, replyTarget ReplyTarget, reply OutboundReply, dispatchID string) {
	if connectorRuntime.taskWaitTokenRepository == nil {
		return
	}
	taskRunID := strings.TrimSpace(reply.TaskRunID)
	if taskRunID == "" || reply.ReplyKind != connectorReplyKindUserNotice {
		return
	}
	taskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if !isFound || !taskRunCanContinueGoal(taskRun, connectorRuntime.taskRunService.ListTaskEvent(taskRunID)) {
		return
	}
	taskWaitToken := connectorRuntime.taskWaitTokenForReply(platform, event, replyTarget, reply, dispatchID, taskRun)
	if taskWaitToken.Kind == "" {
		return
	}
	if errorValue := connectorRuntime.taskWaitTokenRepository.InsertTaskWaitToken(taskWaitToken); errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "task.wait.persist_failed", connectorReplyEventBody(event, reply, "", dispatchID, errorValue.Error()))
		connectorRuntime.logger.Warn("connector."+platform+".wait.persist_failed", slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) taskWaitTokenForReply(platform string, event PlatformInboundEvent, replyTarget ReplyTarget, reply OutboundReply, dispatchID string, taskRun task.TaskRun) task.TaskWaitToken {
	now := time.Now().UTC()
	interactionID := replyInteractionID(reply, connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID))
	return task.TaskWaitToken{
		WaitID:         taskWaitID(taskRun.TaskRunID, interactionID, dispatchID),
		TaskRunID:      taskRun.TaskRunID,
		PersonID:       taskRun.RequesterPersonID,
		Platform:       strings.TrimSpace(platform),
		ConversationID: firstNonEmptyString(event.ConversationID, replyTarget.ConversationID, taskRun.OriginConversationID),
		ReplyTargetID:  firstNonEmptyString(dispatchID, replyTarget.ReplyTargetID, taskRun.OriginReplyTargetID),
		ThreadRootID:   firstNonEmptyString(eventThreadRootID(event), replyTarget.ReplyTargetID, taskRun.OriginReplyTargetID),
		DispatchID:     strings.TrimSpace(dispatchID),
		InteractionID:  interactionID,
		Kind:           taskWaitKind(reply, taskRun),
		State:          "open",
		ExpiresAt:      now.Add(24 * time.Hour),
		CreatedAt:      now,
	}
}

func taskWaitID(taskRunID string, interactionID string, dispatchID string) string {
	return strings.Join([]string{
		"wait",
		strings.TrimSpace(taskRunID),
		firstNonEmptyString(interactionID, "interaction"),
		firstNonEmptyString(dispatchID, "dispatch"),
	}, ":")
}

func replyInteractionID(reply OutboundReply, taskEvents []task.TaskEvent) string {
	if reply.Interaction != nil {
		return strings.TrimSpace(reply.Interaction.InteractionID)
	}
	return latestAskInteractionID(taskEvents)
}

func taskWaitKind(reply OutboundReply, taskRun task.TaskRun) string {
	if taskRun.Status == task.TaskStatusWaitingApproval {
		return "approval"
	}
	if reply.Interaction == nil {
		if taskRun.Status == task.TaskStatusWaitingUserInput {
			return "input"
		}
		return ""
	}
	switch normalizedAskInteractionKind(reply.Interaction.Kind) {
	case "ask_confirm":
		return "approval"
	case "ask_input":
		return "input"
	default:
		return ""
	}
}

func (connectorRuntime *ConnectorRuntime) appendConnectorReplyEvent(taskRunID string, name string, body map[string]string) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, name, marshalConnectorEventBody(body))
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
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.suppressed", connectorReplyEventBody(event, reply, "", "", "missing_checkpoint_message"))
		return errors.New("missing checkpoint message")
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, reply)
	if errorValue != nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.failed", connectorReplyEventBody(event, reply, "", "", errorValue.Error()))
		connectorRuntime.logger.Error("connector."+platform+".checkpoint.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return errorValue
	}
	if connectorRuntime.outboxRepository() == nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.sent", connectorReplyEventBody(event, reply, "", dispatchID, ""))
	}
	connectorRuntime.logger.Info("connector."+platform+".checkpoint.sent", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("replyDispatchID", dispatchID))
	return nil
}

func (connectorRuntime *ConnectorRuntime) sendUserNoticeReply(ctx context.Context, platform string, event PlatformInboundEvent, taskRunID string, replyTarget ReplyTarget, turnResult agentcontract.AgentTurnResult, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (string, bool) {
	notice, failureNotice, missingReason := userNoticeReplyMessage(turnResult)
	if missingReason != "" {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.suppressed", connectorReplyEventBody(event, OutboundReply{TaskRunID: taskRunID, ReplyKind: connectorReplyKindUserNotice}, "", "", missingReason))
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
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.failed", connectorReplyEventBody(event, reply, "", "", errorValue.Error()))
		connectorRuntime.logger.Error("connector."+platform+".outbound.failed", slog.String("messageID", event.MessageID), slog.String("taskRunID", taskRunID), slog.String("error", errorValue.Error()))
		return "", false
	}
	if connectorRuntime.outboxRepository() == nil {
		connectorRuntime.appendConnectorReplyEvent(taskRunID, "connector.reply.sent", connectorReplyEventBody(event, reply, "", dispatchID, ""))
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

// Only the triggering message's attachments are imported eagerly: what the
// person just handed over should be visible without a tool call. Everything
// older stays where it is and is read on demand by the url standing in its
// message — importing the whole visible window bought nothing but latency and
// still missed every message that had scrolled past it.
func (connectorRuntime *ConnectorRuntime) withAttachmentMaterials(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent, personID string) PlatformInboundEvent {
	attachments := connectorUniqueInputAttachments(event.Context.InputAttachments)
	if len(attachments) == 0 {
		return event
	}
	importingAdapter, isSupported := adapter.(InputAttachmentImportingAdapter)
	if !isSupported {
		event.Context.Materials = attachments
		return event
	}
	scope := connectorInputAttachmentScope(personID, event)
	targetDirectoryPath := connectorInputAttachmentDirectory(scope, event)
	result, errorValue := importingAdapter.ImportInputAttachments(ctx, InputAttachmentImportRequest{
		MessageID:           event.MessageID,
		TargetDirectoryPath: targetDirectoryPath,
		InputAttachments:    attachments,
	})
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+adapter.Name()+".attachments.import_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		refused := connectorRefusedInputAttachments(attachments, errorValue)
		event.Context.InputAttachments = refused
		event.Context.Materials = refused
		event.Context.Messages = connectorReplaceImportedMessageAttachments(event.Context.Messages, refused)
		return event
	}
	if len(result.InputAttachments) > 0 {
		writtenAttachments, writtenContents := connectorRuntime.attachmentWriterFor(personID).writeAll(ctx, result.InputAttachments)
		result.InputParts = connectorInputPartsAtWrittenPaths(result.InputParts, writtenAttachments)
		result.InputParts = connectorImagePartsShowingTheirBytes(result.InputParts, writtenContents)
		importedAttachments := connectorReadableInputAttachments(writtenAttachments, personID, scope)
		event.Context.InputAttachments = connectorReplaceImportedInputAttachments(event.Context.InputAttachments, importedAttachments)
		event.Context.Materials = connectorReplaceImportedInputAttachments(event.Context.Materials, importedAttachments)
		event.Context.Messages = connectorReplaceImportedMessageAttachments(event.Context.Messages, importedAttachments)
	}
	if len(result.InputParts) > 0 {
		readableInputParts := connectorReadableAgentParts(result.InputParts, personID, scope)
		event.InputParts = append(event.InputParts, connectorCurrentInputParts(readableInputParts, event)...)
	}
	return event
}

const connectorAttachmentImportRefusedCode = "attachment_import_failed"

// An attachment that could not be brought in is still handed to the agent, and
// without this it arrives looking ordinary: a file with a url the agent cannot
// open. It would then ask whoever sent it to attach the file again, which is
// the one thing they already did. It arrives refused, with the reason, so the
// agent says what went wrong and the ledger holds it.
func connectorRefusedInputAttachments(attachments []InputAttachment, errorValue error) []InputAttachment {
	refused := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		refused = append(refused, refusedInputAttachment(attachment, errorValue))
	}
	return refused
}

func (connectorRuntime *ConnectorRuntime) attachmentWriterFor(personID string) importedAttachmentWriter {
	return importedAttachmentWriter{workspaceActorFactory: connectorRuntime.workspaceActorFactory, personID: personID}
}

const mostInlineImageBytesShown = 8 << 20

// An image the message came with is put in front of the model with the message,
// whatever messenger it came over. This layer is the one owner of that: an
// importing adapter hands back bytes, the writer puts them in the person's
// workspace, and the picture travels with the prompt instead of costing a tool
// call to look at what was just sent.
func connectorImagePartsShowingTheirBytes(parts []agentcontract.AgentPart, contents writtenAttachmentContents) []agentcontract.AgentPart {
	result := make([]agentcontract.AgentPart, 0, len(parts))
	for _, part := range parts {
		if part.Image != nil && strings.TrimSpace(part.Image.DataBase64) == "" {
			if content, isHeld := contents[strings.TrimSpace(part.Image.Path)]; isHeld && len(content) <= mostInlineImageBytesShown {
				image := *part.Image
				image.DataBase64 = base64.StdEncoding.EncodeToString(content)
				part.Image = &image
			}
		}
		result = append(result, part)
	}
	return result
}

// A part names the same file the attachment does. Whatever name the file ended
// up under is the one the agent is told about.
func connectorInputPartsAtWrittenPaths(parts []agentcontract.AgentPart, writtenAttachments []InputAttachment) []agentcontract.AgentPart {
	pathByFilename := map[string]string{}
	for _, attachment := range writtenAttachments {
		if strings.TrimSpace(attachment.Path) == "" {
			continue
		}
		pathByFilename[attachment.Filename] = attachment.Path
	}
	result := make([]agentcontract.AgentPart, 0, len(parts))
	for _, part := range parts {
		if part.File != nil {
			file := *part.File
			file.Path = firstNonEmptyString(pathByFilename[file.Filename], file.Path)
			part.File = &file
		}
		if part.Image != nil {
			image := *part.Image
			image.Path = firstNonEmptyString(pathByFilename[image.Filename], image.Path)
			part.Image = &image
		}
		result = append(result, part)
	}
	return result
}

func connectorReplaceImportedMessageAttachments(messages []VisibleContextMessage, importedAttachments []InputAttachment) []VisibleContextMessage {
	result := make([]VisibleContextMessage, 0, len(messages))
	for _, message := range messages {
		message.InputAttachments = connectorReplaceImportedInputAttachments(message.InputAttachments, importedAttachments)
		result = append(result, message)
	}
	return result
}

func connectorReplaceImportedInputAttachments(attachments []InputAttachment, importedAttachments []InputAttachment) []InputAttachment {
	importedByKey := connectorImportedAttachmentByKey(importedAttachments)
	replacedAttachments := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		key := connectorInputAttachmentKey(attachment)
		if importedAttachment, isFound := importedByKey[key]; isFound {
			replacedAttachments = append(replacedAttachments, importedAttachment)
			continue
		}
		replacedAttachments = append(replacedAttachments, attachment)
	}
	return connectorUniqueInputAttachments(replacedAttachments)
}

func connectorImportedAttachmentByKey(importedAttachments []InputAttachment) map[string]InputAttachment {
	importedByKey := map[string]InputAttachment{}
	for _, attachment := range importedAttachments {
		key := connectorInputAttachmentKey(attachment)
		if key != "" {
			importedByKey[key] = attachment
		}
	}
	return importedByKey
}

func connectorCurrentInputParts(parts []agentcontract.AgentPart, event PlatformInboundEvent) []agentcontract.AgentPart {
	currentFileIDs := map[string]bool{}
	for _, attachment := range event.Context.InputAttachments {
		fileID := strings.TrimSpace(attachment.FileID)
		if fileID != "" {
			currentFileIDs[fileID] = true
		}
	}
	currentParts := []agentcontract.AgentPart{}
	for _, part := range parts {
		if connectorIsCurrentInputPart(part, event, currentFileIDs) {
			currentParts = append(currentParts, part)
		}
	}
	return currentParts
}

func connectorIsCurrentInputPart(part agentcontract.AgentPart, event PlatformInboundEvent, currentFileIDs map[string]bool) bool {
	if strings.TrimSpace(part.Source.MessageID) != "" && strings.TrimSpace(part.Source.MessageID) == strings.TrimSpace(event.MessageID) {
		return true
	}
	if strings.TrimSpace(part.Source.FileID) != "" && currentFileIDs[strings.TrimSpace(part.Source.FileID)] {
		return true
	}
	return false
}

func connectorInputAttachmentScope(personID string, event PlatformInboundEvent) agentruntime.ConversationResourceScope {
	return agentruntime.ConversationScopeForRequest(connectorWorkspaceRootPath, agentruntime.ToolCatalogRequest{
		RequesterPersonID:       personID,
		ConversationID:          event.ConversationID,
		ConversationType:        event.Context.ConversationType,
		ConversationChannelID:   event.Context.ChannelID,
		ConversationChannelName: event.Context.ChannelName,
	})
}

func connectorInputAttachmentDirectory(scope agentruntime.ConversationResourceScope, event PlatformInboundEvent) string {
	platform := connectorSafePathSegment(firstNonEmptyString(event.Platform, "platform"))
	conversationLabel := connectorAttachmentConversationLabel(event)
	return strings.TrimRight(scope.DefaultDirectoryPath, "/") + "/inbox/" + platform + "/" + conversationLabel
}

func connectorAttachmentConversationLabel(event PlatformInboundEvent) string {
	if channelName := strings.TrimSpace(event.Context.ChannelName); channelName != "" {
		return connectorSafePathSegment(channelName)
	}
	conversationID := strings.TrimSpace(event.ConversationID)
	if strings.HasPrefix(strings.ToLower(conversationID), "dm:") {
		return "dm"
	}
	return connectorSafePathSegment(firstNonEmptyString(conversationID, "conversation"))
}

func connectorReadableInputAttachments(attachments []InputAttachment, personID string, scope agentruntime.ConversationResourceScope) []InputAttachment {
	result := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		attachment.Path = connectorReadableAttachmentPath(attachment.Path, personID, scope)
		result = append(result, attachment)
	}
	return result
}

func connectorReadableAgentParts(parts []agentcontract.AgentPart, personID string, scope agentruntime.ConversationResourceScope) []agentcontract.AgentPart {
	result := make([]agentcontract.AgentPart, 0, len(parts))
	for _, part := range parts {
		if part.File != nil {
			file := *part.File
			file.Path = connectorReadableAttachmentPath(file.Path, personID, scope)
			part.File = &file
		}
		if part.Image != nil {
			image := *part.Image
			image.Path = connectorReadableAttachmentPath(image.Path, personID, scope)
			part.Image = &image
		}
		result = append(result, part)
	}
	return result
}

func connectorReadableAttachmentPath(path string, personID string, scope agentruntime.ConversationResourceScope) string {
	if scope.Kind != "private" || strings.TrimSpace(personID) == "" {
		return path
	}
	prefix := "/workspace/private/people/" + connectorSafePathSegment(personID)
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == prefix {
		return "~"
	}
	if strings.HasPrefix(trimmedPath, prefix+"/") {
		return "~/" + strings.TrimPrefix(trimmedPath, prefix+"/")
	}
	return path
}

func connectorUniqueInputAttachments(attachments []InputAttachment) []InputAttachment {
	seen := map[string]bool{}
	result := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		key := connectorInputAttachmentKey(attachment)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, attachment)
	}
	return result
}

func connectorInputAttachmentKey(attachment InputAttachment) string {
	if strings.TrimSpace(attachment.FileID) != "" {
		return strings.TrimSpace(attachment.Platform) + ":" + strings.TrimSpace(attachment.FileID)
	}
	if strings.TrimSpace(attachment.URL) != "" {
		return strings.TrimSpace(attachment.Platform) + ":" + strings.TrimSpace(attachment.URL)
	}
	if strings.TrimSpace(attachment.Path) != "" {
		return strings.TrimSpace(attachment.Platform) + ":" + strings.TrimSpace(attachment.Path)
	}
	return ""
}

type connectorAttachmentMaterialResolver struct {
	adapter          PlatformAdapter
	personID         string
	event            PlatformInboundEvent
	sentSources      *sentAttachmentSourceStore
	attachmentWriter importedAttachmentWriter
}

func (resolver connectorAttachmentMaterialResolver) ResolveAttachmentMaterial(ctx context.Context, materialID string) (agentcontract.VisibleContextMaterial, error) {
	attachment, isFound, errorValue := resolver.findAttachmentMaterial(ctx, materialID)
	if errorValue != nil {
		return agentcontract.VisibleContextMaterial{}, errorValue
	}
	if !isFound {
		return agentcontract.VisibleContextMaterial{}, errors.New("attachment material is not visible in this conversation")
	}
	return resolver.importAttachmentMaterial(ctx, attachment)
}

func (resolver connectorAttachmentMaterialResolver) sentSourceMaterial(attachment InputAttachment) (agentcontract.VisibleContextMaterial, bool) {
	devicePath, isFound := resolver.sentSources.SourcePath(attachment.Platform, attachment.MessageID, attachment.Filename)
	if !isFound {
		return agentcontract.VisibleContextMaterial{}, false
	}
	scope := connectorInputAttachmentScope(resolver.personID, resolver.event)
	readablePath := connectorReadableAttachmentPath(devicePath, resolver.personID, scope)
	if readablePath == devicePath {
		return agentcontract.VisibleContextMaterial{}, false
	}
	sourceAttachment := attachment
	sourceAttachment.Path = readablePath
	sourceAttachment.IsAvailable = true
	sourceAttachment.ErrorCode = ""
	sourceAttachment.Message = ""
	materials := agentVisibleContextMaterials([]InputAttachment{sourceAttachment})
	if len(materials) == 0 {
		return agentcontract.VisibleContextMaterial{}, false
	}
	return materials[0], true
}

const attachmentHistoryPageLimit = 40

func (resolver connectorAttachmentMaterialResolver) findAttachmentMaterial(ctx context.Context, materialID string) (InputAttachment, bool, error) {
	if attachment, isFound := findAttachmentMaterialInContext(resolver.event.Context, materialID); isFound {
		return attachment, true, nil
	}
	historyCursor := strings.TrimSpace(resolver.event.Context.HistoryCursor)
	if historyCursor == "" {
		return InputAttachment{}, false, nil
	}
	for pageCount := 0; pageCount < attachmentHistoryPageLimit; pageCount++ {
		visibleContext, errorValue := resolver.adapter.FetchHistory(ctx, historyCursor, 50)
		if errorValue != nil {
			return InputAttachment{}, false, errors.New("attachment history lookup failed: " + errorValue.Error())
		}
		if attachment, isFound := findAttachmentMaterialInContext(visibleContext, materialID); isFound {
			return attachment, true, nil
		}
		if !visibleContext.HasMoreBefore {
			return InputAttachment{}, false, nil
		}
		nextHistoryCursor := strings.TrimSpace(visibleContext.HistoryCursor)
		if nextHistoryCursor == "" || nextHistoryCursor == historyCursor {
			return InputAttachment{}, false, nil
		}
		historyCursor = nextHistoryCursor
	}
	return InputAttachment{}, false, errors.New("attachment lookup stopped after " + strconv.Itoa(attachmentHistoryPageLimit) + " pages; the conversation is longer than the resolver reads")
}

// A reference is a material ID, or the attachment's exact URL: the URL is the
// one name the model always holds, because it stands in the message text
// itself, long after the message has scrolled out of the visible window.
func findAttachmentMaterialInContext(visibleContext VisibleContext, reference string) (InputAttachment, bool) {
	trimmedReference := strings.TrimSpace(reference)
	if trimmedReference == "" {
		return InputAttachment{}, false
	}
	for _, attachment := range visibleContextAttachmentMaterials(visibleContext) {
		if attachmentMaterialID(attachment) == trimmedReference {
			return attachment, true
		}
		if strings.TrimSpace(attachment.URL) == trimmedReference {
			return attachment, true
		}
	}
	return InputAttachment{}, false
}

func visibleContextAttachmentMaterials(visibleContext VisibleContext) []InputAttachment {
	attachments := []InputAttachment{}
	attachments = append(attachments, visibleContext.Materials...)
	attachments = append(attachments, visibleContext.InputAttachments...)
	for _, message := range visibleContext.Messages {
		attachments = append(attachments, message.InputAttachments...)
	}
	return connectorUniqueInputAttachments(attachments)
}

func (resolver connectorAttachmentMaterialResolver) importAttachmentMaterial(ctx context.Context, attachment InputAttachment) (agentcontract.VisibleContextMaterial, error) {
	if material, isResolved := resolver.sentSourceMaterial(attachment); isResolved {
		return material, nil
	}
	importingAdapter, isSupported := resolver.adapter.(InputAttachmentImportingAdapter)
	if isSupported && strings.TrimSpace(attachment.FileID) != "" {
		material, errorValue := resolver.importAttachmentWithAdapter(ctx, importingAdapter, attachment)
		if errorValue == nil {
			return material, nil
		}
		if strings.TrimSpace(attachment.Path) == "" || strings.TrimSpace(attachment.ErrorCode) != "" {
			return agentcontract.VisibleContextMaterial{}, errorValue
		}
	}
	if strings.TrimSpace(attachment.Path) != "" && strings.TrimSpace(attachment.ErrorCode) == "" {
		return connectorAttachmentToAgentMaterial(resolver.personID, resolver.event, attachment), nil
	}
	if !isSupported {
		return agentcontract.VisibleContextMaterial{}, errors.New("attachment import is unavailable for this platform")
	}
	return resolver.importAttachmentWithAdapter(ctx, importingAdapter, attachment)
}

func (resolver connectorAttachmentMaterialResolver) importAttachmentWithAdapter(ctx context.Context, importingAdapter InputAttachmentImportingAdapter, attachment InputAttachment) (agentcontract.VisibleContextMaterial, error) {
	scope := connectorInputAttachmentScope(resolver.personID, resolver.event)
	messageID := firstNonEmptyString(attachment.MessageID, resolver.event.MessageID)
	result, errorValue := importingAdapter.ImportInputAttachments(ctx, InputAttachmentImportRequest{
		MessageID:           messageID,
		TargetDirectoryPath: connectorInputAttachmentDirectory(scope, resolver.event),
		InputAttachments:    []InputAttachment{attachment},
	})
	if errorValue != nil {
		return agentcontract.VisibleContextMaterial{}, errorValue
	}
	if len(result.InputAttachments) == 0 {
		return agentcontract.VisibleContextMaterial{}, errors.New("attachment import returned no material")
	}
	writtenAttachments, writtenContents := resolver.attachmentWriter.writeAll(ctx, result.InputAttachments)
	result.InputParts = connectorInputPartsAtWrittenPaths(result.InputParts, writtenAttachments)
	result.InputParts = connectorImagePartsShowingTheirBytes(result.InputParts, writtenContents)
	importedAttachment := connectorReadableInputAttachments(writtenAttachments, resolver.personID, scope)[0]
	if strings.TrimSpace(importedAttachment.Path) == "" {
		return agentcontract.VisibleContextMaterial{}, errors.New("attachment import returned no readable path")
	}
	material := agentVisibleContextMaterials([]InputAttachment{importedAttachment})[0]
	return connectorMaterialWithPreview(material, result.InputParts), nil
}

func connectorMaterialWithPreview(material agentcontract.VisibleContextMaterial, parts []agentcontract.AgentPart) agentcontract.VisibleContextMaterial {
	materialID := strings.TrimSpace(material.MaterialID)
	path := strings.TrimSpace(material.Path)
	for _, part := range parts {
		if part.File == nil {
			continue
		}
		if materialID != "" && connectorAgentPartMaterialID(part) != materialID {
			continue
		}
		if materialID == "" && path != "" && strings.TrimSpace(part.File.Path) != path {
			continue
		}
		material.MarkdownPreview = strings.TrimSpace(part.File.MarkdownPreview)
		material.ConversionStatus = strings.TrimSpace(part.File.ConversionStatus)
		material.ConversionMessage = strings.TrimSpace(part.File.ConversionMessage)
		return material
	}
	return material
}

func connectorAgentPartMaterialID(part agentcontract.AgentPart) string {
	fileID := strings.TrimSpace(part.Source.FileID)
	platform := firstNonEmptyString(strings.TrimSpace(part.Source.Platform), "attachment")
	if fileID != "" {
		return platform + ":" + fileID
	}
	if part.File != nil && strings.TrimSpace(part.File.Path) != "" {
		return platform + ":" + connectorSafePathSegment(part.File.Path)
	}
	return ""
}

func connectorAttachmentToAgentMaterial(personID string, event PlatformInboundEvent, attachment InputAttachment) agentcontract.VisibleContextMaterial {
	scope := connectorInputAttachmentScope(personID, event)
	readableAttachments := connectorReadableInputAttachments([]InputAttachment{attachment}, personID, scope)
	materials := agentVisibleContextMaterials(readableAttachments)
	if len(materials) == 0 {
		return agentcontract.VisibleContextMaterial{}
	}
	return materials[0]
}

func connectorSafePathSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	result := strings.Builder{}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			result.WriteRune(character)
			continue
		}
		result.WriteRune('-')
	}
	segment := strings.Trim(result.String(), "-_")
	if segment == "" {
		return "unknown"
	}
	return segment
}

func (connectorRuntime *ConnectorRuntime) Health() ConnectorRuntimeHealth {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	platforms := []string{}
	for platform := range connectorRuntime.adapterByPlatform {
		platforms = append(platforms, platform)
	}
	health := ConnectorRuntimeHealth{
		Started:                     connectorRuntime.started,
		HasEventRepository:          connectorRuntime.eventRepository != nil,
		HasQueueRepository:          connectorRuntime.queueRepository() != nil,
		HasOutboxRepository:         connectorRuntime.outboxRepository() != nil,
		RegisteredPlatforms:         platforms,
		MattermostAdapterRegistered: connectorRuntime.adapterByPlatform["mattermost"] != nil,
		InboxWorkerCount:            len(connectorRuntime.inboxHeartbeats),
		OutboxWorkerCount:           len(connectorRuntime.outboxHeartbeats),
		LastInboxHeartbeatAt:        latestTime(connectorRuntime.inboxHeartbeats),
		LastOutboxHeartbeatAt:       latestTime(connectorRuntime.outboxHeartbeats),
	}
	health.InboxWorkersAlive = connectorWorkersAlive(connectorRuntime.inboxHeartbeats, 2*connectorWorkerIdleDelay)
	health.OutboxWorkersAlive = connectorWorkersAlive(connectorRuntime.outboxHeartbeats, 2*connectorWorkerIdleDelay)
	health.Passed = health.Started &&
		health.HasEventRepository &&
		health.HasQueueRepository &&
		health.HasOutboxRepository &&
		health.MattermostAdapterRegistered &&
		health.InboxWorkersAlive &&
		health.OutboxWorkersAlive
	return health
}

func (connectorRuntime *ConnectorRuntime) prepareConnectorWorkers(kind string, count int) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	heartbeats := make([]time.Time, count)
	now := time.Now()
	for index := range heartbeats {
		heartbeats[index] = now
	}
	if kind == "inbox" {
		connectorRuntime.inboxHeartbeats = heartbeats
		return
	}
	connectorRuntime.outboxHeartbeats = heartbeats
}

func (connectorRuntime *ConnectorRuntime) recordConnectorWorkerHeartbeat(kind string, workerIndex int) {
	connectorRuntime.mutex.Lock()
	defer connectorRuntime.mutex.Unlock()

	now := time.Now()
	if kind == "inbox" && workerIndex >= 0 && workerIndex < len(connectorRuntime.inboxHeartbeats) {
		connectorRuntime.inboxHeartbeats[workerIndex] = now
		return
	}
	if kind == "outbox" && workerIndex >= 0 && workerIndex < len(connectorRuntime.outboxHeartbeats) {
		connectorRuntime.outboxHeartbeats[workerIndex] = now
	}
}

func (connectorRuntime *ConnectorRuntime) recordConnectorWorkerHeartbeatUntilStopped(ctx context.Context, kind string, workerIndex int) {
	ticker := time.NewTicker(connectorWorkerIdleDelay)
	defer ticker.Stop()
	for {
		connectorRuntime.recordConnectorWorkerHeartbeat(kind, workerIndex)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func connectorWorkersAlive(heartbeats []time.Time, maximumAge time.Duration) bool {
	if len(heartbeats) == 0 {
		return false
	}
	oldestAllowed := time.Now().Add(-maximumAge)
	for _, heartbeat := range heartbeats {
		if heartbeat.IsZero() || heartbeat.Before(oldestAllowed) {
			return false
		}
	}
	return true
}

func latestTime(values []time.Time) time.Time {
	var latest time.Time
	for _, value := range values {
		if value.After(latest) {
			latest = value
		}
	}
	return latest
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
		MemoryNamespaces:           connectorRuntime.accessibleNamespaces(personID, personAccess, event),
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

func marshalConnectorToolResult(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return fmt.Sprint(value)
	}
	return string(document)
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

func (connectorRuntime *ConnectorRuntime) accessibleNamespaces(personID string, personAccess policy.PersonAccess, event PlatformInboundEvent) []memory.MemoryNamespace {
	conversationSecurityLevelRank := personAccess.SecurityLevelRank
	conversationRequiredClasses := append([]string{}, personAccess.GrantedClasses...)
	if channelPolicy, isFound := connectorRuntime.identityService.ResolveConversationPolicy(event.Platform, event.ConversationID); isFound {
		conversationSecurityLevelRank = channelPolicy.DefaultSecurityLevelRank
		conversationRequiredClasses = append([]string{}, channelPolicy.DefaultRequiredClasses...)
	}
	namespaces := []memory.MemoryNamespace{
		memory.UserNamespace(personID),
		memory.PrivatePersonNamespace(personID),
		memory.WorkspaceNamespace(connectorRuntime.workspaceID, personAccess.SecurityLevelRank, personAccess.GrantedClasses),
	}
	if !isPrivateConversationID(event.ConversationID) {
		namespaces = append(namespaces, memory.ConversationNamespace(event.ConversationID, conversationSecurityLevelRank, conversationRequiredClasses))
	}
	for _, circleID := range personAccess.Circles {
		namespaces = append(namespaces, memory.CircleNamespace(connectorRuntime.workspaceID, circleID))
	}
	return namespaces
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

// askTheHostAboutUnknownAccount runs while a person waits on the answer, so a host that
// cannot say leaves this agent exactly as it was: the account stays unmatched and the
// refusal is the one it would have sent anyway.
//
// This sits at the one place a match fails rather than on each platform adapter. Whether
// a message arrived through chatd or through a capability is a routing detail, and an
// answer that depends on which door it came through is the same fact kept twice.
func (connectorRuntime *ConnectorRuntime) askTheHostAboutUnknownAccount(ctx context.Context, platform string, externalUserID string, messageID string, platformAccountIdentity identity.PlatformAccountIdentity) (string, bool, bool) {
	// A deployment with no resolver has no directory to ask, which is not a lookup
	// that failed. It refuses as it did before this existed.
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
		// The directory carries the address and this agent still cannot place it,
		// which is a projection that has not caught up rather than a stranger.
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

// While a turn runs, what it is doing is written into one message the person can
// watch. Nothing is registered when the platform cannot change a message it
// already sent, because a line per tool call would be a line per message.
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

func (connectorRuntime *ConnectorRuntime) queueRepository() ConnectorQueueRepository {
	queueRepository, isFound := connectorRuntime.eventRepository.(ConnectorQueueRepository)
	if !isFound {
		return nil
	}
	return queueRepository
}

func (connectorRuntime *ConnectorRuntime) outboxRepository() ConnectorOutboxRepository {
	outboxRepository, isFound := connectorRuntime.eventRepository.(ConnectorOutboxRepository)
	if !isFound {
		return nil
	}
	return outboxRepository
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

func (connectorRuntime *ConnectorRuntime) markQueuedConnectorEventFailed(queuedEvent QueuedConnectorEvent, errorValue error) {
	nextAttemptAt := nextConnectorAttemptAt(queuedEvent.AttemptCount)
	if markError := connectorRuntime.queueRepository().MarkConnectorEventFailed(queuedEvent, errorValue, nextAttemptAt); markError != nil {
		connectorRuntime.logger.Warn("connector."+queuedEvent.Event.Platform+".inbox.mark_failed_failed", slog.String("messageID", queuedEvent.Event.MessageID), slog.String("error", markError.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) markQueuedConnectorReplyFailed(queuedReply QueuedConnectorReply, errorValue error) {
	connectorRuntime.appendConnectorReplyEvent(queuedReply.Reply.TaskRunID, "connector.reply.failed", connectorReplyEventBody(PlatformInboundEvent{MessageID: queuedReply.RawEventID}, queuedReply.Reply, queuedReply.OutboxID, "", errorValue.Error()))
	nextAttemptAt := nextConnectorAttemptAt(queuedReply.AttemptCount)
	if markError := connectorRuntime.outboxRepository().MarkConnectorReplyFailed(queuedReply, errorValue, nextAttemptAt); markError != nil {
		connectorRuntime.logger.Warn("connector."+queuedReply.Platform+".outbox.mark_failed_failed", slog.String("outboxID", queuedReply.OutboxID), slog.String("error", markError.Error()))
	}
}

func nextConnectorAttemptAt(attemptCount int) time.Time {
	if attemptCount >= connectorMaximumAttemptCount {
		return time.Time{}
	}
	delaySeconds := 1 << max(0, min(attemptCount, 6))
	return time.Now().UTC().Add(time.Duration(delaySeconds) * time.Second)
}

func sleepConnectorWorker(ctx context.Context) {
	timer := time.NewTimer(connectorWorkerIdleDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

type connectorEventContextKey struct{}

func withConnectorEvent(ctx context.Context, event PlatformInboundEvent) context.Context {
	return context.WithValue(ctx, connectorEventContextKey{}, event)
}

func connectorEventFromContext(ctx context.Context) (PlatformInboundEvent, bool) {
	event, isFound := ctx.Value(connectorEventContextKey{}).(PlatformInboundEvent)
	return event, isFound
}

func (event PlatformInboundEvent) DedupeKey() string {
	messageID := strings.TrimSpace(event.MessageID)
	conversationID := strings.TrimSpace(event.ConversationID)
	return event.Platform + ":" + conversationID + ":" + messageID
}

func (connectorRuntime *ConnectorRuntime) suppressDuplicateSourceTaskIfNeeded(platform string, event PlatformInboundEvent, personID string) (ConnectorRuntimeResult, bool) {
	sourceReference := event.DedupeKey()
	taskRun, isFound := connectorRuntime.findTaskRunBySourceReference(personID, sourceReference)
	if !isFound {
		return ConnectorRuntimeResult{}, false
	}
	connectorRuntime.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "connector.duplicate_source_suppressed", marshalConnectorEventBody(map[string]string{
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
		if taskEvent.Name != "agent.task_source" && taskEvent.Name != "agent.task_launched" {
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

func (event *PlatformInboundEvent) UnmarshalJSON(document []byte) error {
	type platformInboundEvent PlatformInboundEvent
	var parsedEvent platformInboundEvent
	if errorValue := json.Unmarshal(document, &parsedEvent); errorValue != nil {
		return errorValue
	}

	var rawFields map[string]interface{}
	if errorValue := json.Unmarshal(document, &rawFields); errorValue == nil {
		if len(parsedEvent.LegacyFields) == 0 {
			parsedEvent.LegacyFields = rawFields
		}
	}

	if strings.TrimSpace(parsedEvent.Prompt) == "" {
		parsedEvent.Prompt = stringField(rawFields, "text")
	}
	if strings.TrimSpace(parsedEvent.SenderID) == "" {
		parsedEvent.SenderID = stringField(rawFields, "senderUserID")
	}

	*event = PlatformInboundEvent(parsedEvent)
	return nil
}

func (visibleContext VisibleContext) ToAgentVisibleContext() agentcontract.VisibleContext {
	messages := make([]agentcontract.VisibleContextMessage, 0, len(visibleContext.Messages))
	for _, message := range visibleContext.Messages {
		messages = append(messages, agentcontract.VisibleContextMessage{
			Speaker:            message.Speaker,
			SpeakerCallingName: message.SpeakerCallingName,
			SpeakerHandle:      message.SpeakerHandle,
			Text:               message.Text,
			SentAt:             message.SentAt,
			Materials:          agentVisibleContextMaterials(message.InputAttachments),
		})
	}

	currentMaterials := agentVisibleContextMaterials(visibleContext.InputAttachments)
	return agentcontract.VisibleContext{
		Messages:                   messages,
		MessagesOpenOtherExchanges: visibleContext.MessagesOpenOtherExchanges,
		CurrentMaterials:           currentMaterials,
		Materials:                  agentPreviousVisibleContextMaterials(visibleContext.Materials, currentMaterials),
		HasMoreBefore:              visibleContext.HasMoreBefore,
		HistoryCursor:              visibleContext.HistoryCursor,
		ResponseLanguage:           visibleContext.ResponseLanguage,
	}
}

func agentPreviousVisibleContextMaterials(attachments []InputAttachment, currentMaterials []agentcontract.VisibleContextMaterial) []agentcontract.VisibleContextMaterial {
	currentMaterialIDs := map[string]bool{}
	for _, material := range currentMaterials {
		currentMaterialID := strings.TrimSpace(material.MaterialID)
		if currentMaterialID != "" {
			currentMaterialIDs[currentMaterialID] = true
		}
	}
	materials := []agentcontract.VisibleContextMaterial{}
	for _, material := range agentVisibleContextMaterials(attachments) {
		if currentMaterialIDs[strings.TrimSpace(material.MaterialID)] {
			continue
		}
		materials = append(materials, material)
	}
	return materials
}

// An attachment that failed to come in stays in the catalog with its error, or
// the model is left with nothing but the url in the message text and invents a
// path from it. Only an attachment with no identity at all is dropped.
func agentVisibleContextMaterials(attachments []InputAttachment) []agentcontract.VisibleContextMaterial {
	materials := make([]agentcontract.VisibleContextMaterial, 0, len(attachments))
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.FileID) == "" && strings.TrimSpace(attachment.Path) == "" &&
			strings.TrimSpace(attachment.URL) == "" {
			continue
		}
		materials = append(materials, agentcontract.VisibleContextMaterial{
			MaterialID:  attachmentMaterialID(attachment),
			URL:         strings.TrimSpace(attachment.URL),
			FileID:      strings.TrimSpace(attachment.FileID),
			Platform:    attachment.Platform,
			MessageID:   attachment.MessageID,
			Filename:    attachment.Filename,
			ContentType: attachment.ContentType,
			SizeBytes:   attachment.SizeBytes,
			Path:        attachment.Path,
			IsAvailable: attachment.IsAvailable,
			ErrorCode:   attachment.ErrorCode,
			Message:     attachment.Message,
		})
	}
	return materials
}

func attachmentMaterialID(attachment InputAttachment) string {
	fileID := strings.TrimSpace(attachment.FileID)
	if fileID != "" {
		return firstNonEmptyString(strings.TrimSpace(attachment.Platform), "attachment") + ":" + fileID
	}
	return firstNonEmptyString(strings.TrimSpace(attachment.Platform), "attachment") + ":" + connectorSafePathSegment(firstNonEmptyString(attachment.Path, attachment.Filename, attachment.URL))
}

func responseLanguageForEvent(event PlatformInboundEvent) string {
	return toolcontract.ResolveResponseLanguage(event.ResponseLanguage, event.Context.ResponseLanguage)
}

func stringField(fields map[string]interface{}, name string) string {
	if fields == nil {
		return ""
	}
	value, isFound := fields[name]
	if !isFound {
		return ""
	}
	stringValue, isString := value.(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(stringValue)
}

// The person chose to approve the whole family for this task, so the scope the
// pending question named is recorded once and the agent stops asking for it.
func (connectorRuntime *ConnectorRuntime) grantApprovalScopeForTask(taskRunID string) {
	scope := pendingApprovalScope(connectorRuntime.taskRunService.ListTaskEvent(taskRunID))
	if scope == "" {
		return
	}
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, "approval.scope_granted", marshalConnectorEventBody(map[string]string{"scope": scope}))
}

func pendingApprovalScope(taskEvents []taskstate.TaskEvent) string {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		if taskEvents[index].Name != "ask.requested" {
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
