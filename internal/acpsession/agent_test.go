package acpsession

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type recordingLauncher struct {
	mutex          sync.Mutex
	launched       []agentruntime.TaskLaunchRequest
	reply          string
	attachments    []toolcontract.FileAttachment
	taskRunID      string
	launchedSignal chan agentruntime.TaskLaunchRequest
	startsANewRun  bool
}

func (launcher *recordingLauncher) taskRunIDFor(request agentruntime.TaskLaunchRequest) string {
	if launcher.startsANewRun {
		return launcher.taskRunID
	}
	return firstNonEmpty(request.ExistingTaskRunID, launcher.taskRunID)
}

func (launcher *recordingLauncher) Launch(_ context.Context, request agentruntime.TaskLaunchRequest) (agentruntime.TaskLaunchResult, error) {
	launcher.mutex.Lock()
	launcher.launched = append(launcher.launched, request)
	launchedSignal := launcher.launchedSignal
	launcher.mutex.Unlock()
	select {
	case launchedSignal <- request:
	default:
	}
	return agentruntime.TaskLaunchResult{TurnResult: agentcontract.AgentTurnResult{
		TaskRun:       agentcontract.TaskRun{TaskRunID: launcher.taskRunIDFor(request), Status: agentcontract.TaskStatusCompleted},
		FinishMessage: launcher.reply,
		Attachments:   launcher.attachments,
	}}, nil
}

type staticDirectory struct{}

func (staticDirectory) ResolvePersonIDByEmail(email string) (string, bool) {
	if email == "sample@example.test" {
		return "person-sample", true
	}
	return "", false
}

func (directory staticDirectory) AwaitPersonIDByEmail(ctx context.Context, email string) (string, error) {
	if personID, isKnown := directory.ResolvePersonIDByEmail(email); isKnown {
		return personID, nil
	}
	<-ctx.Done()
	return "", ctx.Err()
}

func (staticDirectory) ResolvePersonDisplayName(string) string { return "이샘플" }

func (staticDirectory) ResolvePersonAccess(personID string) policy.PersonAccess {
	return policy.PersonAccess{PersonID: personID}
}

type recordingClient struct {
	mutex              sync.Mutex
	connection         *acp.ClientSideConnection
	notifications      []acp.SessionNotification
	messages           []string
	thoughts           []string
	resourceLinks      []acp.ContentBlockResourceLink
	deliveries         []Delivery
	postedMessages     int
	undeliveredBecause string
	refusedFiles       map[string]string
	posted             []string
	isSilent           bool
	permissionAsked    []acp.RequestPermissionRequest
}

func (client *recordingClient) SessionUpdate(ctx context.Context, notification acp.SessionNotification) error {
	client.mutex.Lock()
	client.notifications = append(client.notifications, notification)
	refusal := ""
	if chunk := notification.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
		client.messages = append(client.messages, chunk.Content.Text.Text)
		client.posted = append(client.posted, "words:"+chunk.Content.Text.Text)
	}
	if chunk := notification.Update.AgentThoughtChunk; chunk != nil && chunk.Content.Text != nil {
		client.thoughts = append(client.thoughts, chunk.Content.Text.Text)
	}
	if chunk := notification.Update.AgentMessageChunk; chunk != nil && chunk.Content.ResourceLink != nil {
		client.resourceLinks = append(client.resourceLinks, *chunk.Content.ResourceLink)
		client.posted = append(client.posted, "file:"+chunk.Content.ResourceLink.Name)
		refusal = client.refusedFiles[chunk.Content.ResourceLink.Name]
	}
	client.mutex.Unlock()
	go client.reportDelivery(context.WithoutCancel(ctx), notification.Meta, refusal)
	return nil
}

