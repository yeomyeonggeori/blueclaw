package acpsession

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

var (
	errSessionNamesNobody           = errors.New("an acp session names the person it acts for on _meta " + SessionMetaKey + ", and this one names nobody")
	errSessionNamesNoConversation   = errors.New("an acp session names the conversation it answers in on _meta " + SessionMetaKey + ", and this one names none")
	errSessionRequesterIsNotKnown   = errors.New("this company knows nobody by that address, so there is no requester to run tools as")
	errSessionIsNotOpen             = errors.New("no session by that id is open on this connection")
	errPromptCarriesNothingToAnswer = errors.New("a prompt with no text is nothing to answer")
)

type TaskLauncher interface {
	Launch(context.Context, agentruntime.TaskLaunchRequest) (agentruntime.TaskLaunchResult, error)
	RouterRequest(agentruntime.TaskLaunchRequest) agentcontract.AgentRequest
}

type AttachmentImporter interface {
	ImportMessageAttachments(context.Context, connectors.PlatformInboundEvent, string) (connectors.PlatformInboundEvent, agentruntime.AttachmentMaterialResolver)
}

type PersonDirectory interface {
	ResolvePersonIDByEmail(email string) (string, bool)
	AwaitPersonIDByEmail(ctx context.Context, email string) (string, error)
	ResolvePersonDisplayName(personID string) string
	ResolvePersonAccess(personID string) policy.PersonAccess
}

type openSession struct {
	context           SessionContext
	workspaceRootPath string
	recordCatalog     *mcp.RecordCatalog
}

// A nil pointer put in an interface is not a nil interface, and the caller
// checks the interface.
func (session openSession) catalog() agentruntime.RecordCatalogClient {
	if session.recordCatalog == nil {
		return nil
	}
	return session.recordCatalog
}

type Agent struct {
	taskLauncher       TaskLauncher
	directory          PersonDirectory
	permissionRelay    *PermissionRelay
	approvalDeferrer   ApprovalDeferrer
	turnRouter         TurnRouter
	intakeDecider      IntakeDecider
	attachmentImporter AttachmentImporter
	taskRunStore       taskstate.TaskRunStore
	logger             *slog.Logger

	connection *acp.AgentSideConnection
	mutex      sync.RWMutex
	sessions   map[acp.SessionId]openSession
}

func NewAgent(collaborators Collaborators, permissionRelay *PermissionRelay, logger *slog.Logger) *Agent {
	return &Agent{
		taskLauncher:       collaborators.TaskLauncher,
		directory:          collaborators.Directory,
		permissionRelay:    permissionRelay,
		approvalDeferrer:   collaborators.ApprovalDeferrer,
		turnRouter:         collaborators.TurnRouter,
		intakeDecider:      collaborators.IntakeDecider,
		attachmentImporter: collaborators.AttachmentImporter,
		taskRunStore:       collaborators.TaskRunStore,
		logger:             logger,
		sessions:           map[acp.SessionId]openSession{},
	}
}

type ApprovalDeferrer interface {
	DeferApprovedCall(context.Context, approvalgate.DeferralRequest) (toolcontract.ToolResult, error)
}

type Collaborators struct {
	ApprovalDeferrer   ApprovalDeferrer
	TaskLauncher       TaskLauncher
	Directory          PersonDirectory
	TurnRouter         TurnRouter
	IntakeDecider      IntakeDecider
	AttachmentImporter AttachmentImporter
	TaskRunStore       taskstate.TaskRunStore
}

func (agent *Agent) UseConnection(connection *acp.AgentSideConnection) {
	agent.connection = connection
}

func (agent *Agent) Initialize(_ context.Context, request acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion:   request.ProtocolVersion,
		AgentInfo:         &acp.Implementation{Name: "blueclaw", Version: "1"},
		AgentCapabilities: acp.AgentCapabilities{LoadSession: true, McpCapabilities: acp.McpCapabilities{Http: true}},
		AuthMethods:       []acp.AuthMethod{},
	}, nil
}

