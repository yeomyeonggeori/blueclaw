package acpsession

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
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
}

type Agent struct {
	taskLauncher    TaskLauncher
	directory       PersonDirectory
	permissionRelay *PermissionRelay
	turnRouter      TurnRouter
	logger          *slog.Logger

	connection *acp.AgentSideConnection
	mutex      sync.RWMutex
	sessions   map[acp.SessionId]openSession
}

func NewAgent(taskLauncher TaskLauncher, directory PersonDirectory, permissionRelay *PermissionRelay, turnRouter TurnRouter, logger *slog.Logger) *Agent {
	return &Agent{
		taskLauncher:    taskLauncher,
		directory:       directory,
		permissionRelay: permissionRelay,
		turnRouter:      turnRouter,
		logger:          logger,
		sessions:        map[acp.SessionId]openSession{},
	}
}

func (agent *Agent) UseConnection(connection *acp.AgentSideConnection) {
	agent.connection = connection
}

func (agent *Agent) Initialize(_ context.Context, request acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion:   request.ProtocolVersion,
		AgentInfo:         &acp.Implementation{Name: "blueclaw", Version: "1"},
		AgentCapabilities: acp.AgentCapabilities{McpCapabilities: acp.McpCapabilities{Http: true}},
		AuthMethods:       []acp.AuthMethod{},
	}, nil
}

func (agent *Agent) NewSession(_ context.Context, request acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	sessionContext, errorValue := SessionContextFromMeta(request.Meta)
	if errorValue != nil {
		return acp.NewSessionResponse{}, errorValue
	}
	sessionContext.Requester.PersonID, errorValue = agent.resolveRequesterPersonID(sessionContext.Requester)
	if errorValue != nil {
		return acp.NewSessionResponse{}, errorValue
	}
	sessionID := acp.SessionId(newSessionIdentifier())
	agent.mutex.Lock()
	agent.sessions[sessionID] = openSession{context: sessionContext, workspaceRootPath: request.Cwd}
	agent.mutex.Unlock()
	agent.permissionRelay.hold(sessionContext, sessionID, agent.connection)
	agent.logger.Info("acpsession.opened",
		"sessionID", string(sessionID),
		"personID", sessionContext.Requester.PersonID,
		"platform", sessionContext.Addressing.Platform,
		"conversationID", sessionContext.Addressing.ConversationID,
	)
	return acp.NewSessionResponse{SessionId: sessionID}, nil
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
	launchResult, errorValue := agent.taskLauncher.Launch(ctx, agent.taskLaunchRequestFor(session, request.SessionId, prompt))
	if errorValue != nil {
		return acp.PromptResponse{}, errorValue
	}
	agent.sendReply(ctx, request.SessionId, launchResult.TurnResult)
	return acp.PromptResponse{StopReason: stopReasonForTaskStatus(launchResult.TurnResult.TaskRun.Status)}, nil
}

func (agent *Agent) session(sessionID acp.SessionId) (openSession, bool) {
	agent.mutex.RLock()
	defer agent.mutex.RUnlock()
	session, isOpen := agent.sessions[sessionID]
	return session, isOpen
}

func (agent *Agent) taskLaunchRequestFor(session openSession, sessionID acp.SessionId, prompt string) agentruntime.TaskLaunchRequest {
	requester := session.context.Requester
	addressing := session.context.Addressing
	return agentruntime.TaskLaunchRequest{
		Source:               agentruntime.TaskLaunchSourceConnector,
		SourceReference:      "acp:" + string(sessionID),
		RequesterPersonID:    requester.PersonID,
		RequesterName:        agent.requesterName(requester),
		RequesterCallingName: requester.CallingName,
		RequesterHandle:      requester.Handle,
		RequesterEmail:       requester.Email,
		OriginReplyTargetID:  addressing.ReplyTargetID,
		OriginIsThread:       addressing.IsThread,
		ProfileName:          "default",
		Platform:             addressing.Platform,
		ConversationID:       addressing.ConversationID,
		ConversationType:     addressing.ConversationType,
		ReplyTargetID:        addressing.ReplyTargetID,
		Prompt:               prompt,
		ResponseLanguage:     addressing.ResponseLanguage,
		PersonAccess:         agent.directory.ResolvePersonAccess(requester.PersonID),
		CheckpointSender:     agent.checkpointSenderFor(sessionID),
	}
}

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