func (client *recordingClient) reportDelivery(ctx context.Context, meta map[string]any, refusal string) {
	delivery, isNamed := deliveryNamedIn(meta)
	if !isNamed || client.isSilent {
		return
	}
	client.mutex.Lock()
	client.deliveries = append(client.deliveries, delivery)
	undeliveredBecause := firstNonEmpty(refusal, client.undeliveredBecause)
	if undeliveredBecause == "" {
		client.postedMessages++
	}
	posted := client.postedMessages
	connection := client.connection
	client.mutex.Unlock()
	if delivery.DeliveryID == "" {
		return
	}
	if undeliveredBecause != "" {
		_, _ = connection.CallExtension(ctx, UndeliveredExtensionMethod, UndeliveredReport{DeliveryID: delivery.DeliveryID, Reason: undeliveredBecause})
		return
	}
	_, _ = connection.CallExtension(ctx, DeliveredExtensionMethod, DeliveredReport{DeliveryID: delivery.DeliveryID, MessageID: fmt.Sprintf("posted-%d", posted)})
}

func deliveryNamedIn(meta map[string]any) (Delivery, bool) {
	carried, isCarried := meta[DeliveryMetaKey]
	if !isCarried {
		return Delivery{}, false
	}
	document, errorValue := json.Marshal(carried)
	if errorValue != nil {
		return Delivery{}, false
	}
	delivery := Delivery{}
	return delivery, json.Unmarshal(document, &delivery) == nil
}

func (client *recordingClient) RequestPermission(_ context.Context, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	client.mutex.Lock()
	client.permissionAsked = append(client.permissionAsked, request)
	client.mutex.Unlock()
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
}

func (client *recordingClient) permissionRequests() int {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return len(client.permissionAsked)
}

func (client *recordingClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, io.ErrUnexpectedEOF
}

func (client *recordingClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, io.ErrUnexpectedEOF
}

func (client *recordingClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, io.ErrUnexpectedEOF
}

func (client *recordingClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, io.ErrUnexpectedEOF
}

func (client *recordingClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, io.ErrUnexpectedEOF
}

func (client *recordingClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, io.ErrUnexpectedEOF
}

func (client *recordingClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, io.ErrUnexpectedEOF
}

func approvalRequestForTest() mcpserver.ApprovalRequest {
	return mcpserver.ApprovalRequest{
		Platform:       "buzz",
		RequesterEmail: "sample@example.test",
		ConversationID: "conversation-1",
		TaskRunID:      "task-1",
		ToolName:       "message_send",
		ToolInput:      json.RawMessage(`{"targetType":"directMessage"}`),
	}
}

func connectorRuntimeForTest(taskRunStore taskstate.TaskRunStore) *connectors.ConnectorRuntime {
	taskRunService, isShared := taskRunStore.(*task.TaskRunService)
	if !isShared {
		taskRunService = task.NewTaskRunService(task.NewTaskEventService())
	}
	return connectors.NewConnectorRuntime(identity.NewIdentityService(policy.PolicyProjection{}), nil, taskRunService, task.NewTaskEventService(), silentLogger())
}

func connectorRuntimeDeciding(taskRunStore taskstate.TaskRunStore, gatewayDecider inboundengagement.Decider) *connectors.ConnectorRuntime {
	connectorRuntime := connectorRuntimeForTest(taskRunStore)
	connectorRuntime.UseGatewayDecider(gatewayDecider)
	return connectorRuntime
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func connectedPair(t *testing.T, launcher TaskLauncher, client *recordingClient) (*acp.ClientSideConnection, *SessionRoutes) {
	t.Helper()
	return connectedPairWithCollaborators(t, client, Collaborators{TaskLauncher: launcher, Directory: staticDirectory{}})
}

func connectedPairWithCollaborators(t *testing.T, client *recordingClient, collaborators Collaborators) (*acp.ClientSideConnection, *SessionRoutes) {
	t.Helper()
	permissionRelay := NewSessionRoutes(silentLogger())
	if collaborators.SessionTurns == nil {
		collaborators.SessionTurns = connectorRuntimeForTest(collaborators.TaskRunStore)
	}
	return connectAgentTo(t, NewAgent(collaborators, permissionRelay, silentLogger()), client), permissionRelay
}

func connectAgentTo(t *testing.T, agent *Agent, client *recordingClient) *acp.ClientSideConnection {
	t.Helper()
	agentSide, clientSide := net.Pipe()
	agentConnection := acp.NewAgentSideConnection(agent, agentSide, agentSide)
	agent.UseConnection(agentConnection)
	clientConnection := acp.NewClientSideConnection(client, clientSide, clientSide)
	client.mutex.Lock()
	client.connection = clientConnection
	client.mutex.Unlock()
	t.Cleanup(func() {
		agentSide.Close()
		clientSide.Close()
	})
	return clientConnection
}

func sessionMeta(email string, conversationID string) map[string]any {
	return map[string]any{SessionMetaKey: map[string]any{
		"requester":  map[string]any{"email": email},
		"addressing": map[string]any{"platform": "buzz", "conversationID": conversationID},
	}}
}

func openSessionForTest(t *testing.T, connection *acp.ClientSideConnection, meta map[string]any) acp.SessionId {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, errorValue := connection.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); errorValue != nil {
		t.Fatalf("initialize: %v", errorValue)
	}
	newSession, errorValue := connection.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        "/workspace",
		McpServers: []acp.McpServer{},
		Meta:       meta,
	})
	if errorValue != nil {
		t.Fatalf("new session: %v", errorValue)
	}
	return newSession.SessionId
}