func (agent *Agent) NewSession(ctx context.Context, request acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	sessionContext, errorValue := SessionContextFromMeta(request.Meta)
	if errorValue != nil {
		return acp.NewSessionResponse{}, errorValue
	}
	sessionContext.Requester.PersonID, errorValue = agent.awaitRequesterPersonID(ctx, sessionContext.Requester)
	if errorValue != nil {
		return acp.NewSessionResponse{}, errorValue
	}
	sessionID := acp.SessionId(newSessionIdentifier())
	agent.openSession(sessionID, sessionContext, request.Cwd, request.McpServers)
	return acp.NewSessionResponse{SessionId: sessionID}, nil
}

func (agent *Agent) LoadSession(ctx context.Context, request acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	sessionContext, errorValue := SessionContextFromMeta(request.Meta)
	if errorValue != nil {
		return acp.LoadSessionResponse{}, errorValue
	}
	sessionContext.Requester.PersonID, errorValue = agent.resolveRequesterPersonID(sessionContext.Requester)
	if errorValue != nil {
		return acp.LoadSessionResponse{}, errorValue
	}
	agent.openSession(request.SessionId, sessionContext, request.Cwd, request.McpServers)
	// A caller is blocked until this returns (agentclientprotocol.com/protocol/session-setup).
	go agent.reissueHeldPermissions(context.WithoutCancel(ctx), request.SessionId, sessionContext)
	return acp.LoadSessionResponse{}, nil
}

func (agent *Agent) openSession(sessionID acp.SessionId, sessionContext SessionContext, workspaceRootPath string, mcpServers []acp.McpServer) {
	recordCatalog := mcp.NewRecordCatalog(recordCatalogAddressOf(mcpServers))
	agent.mutex.Lock()
	replaced := agent.sessions[sessionID]
	agent.sessions[sessionID] = openSession{
		context:           sessionContext,
		workspaceRootPath: workspaceRootPath,
		recordCatalog:     recordCatalog,
	}
	agent.mutex.Unlock()
	closeRecordCatalog(replaced.recordCatalog)
	agent.permissionRelay.hold(sessionContext, sessionID, agent.connection)
	agent.logger.Info("acpsession.opened",
		"sessionID", string(sessionID),
		"personID", sessionContext.Requester.PersonID,
		"platform", sessionContext.Addressing.Platform,
		"conversationID", sessionContext.Addressing.ConversationID,
	)
}

func (agent *Agent) resolveRequesterPersonID(requester Requester) (string, error) {
	if requester.PersonID != "" {
		return requester.PersonID, nil
	}
	personID, isKnown := agent.directory.ResolvePersonIDByEmail(requester.Email)
	if !isKnown {
		return "", errSessionRequesterIsNotKnown
	}
	return personID, nil
}

func (agent *Agent) awaitRequesterPersonID(ctx context.Context, requester Requester) (string, error) {
	personID, errorValue := agent.resolveRequesterPersonID(requester)
	if errorValue == nil {
		return personID, nil
	}
	agent.logger.Info("acpsession.requester.awaited", "email", requester.Email)
	personID, errorValue = agent.directory.AwaitPersonIDByEmail(ctx, requester.Email)
	if errorValue != nil {
		return "", fmt.Errorf("%w; the session stopped waiting for the roster to name them: %w", errSessionRequesterIsNotKnown, errorValue)
	}
	agent.logger.Info("acpsession.requester.arrived", "email", requester.Email, "personID", personID)
	return personID, nil
}

func (agent *Agent) Prompt(ctx context.Context, request acp.PromptRequest) (acp.PromptResponse, error) {
	session, isOpen := agent.session(request.SessionId)
	if !isOpen {
		return acp.PromptResponse{}, errSessionIsNotOpen
	}
	prompt := promptText(request.Prompt)
	if prompt == "" {
		return acp.PromptResponse{}, errPromptCarriesNothingToAnswer
	}
	messageContext := MessageContextFromMeta(request.Meta)
	launchRequest, decided, reason := agent.decideOnce(ctx, session, messageContext, agent.taskLaunchRequestFor(session, request.SessionId, prompt, messageContext))
	if reason != "" {
		agent.logger.Info("acpsession.prompt.ignored",
			"sessionID", string(request.SessionId),
			"messageID", messageContext.MessageID,
			"reason", reason,
		)
		return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}
	launchResult, errorValue := agent.taskLauncher.Launch(ctx, agent.withMessageAttachments(ctx, messageContext, launchRequest))
	if errorValue != nil {
		return acp.PromptResponse{}, errorValue
	}
	agent.recordDecisionCalls(decided, launchResult.TurnResult.TaskRun.TaskRunID)
	agent.sendReply(ctx, request.SessionId, launchResult.TurnResult)
	return acp.PromptResponse{StopReason: stopReasonForTaskStatus(launchResult.TurnResult.TaskRun.Status)}, nil
}

