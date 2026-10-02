package e2e

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/acpsession"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

const virtualACPRequesterEmail = "sample@example.com"

type virtualACPSession struct {
	connection *acp.ClientSideConnection
	client     *virtualACPClient
	sessionID  acp.SessionId
	close      func()
}

func openVirtualACPSession(collaborators acpsession.Collaborators, conversationID string) (*virtualACPSession, error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	agentSide, clientSide := net.Pipe()
	agent := acpsession.NewAgent(collaborators, acpsession.NewPermissionRelay(logger), logger)
	agentConnection := acp.NewAgentSideConnection(agent, agentSide, agentSide)
	agent.UseConnection(agentConnection)
	client := &virtualACPClient{}
	connection := acp.NewClientSideConnection(client, clientSide, clientSide)
	session := &virtualACPSession{
		connection: connection,
		client:     client,
		close: func() {
			agentSide.Close()
			clientSide.Close()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, errorValue := connection.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); errorValue != nil {
		session.close()
		return nil, errorValue
	}
	opened, errorValue := connection.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        "/workspace",
		McpServers: []acp.McpServer{},
		Meta: map[string]any{acpsession.SessionMetaKey: acpsession.SessionContext{
			Requester:  acpsession.Requester{Email: virtualACPRequesterEmail},
			Addressing: acpsession.Addressing{Platform: "virtual", ConversationID: conversationID},
		}},
	})
	if errorValue != nil {
		session.close()
		return nil, errorValue
	}
	session.sessionID = opened.SessionId
	return session, nil
}

func (session *virtualACPSession) prompt(ctx context.Context, event connectors.PlatformInboundEvent) (string, error) {
	messageCountBefore := session.client.messageCount()
	isThread := event.IsThread != nil && *event.IsThread
	_, errorValue := session.connection.Prompt(ctx, acp.PromptRequest{
		SessionId: session.sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock(event.Prompt)},
		Meta: map[string]any{acpsession.MessageMetaKey: acpsession.MessageContext{
			MessageID:     event.MessageID,
			ReplyTargetID: event.ReplyTargetID,
			IsThread:      isThread,
			Context:       event.Context,
		}},
	})
	if errorValue != nil {
		return "", errorValue
	}
	return session.client.messagesSince(messageCountBefore), nil
}

func (harness *VirtualSessionHarness) runTurnOverACP(ctx context.Context, event connectors.PlatformInboundEvent, observedTurnResult func() VirtualTurnResult) (VirtualTurnResult, error) {
	updatedBefore := time.Now()
	reply, errorValue := harness.acpSession.prompt(ctx, event)
	if errorValue != nil {
		return VirtualTurnResult{}, errorValue
	}
	turnResult := observedTurnResult()
	turnResult.Handled = true
	taskRun, isFound := harness.latestTaskRunUpdatedSince(updatedBefore)
	if !isFound {
		return VirtualTurnResult{}, errors.New("the acp turn touched no task run")
	}
	turnResult.TaskRunID = taskRun.TaskRunID
	turnResult.TaskStatus = taskRun.Status
	turnResult.FailureReason = taskRun.FailureReason
	turnResult.Events = harness.taskEventService.ListTaskEvent(taskRun.TaskRunID)
	if strings.TrimSpace(reply) == "" {
		return turnResult, nil
	}
	turnResult.DidReply = true
	turnResult.FinishMessage = reply
	turnResult.ReplyTargetID = event.ReplyTargetID
	return turnResult, nil
}

func (harness *VirtualSessionHarness) latestTaskRunUpdatedSince(since time.Time) (task.TaskRun, bool) {
	var latest task.TaskRun
	isFound := false
	for _, taskRun := range harness.taskRunService.ListTaskRunByPersonID(virtualRequesterPersonID) {
		if taskRun.UpdatedAt.Before(since) {
			continue
		}
		if isFound && !taskRun.UpdatedAt.After(latest.UpdatedAt) {
			continue
		}
		latest = taskRun
		isFound = true
	}
	return latest, isFound
}

type virtualACPClient struct {
	mutex    sync.Mutex
	messages []string
}

func (client *virtualACPClient) messageCount() int {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return len(client.messages)
}

func (client *virtualACPClient) messagesSince(index int) string {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return strings.Join(client.messages[index:], "")
}

func (client *virtualACPClient) SessionUpdate(_ context.Context, notification acp.SessionNotification) error {
	chunk := notification.Update.AgentMessageChunk
	if chunk == nil || chunk.Content.Text == nil {
		return nil
	}
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.messages = append(client.messages, chunk.Content.Text.Text)
	return nil
}

func (client *virtualACPClient) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
}

func (client *virtualACPClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, io.ErrUnexpectedEOF
}

func (client *virtualACPClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, io.ErrUnexpectedEOF
}

func (client *virtualACPClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, io.ErrUnexpectedEOF
}

func (client *virtualACPClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, io.ErrUnexpectedEOF
}

func (client *virtualACPClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, io.ErrUnexpectedEOF
}

func (client *virtualACPClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, io.ErrUnexpectedEOF
}

func (client *virtualACPClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, io.ErrUnexpectedEOF
}