func TestPromptRunsATurnForTheRequesterTheSessionNames(t *testing.T) {
	launcher := &recordingLauncher{reply: "보냈습니다"}
	client := &recordingClient{}
	connection, _ := connectedPair(t, launcher, client)
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, errorValue := connection.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock("박예시한테 DM 보내줘")},
	})
	if errorValue != nil {
		t.Fatalf("prompt: %v", errorValue)
	}
	if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason is %q, expected %q", response.StopReason, acp.StopReasonEndTurn)
	}
	if len(launcher.launched) != 1 {
		t.Fatalf("the turn launched %d times, expected once", len(launcher.launched))
	}
	launched := launcher.launched[0]
	if launched.RequesterPersonID != "person-sample" {
		t.Fatalf("the turn ran for %q, expected person-sample", launched.RequesterPersonID)
	}
	if launched.Platform != "buzz" || launched.ConversationID != "conversation-1" {
		t.Fatalf("the turn was addressed to %q/%q, expected buzz/conversation-1", launched.Platform, launched.ConversationID)
	}
	if launched.Prompt != "박예시한테 DM 보내줘" {
		t.Fatalf("the turn carried %q", launched.Prompt)
	}
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if strings.Join(client.messages, "") != "보냈습니다" {
		t.Fatalf("the client was told %v, expected the finish message", client.messages)
	}
}

func TestSessionThatNamesNobodyIsRefused(t *testing.T) {
	client := &recordingClient{}
	connection, _ := connectedPair(t, &recordingLauncher{}, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, errorValue := connection.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); errorValue != nil {
		t.Fatalf("initialize: %v", errorValue)
	}
	_, errorValue := connection.NewSession(ctx, acp.NewSessionRequest{Cwd: "/workspace", McpServers: []acp.McpServer{}})
	if errorValue == nil {
		t.Fatal("a session that names nobody opened, and every tool it ran would run as the service account")
	}
}

func TestSessionForSomebodyTheRosterNeverNamesIsRefusedOnceItsCallerStopsWaiting(t *testing.T) {
	launcher := &recordingLauncher{}
	client := &recordingClient{}
	connection, _ := connectedPair(t, launcher, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, errorValue := connection.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); errorValue != nil {
		t.Fatalf("initialize: %v", errorValue)
	}
	waiting, stopWaiting := context.WithTimeout(ctx, 200*time.Millisecond)
	defer stopWaiting()
	_, errorValue := connection.NewSession(waiting, acp.NewSessionRequest{
		Cwd:        "/workspace",
		McpServers: []acp.McpServer{},
		Meta:       sessionMeta("stranger@example.test", "conversation-1"),
	})
	if errorValue == nil {
		t.Fatal("a session opened for somebody this company does not know")
	}
}

type sessionOpening struct {
	sessionID  acp.SessionId
	errorValue error
}