func (agent *Agent) session(sessionID acp.SessionId) (openSession, bool) {
	agent.mutex.RLock()
	defer agent.mutex.RUnlock()
	session, isOpen := agent.sessions[sessionID]
	return session, isOpen
}

func (agent *Agent) taskLaunchRequestFor(session openSession, sessionID acp.SessionId, prompt string, messageContext MessageContext) agentruntime.TaskLaunchRequest {
	requester := session.context.Requester
	addressing := session.context.Addressing
	replyTargetID := messageContext.replyTargetID(addressing)
	return agentruntime.TaskLaunchRequest{
		Source:                  agentruntime.TaskLaunchSourceConnector,
		SourceReference:         "acp:" + string(sessionID),
		RequesterPersonID:       requester.PersonID,
		RequesterName:           agent.requesterName(requester),
		RequesterCallingName:    requester.CallingName,
		RequesterHandle:         requester.Handle,
		RequesterEmail:          requester.Email,
		RecordCatalog:           session.catalog(),
		OriginReplyTargetID:     replyTargetID,
		OriginIsThread:          addressing.IsThread || messageContext.IsThread,
		ProfileName:             defaultProfileName,
		Platform:                addressing.Platform,
		ConversationID:          addressing.ConversationID,
		ConversationType:        messageContext.conversationType(addressing),
		ConversationChannelID:   messageContext.Context.ChannelID,
		ConversationChannelName: messageContext.Context.ChannelName,
		ReplyTargetID:           replyTargetID,
		Prompt:                  prompt,
		ResponseLanguage:        messageContext.responseLanguage(addressing),
		VisibleContext:          messageContext.Context.ToAgentVisibleContext(),
		PersonAccess:            agent.directory.ResolvePersonAccess(requester.PersonID),
		CheckpointSender:        agent.checkpointSenderFor(sessionID),
		TurnStartedAt:           time.Now(),
	}
}

func (agent *Agent) withMessageAttachments(ctx context.Context, messageContext MessageContext, launchRequest agentruntime.TaskLaunchRequest) agentruntime.TaskLaunchRequest {
	if agent.attachmentImporter == nil {
		return launchRequest
	}
	imported, resolver := agent.attachmentImporter.ImportMessageAttachments(ctx, inboundEventOf(messageContext, launchRequest), launchRequest.RequesterPersonID)
	launchRequest.InputParts = imported.InputParts
	launchRequest.VisibleContext = imported.Context.ToAgentVisibleContext()
	launchRequest.AttachmentMaterialResolver = resolver
	return launchRequest
}

func inboundEventOf(messageContext MessageContext, launchRequest agentruntime.TaskLaunchRequest) connectors.PlatformInboundEvent {
	visibleContext := messageContext.Context
	visibleContext.ConversationType = launchRequest.ConversationType
	return connectors.PlatformInboundEvent{
		Platform:       launchRequest.Platform,
		ConversationID: launchRequest.ConversationID,
		MessageID:      messageContext.MessageID,
		ReplyTargetID:  launchRequest.ReplyTargetID,
		Prompt:         launchRequest.Prompt,
		Context:        visibleContext,
	}
}

// The company's catalog is named by whoever opened the session. A turn that
// arrived any other way is given none, and answers from what capabilityd
// offers.
func recordCatalogAddressOf(mcpServers []acp.McpServer) mcp.RecordCatalogAddress {
	for _, server := range mcpServers {
		if server.Http == nil {
			continue
		}
		headers := map[string]string{}
		for _, header := range server.Http.Headers {
			headers[header.Name] = header.Value
		}
		return mcp.RecordCatalogAddress{Name: server.Http.Name, URL: server.Http.Url, Headers: headers}
	}
	return mcp.RecordCatalogAddress{}
}

func closeRecordCatalog(recordCatalog *mcp.RecordCatalog) {
	if recordCatalog == nil {
		return
	}
	_ = recordCatalog.Close()
}

const defaultProfileName = "default"

