package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

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

func openVirtualACPSession(collaborators acpsession.Collaborators, conversationID string, recordCatalogURL string) (*virtualACPSession, error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	agentSide, clientSide := net.Pipe()
	agent := acpsession.NewAgent(collaborators, acpsession.NewPermissionRelay(logger), logger)
	agentConnection := acp.NewAgentSideConnection(agent, agentSide, agentSide)
	agent.UseConnection(agentConnection)
	client := &virtualACPClient{}
	connection := acp.NewClientSideConnection(client, clientSide, clientSide)
	client.connection = connection
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
		McpServers: virtualRecordCatalogServers(recordCatalogURL),
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

func virtualRecordCatalogServers(recordCatalogURL string) []acp.McpServer {
	if strings.TrimSpace(recordCatalogURL) == "" {
		return []acp.McpServer{}
	}
	return []acp.McpServer{{Http: &acp.McpServerHttpInline{Type: "http", Name: "internkim", Url: recordCatalogURL, Headers: []acp.HttpHeader{}}}}
}

func (session *virtualACPSession) prompt(ctx context.Context, event connectors.PlatformInboundEvent) (string, []toolcontract.FileAttachment, error) {
	messageCountBefore := session.client.messageCount()
	fileCountBefore := session.client.fileCount()
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
		return "", nil, errorValue
	}
	return session.client.messagesSince(messageCountBefore), session.client.filesSince(fileCountBefore), nil
}

func (harness *VirtualSessionHarness) runTurnOverACP(ctx context.Context, event connectors.PlatformInboundEvent, observedTurnResult func() VirtualTurnResult) (VirtualTurnResult, error) {
	updatedBefore := time.Now()
	reply, files, errorValue := harness.acpSession.prompt(ctx, event)
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
	turnResult.Attachments = files
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
	mutex      sync.Mutex
	connection *acp.ClientSideConnection
	messages   []string
	files      []toolcontract.FileAttachment
}

func (client *virtualACPClient) messageCount() int {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return len(client.messages)
}

func (client *virtualACPClient) fileCount() int {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return len(client.files)
}

func (client *virtualACPClient) filesSince(index int) []toolcontract.FileAttachment {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return append([]toolcontract.FileAttachment{}, client.files[index:]...)
}

func (client *virtualACPClient) messagesSince(index int) string {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return strings.Join(client.messages[index:], "")
}

func (client *virtualACPClient) SessionUpdate(ctx context.Context, notification acp.SessionNotification) error {
	chunk := notification.Update.AgentMessageChunk
	if chunk == nil {
		return nil
	}
	if chunk.Content.Text != nil {
		client.mutex.Lock()
		client.messages = append(client.messages, chunk.Content.Text.Text)
		client.mutex.Unlock()
	}
	if chunk.Content.ResourceLink != nil {
		client.mutex.Lock()
		client.files = append(client.files, fileAttachmentLinkedBy(*chunk.Content.ResourceLink))
		client.mutex.Unlock()
	}
	go client.reportPosted(context.WithoutCancel(ctx), notification.Meta)
	return nil
}

func fileAttachmentLinkedBy(link acp.ContentBlockResourceLink) toolcontract.FileAttachment {
	attachment := toolcontract.FileAttachment{Filename: link.Name}
	if parsed, errorValue := url.Parse(link.Uri); errorValue == nil {
		attachment.DevicePath = parsed.Path
	}
	if link.MimeType != nil {
		attachment.ContentType = *link.MimeType
	}
	if link.Size != nil {
		attachment.SizeBytes = int64(*link.Size)
	}
	return attachment
}

func (client *virtualACPClient) reportPosted(ctx context.Context, meta map[string]any) {
	deliveryID := deliveryIDNamedIn(meta)
	if deliveryID == "" {
		return
	}
	client.mutex.Lock()
	messageID := fmt.Sprintf("virtual-message-%d", len(client.messages))
	client.mutex.Unlock()
	_, _ = client.connection.CallExtension(ctx, acpsession.DeliveredExtensionMethod, acpsession.DeliveredReport{DeliveryID: deliveryID, MessageID: messageID})
}

func (client *virtualACPClient) RequestPermission(ctx context.Context, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if deliveryID := deliveryIDNamedIn(request.Meta); deliveryID != "" {
		_, _ = client.connection.CallExtension(ctx, acpsession.UndeliveredExtensionMethod, acpsession.UndeliveredReport{DeliveryID: deliveryID, Reason: "the virtual session puts no question to anybody"})
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
}

func deliveryIDNamedIn(meta map[string]any) string {
	document, errorValue := json.Marshal(meta[acpsession.DeliveryMetaKey])
	if errorValue != nil {
		return ""
	}
	delivery := acpsession.Delivery{}
	if json.Unmarshal(document, &delivery) != nil {
		return ""
	}
	return delivery.DeliveryID
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
