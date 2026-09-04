package acpsession

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
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
}

type PersonDirectory interface {
	ResolvePersonIDByEmail(email string) (string, bool)
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
	taskLauncher    TaskLauncher
	directory       PersonDirectory
	permissionRelay *PermissionRelay
	turnRouter      TurnRouter
	engagementGate  EngagementGate
	taskRunStore    taskstate.TaskRunStore
	logger          *slog.Logger

	connection *acp.AgentSideConnection
	mutex      sync.RWMutex
	sessions   map[acp.SessionId]openSession
}

type EngagementGate interface {
	Resolve(ctx context.Context, platform string, request inboundengagement.Request) inboundengagement.Decision
}

func NewAgent(collaborators Collaborators, permissionRelay *PermissionRelay, logger *slog.Logger) *Agent {
	return &Agent{
		taskLauncher:    collaborators.TaskLauncher,
		directory:       collaborators.Directory,
		permissionRelay: permissionRelay,
		turnRouter:      collaborators.TurnRouter,
		engagementGate:  collaborators.EngagementGate,
		taskRunStore:    collaborators.TaskRunStore,
		logger:          logger,
		sessions:        map[acp.SessionId]openSession{},
	}
}

type Collaborators struct {
	TaskLauncher   TaskLauncher
	Directory      PersonDirectory
	TurnRouter     TurnRouter
	EngagementGate EngagementGate
	TaskRunStore   taskstate.TaskRunStore
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

func (agent *Agent) NewSession(_ context.Context, request acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	sessionID := acp.SessionId(newSessionIdentifier())
	if _, errorValue := agent.openSessionAs(sessionID, request.Meta, request.Cwd, request.McpServers); errorValue != nil {
		return acp.NewSessionResponse{}, errorValue
	}
	return acp.NewSessionResponse{SessionId: sessionID}, nil
}

func (agent *Agent) LoadSession(ctx context.Context, request acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	sessionContext, errorValue := agent.openSessionAs(request.SessionId, request.Meta, request.Cwd, request.McpServers)
	if errorValue != nil {
		return acp.LoadSessionResponse{}, errorValue
	}
	// A caller is blocked until this returns (agentclientprotocol.com/protocol/session-setup).
	go agent.reissueHeldPermissions(context.WithoutCancel(ctx), request.SessionId, sessionContext)
	return acp.LoadSessionResponse{}, nil
}

func (agent *Agent) openSessionAs(sessionID acp.SessionId, meta map[string]any, workspaceRootPath string, mcpServers []acp.McpServer) (SessionContext, error) {
	sessionContext, errorValue := SessionContextFromMeta(meta)
	if errorValue != nil {
		return SessionContext{}, errorValue
	}
	sessionContext.Requester.PersonID, errorValue = agent.resolveRequesterPersonID(sessionContext.Requester)
	if errorValue != nil {
		return SessionContext{}, errorValue
	}
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
	return sessionContext, nil
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
	if reason := agent.reasonToLeaveItAlone(ctx, session, messageContext, prompt); reason != "" {
		agent.logger.Info("acpsession.prompt.ignored",
			"sessionID", string(request.SessionId),
			"messageID", messageContext.MessageID,
			"reason", reason,
		)
		return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}
	launchResult, errorValue := agent.taskLauncher.Launch(ctx, agent.taskLaunchRequestFor(session, request.SessionId, prompt, messageContext))
	if errorValue != nil {
		return acp.PromptResponse{}, errorValue
	}
	agent.sendReply(ctx, request.SessionId, launchResult.TurnResult)
	return acp.PromptResponse{StopReason: stopReasonForTaskStatus(launchResult.TurnResult.TaskRun.Status)}, nil
}

func (agent *Agent) reasonToLeaveItAlone(ctx context.Context, session openSession, messageContext MessageContext, prompt string) string {
	if agent.engagementGate == nil {
		return ""
	}
	decision := agent.engagementGate.Resolve(ctx, session.context.Addressing.Platform, inboundengagement.Request{
		Prompt:           prompt,
		MessageID:        messageContext.MessageID,
		ConversationType: messageContext.conversationType(session.context.Addressing),
		BotMentioned:     messageContext.Context.Addressing.BotMentioned,
		AttachmentsOnly:  messageContext.Context.AttachmentsOnly,
		SenderName:       messageContext.Context.Sender.Name,
		SenderHandle:     messageContext.Context.Sender.Handle,
		VisibleContext:   messageContext.Context.ToAgentVisibleContext(),
	})
	if decision.ShouldLaunch {
		return ""
	}
	return decision.IgnoreReason
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
	}
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