func TestAMessageFromSomebodyTheRosterNamesOnlyLaterIsAnsweredOnceItDoes(t *testing.T) {
	roster := identity.NewIdentityService(policy.PolicyProjection{})
	launcher := &recordingLauncher{reply: "받았습니다"}
	client := &recordingClient{}
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: launcher,
		Directory:    roster,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, errorValue := connection.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); errorValue != nil {
		t.Fatalf("initialize: %v", errorValue)
	}
	opened := make(chan sessionOpening, 1)
	go func() {
		response, errorValue := connection.NewSession(ctx, acp.NewSessionRequest{
			Cwd:        "/workspace",
			McpServers: []acp.McpServer{},
			Meta:       sessionMeta("late@example.test", "conversation-1"),
		})
		opened <- sessionOpening{sessionID: response.SessionId, errorValue: errorValue}
	}()
	select {
	case opening := <-opened:
		t.Fatalf("the session answered before the roster named its requester: %v", opening.errorValue)
	case <-time.After(200 * time.Millisecond):
	}

	roster.ReloadPolicyProjection(policy.PolicyProjection{PersonIDByEmail: map[string]string{"late@example.test": "person-late"}})
	opening := <-opened
	if opening.errorValue != nil {
		t.Fatalf("the session did not open after the roster named its requester: %v", opening.errorValue)
	}
	promptForTest(t, connection, opening.sessionID)

	if launched := theOnlyLaunch(t, launcher); launched.RequesterPersonID != "person-late" {
		t.Fatalf("the turn ran for %q, expected person-late", launched.RequesterPersonID)
	}
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if strings.Join(client.messages, "") != "받았습니다" {
		t.Fatalf("the client was told %v, expected the finish message", client.messages)
	}
}

func heldCallForTest() agentcontract.HeldCall {
	return agentcontract.HeldCall{
		HoldID:       "hold-stored-before-the-deploy",
		ToolName:     "message_send",
		ToolInput:    json.RawMessage(`{"targetType":"directMessage"}`),
		Confirmation: "박예시에게 보낼까요?",
	}
}

func runWaitingOnAHeldCall(t *testing.T, taskRunService *task.TaskRunService, conversationID string, heldCall agentcontract.HeldCall) agentcontract.TaskRun {
	t.Helper()
	taskRun := taskRunService.CreateTaskRun("person-sample", conversationID, "박예시한테 DM 보내줘")
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, agentcontract.TaskStatusWaitingApproval, heldCall.Confirmation); errorValue != nil {
		t.Fatalf("pause task run: %v", errorValue)
	}
	holdrecord.Open(taskRunService, taskRun.TaskRunID, heldCall, nil)
	return taskRun
}

func reconnectedPair(t *testing.T, launcher TaskLauncher, client *recordingClient, taskRunService *task.TaskRunService) *acp.ClientSideConnection {
	t.Helper()
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{TaskLauncher: launcher, Directory: staticDirectory{}, TaskRunStore: taskRunService})
	return connection
}

func loadSessionForTest(t *testing.T, connection *acp.ClientSideConnection, sessionID acp.SessionId, meta map[string]any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, errorValue := connection.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); errorValue != nil {
		t.Fatalf("initialize: %v", errorValue)
	}
	_, errorValue := connection.LoadSession(ctx, acp.LoadSessionRequest{
		SessionId:  sessionID,
		Cwd:        "/workspace",
		McpServers: []acp.McpServer{},
		Meta:       meta,
	})
	return errorValue
}

func hasTaskEvent(taskRunService *task.TaskRunService, taskRunID string, name string) bool {
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == name {
			return true
		}
	}
	return false
}

func TestLoadingASessionThatNamesNobodyIsRefused(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	runWaitingOnAHeldCall(t, taskRunService, "conversation-1", heldCallForTest())
	client := &recordingClient{}
	connection := reconnectedPair(t, &recordingLauncher{}, client, taskRunService)

	if errorValue := loadSessionForTest(t, connection, "session-the-relay-still-holds", nil); errorValue == nil {
		t.Fatal("a reloaded session that names nobody opened, and every tool it ran would run as the service account")
	}
}