func (agent *Agent) requesterName(requester Requester) string {
	if name := strings.TrimSpace(requester.Name); name != "" {
		return name
	}
	return agent.directory.ResolvePersonDisplayName(requester.PersonID)
}

// A client concatenates agent_message_chunk into the answer and renders
// agent_thought_chunk as progress (agentclientprotocol.com/protocol/prompt-turn).
func (agent *Agent) checkpointSenderFor(sessionID acp.SessionId) agentcontract.AgentCheckpointSender {
	return func(checkpointContext context.Context, checkpoint agentcontract.AgentCheckpoint) error {
		if message := strings.TrimSpace(checkpoint.Message); message != "" {
			if errorValue := agent.notify(checkpointContext, sessionID, acp.UpdateAgentThoughtText(message)); errorValue != nil {
				return errorValue
			}
		}
		if errorValue := agent.notifyAttachments(checkpointContext, sessionID, checkpoint.Attachments); errorValue != nil {
			return errorValue
		}
		toolName := strings.TrimSpace(checkpoint.ToolName)
		if toolName == "" {
			return nil
		}
		return agent.notify(checkpointContext, sessionID, acp.StartToolCall(acp.ToolCallId("run-"+toolName), toolName))
	}
}

func (agent *Agent) sendReply(ctx context.Context, sessionID acp.SessionId, turnResult agentcontract.AgentTurnResult) {
	reply := strings.TrimSpace(turnResult.FinishMessage)
	if reply == "" {
		reply = strings.TrimSpace(turnResult.UserNotice)
	}
	if reply == "" || turnResult.ReplySuppressed {
		return
	}
	if errorValue := agent.notify(ctx, sessionID, acp.UpdateAgentMessageText(reply)); errorValue != nil {
		agent.logger.Warn("acpsession.reply.undelivered", "sessionID", string(sessionID), "error", errorValue.Error())
		return
	}
	if errorValue := agent.notifyAttachments(ctx, sessionID, turnResult.Attachments); errorValue != nil {
		agent.logger.Warn("acpsession.attachments.undelivered", "sessionID", string(sessionID), "error", errorValue.Error())
	}
}

func (agent *Agent) notifyAttachments(ctx context.Context, sessionID acp.SessionId, attachments []toolcontract.FileAttachment) error {
	for _, attachment := range attachments {
		devicePath := strings.TrimSpace(attachment.DevicePath)
		if devicePath == "" {
			continue
		}
		name := strings.TrimSpace(attachment.Filename)
		if name == "" {
			name = filepath.Base(devicePath)
		}
		if errorValue := agent.notify(ctx, sessionID, acp.UpdateAgentMessage(acp.ResourceLinkBlock(name, "file://"+devicePath))); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func (agent *Agent) notify(ctx context.Context, sessionID acp.SessionId, update acp.SessionUpdate) error {
	return agent.connection.SessionUpdate(ctx, acp.SessionNotification{SessionId: sessionID, Update: update})
}

func promptText(blocks []acp.ContentBlock) string {
	segments := []string{}
	for _, block := range blocks {
		if block.Text == nil {
			continue
		}
		if text := strings.TrimSpace(block.Text.Text); text != "" {
			segments = append(segments, text)
		}
	}
	return strings.Join(segments, "\n")
}

func stopReasonForTaskStatus(status agentcontract.TaskStatus) acp.StopReason {
	if status == agentcontract.TaskStatusCancelled {
		return acp.StopReasonCancelled
	}
	return acp.StopReasonEndTurn
}

func (agent *Agent) CloseSession(_ context.Context, request acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	agent.mutex.Lock()
	session, isOpen := agent.sessions[request.SessionId]
	delete(agent.sessions, request.SessionId)
	agent.mutex.Unlock()
	if isOpen {
		agent.permissionRelay.release(session.context)
	}
	return acp.CloseSessionResponse{}, nil
}

func (agent *Agent) closeEverySession() {
	agent.mutex.Lock()
	sessions := agent.sessions
	agent.sessions = map[acp.SessionId]openSession{}
	agent.mutex.Unlock()
	for _, session := range sessions {
		agent.permissionRelay.release(session.context)
	}
}

func (agent *Agent) Cancel(context.Context, acp.CancelNotification) error { return nil }

func (agent *Agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (agent *Agent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (agent *Agent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}

func (agent *Agent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

func (agent *Agent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

func (agent *Agent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}