func openSessionNamingTheCatalog(
	t *testing.T,
	connection *acp.ClientSideConnection,
	meta map[string]any,
	mcpServers []acp.McpServer,
) acp.SessionId {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, errorValue := connection.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); errorValue != nil {
		t.Fatalf("initialize: %v", errorValue)
	}
	newSession, errorValue := connection.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        "/workspace",
		McpServers: mcpServers,
		Meta:       meta,
	})
	if errorValue != nil {
		t.Fatalf("new session: %v", errorValue)
	}
	return newSession.SessionId
}

func theOnlyLaunch(t *testing.T, launcher *recordingLauncher) agentruntime.TaskLaunchRequest {
	t.Helper()
	launcher.mutex.Lock()
	defer launcher.mutex.Unlock()
	if len(launcher.launched) != 1 {
		t.Fatalf("the turn launched %d times, expected once", len(launcher.launched))
	}
	return launcher.launched[0]
}

func promptForTest(t *testing.T, connection *acp.ClientSideConnection, sessionID acp.SessionId) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, errorValue := connection.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock("업무 하나 추가해줘")},
	}); errorValue != nil {
		t.Fatalf("prompt: %v", errorValue)
	}
}

func TestATurnIsGivenTheCatalogTheSessionNamed(t *testing.T) {
	launcher := &recordingLauncher{reply: "네"}
	connection, _ := connectedPair(t, launcher, &recordingClient{})
	sessionID := openSessionNamingTheCatalog(t, connection,
		sessionMeta("sample@example.test", "conversation-1"),
		[]acp.McpServer{{Http: &acp.McpServerHttpInline{
			Name:    "internkim",
			Url:     "http://127.0.0.1:18091/mcp/session-1",
			Headers: []acp.HttpHeader{{Name: "Authorization", Value: "Bearer a-token"}},
		}}},
	)

	promptForTest(t, connection, sessionID)

	if theOnlyLaunch(t, launcher).RecordCatalog == nil {
		t.Fatal("the turn was given no record catalog although the session named one")
	}
}

func TestATurnIsGivenNoCatalogWhenTheSessionNamedNone(t *testing.T) {
	launcher := &recordingLauncher{reply: "네"}
	connection, _ := connectedPair(t, launcher, &recordingClient{})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptForTest(t, connection, sessionID)

	if launched := theOnlyLaunch(t, launcher); launched.RecordCatalog != nil {
		t.Fatalf("a session naming no catalog handed one over: %+v", launched.RecordCatalog)
	}
}

func TestAProgressReplyHandsTheSessionTheFileItAnnounces(t *testing.T) {
	launcher := &recordingLauncher{reply: "보냈습니다"}
	client := &recordingClient{}
	connection, _ := connectedPair(t, launcher, client)
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, errorValue := connection.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock("초안 보내줘")},
	}); errorValue != nil {
		t.Fatalf("prompt: %v", errorValue)
	}
	if len(launcher.launched) != 1 {
		t.Fatalf("the turn launched %d times, expected once", len(launcher.launched))
	}
	checkpointSender := launcher.launched[0].CheckpointSender
	if checkpointSender == nil {
		t.Fatal("the launched turn has no way to speak mid-task")
	}

	if errorValue := checkpointSender(ctx, agentcontract.AgentCheckpoint{
		TaskRunID:   "task-1",
		Message:     "초안을 먼저 보냅니다.",
		Attachments: []toolcontract.FileAttachment{{DevicePath: "/tmp/draft.pdf", Filename: "draft.pdf"}},
	}); errorValue != nil {
		t.Fatalf("checkpoint: %v", errorValue)
	}

	if deliveredLink := client.waitForResourceLink(); deliveredLink != "file:///tmp/draft.pdf" {
		t.Fatalf("the file the reply announced never reached the session, got %q", deliveredLink)
	}
}

func (client *recordingClient) waitForResourceLink() string {
	for attempt := 0; attempt < 100; attempt++ {
		client.mutex.Lock()
		links := append([]acp.ContentBlockResourceLink{}, client.resourceLinks...)
		client.mutex.Unlock()
		if len(links) > 0 {
			return links[0].Uri
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ""
}

func waitForLaunch(t *testing.T, launcher *recordingLauncher) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		launcher.mutex.Lock()
		launchCount := len(launcher.launched)
		launcher.mutex.Unlock()
		if launchCount > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the answered question never resumed its run")
}
