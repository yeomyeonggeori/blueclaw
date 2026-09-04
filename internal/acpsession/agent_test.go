package acpsession

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type recordingLauncher struct {
	mutex          sync.Mutex
	launched       []agentruntime.TaskLaunchRequest
	reply          string
	launchedSignal chan agentruntime.TaskLaunchRequest
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
		TaskRun:       agentcontract.TaskRun{Status: agentcontract.TaskStatusCompleted},
		FinishMessage: launcher.reply,
	}}, nil
}

type staticDirectory struct{}

func (staticDirectory) ResolvePersonIDByEmail(email string) (string, bool) {
	if email == "sample@example.test" {
		return "person-sample", true
	}
	return "", false
}

func (staticDirectory) ResolvePersonDisplayName(string) string { return "이샘플" }

func (staticDirectory) ResolvePersonAccess(personID string) policy.PersonAccess {
	return policy.PersonAccess{PersonID: personID}
}

type recordingClient struct {
	mutex                 sync.Mutex
	messages              []string
	thoughts              []string
	permissionAsked       []acp.RequestPermissionRequest
	permissionAskedSignal chan acp.RequestPermissionRequest
	permissionChoice      acp.PermissionOptionId
	answerByAsking        func(acp.RequestPermissionRequest) acp.PermissionOptionId
}

func (client *recordingClient) SessionUpdate(_ context.Context, notification acp.SessionNotification) error {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if chunk := notification.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
		client.messages = append(client.messages, chunk.Content.Text.Text)
	}
	if chunk := notification.Update.AgentThoughtChunk; chunk != nil && chunk.Content.Text != nil {
		client.thoughts = append(client.thoughts, chunk.Content.Text.Text)
	}
	return nil
}

func (client *recordingClient) RequestPermission(_ context.Context, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	client.mutex.Lock()
	client.permissionAsked = append(client.permissionAsked, request)
	choice := client.permissionChoice
	answerByAsking := client.answerByAsking
	askedSignal := client.permissionAskedSignal
	client.mutex.Unlock()
	select {
	case askedSignal <- request:
	default:
	}
	if answerByAsking != nil {
		choice = answerByAsking(request)
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(choice)}, nil
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
		ConversationID: "conversation-1",
		TaskRunID:      "task-1",
		ToolName:       "message_send",
		ToolInput:      json.RawMessage(`{"targetType":"directMessage"}`),
		ApprovalScope:  "message_send",
	}
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func connectedPair(t *testing.T, launcher TaskLauncher, client *recordingClient) (*acp.ClientSideConnection, *PermissionRelay) {
	return connectedPairWithRouter(t, launcher, client, scriptedRouter{})
}

func connectedPairWithRouter(t *testing.T, launcher TaskLauncher, client *recordingClient, turnRouter TurnRouter) (*acp.ClientSideConnection, *PermissionRelay) {
	t.Helper()
	return connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: launcher,
		Directory:    staticDirectory{},
		TurnRouter:   turnRouter,
	})
}

func connectedPairWithCollaborators(t *testing.T, client *recordingClient, collaborators Collaborators) (*acp.ClientSideConnection, *PermissionRelay) {
	t.Helper()
	agentSide, clientSide := net.Pipe()
	permissionRelay := NewPermissionRelay(silentLogger())
	agent := NewAgent(collaborators, permissionRelay, silentLogger())
	agentConnection := acp.NewAgentSideConnection(agent, agentSide, agentSide)
	agent.UseConnection(agentConnection)
	clientConnection := acp.NewClientSideConnection(client, clientSide, clientSide)
	t.Cleanup(func() {
		agentSide.Close()
		clientSide.Close()
	})
	return clientConnection, permissionRelay
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

func TestSessionForSomebodyTheCompanyDoesNotKnowIsRefused(t *testing.T) {
	client := &recordingClient{}
	connection, _ := connectedPair(t, &recordingLauncher{}, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, errorValue := connection.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); errorValue != nil {
		t.Fatalf("initialize: %v", errorValue)
	}
	_, errorValue := connection.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        "/workspace",
		McpServers: []acp.McpServer{},
		Meta:       sessionMeta("stranger@example.test", "conversation-1"),
	})
	if errorValue == nil {
		t.Fatal("a session opened for somebody this company does not know")
	}
}

func TestHeldCallReachesTheRequesterOverTheSessionThatOwnsTheConversation(t *testing.T) {
	approvalRequest := approvalRequestForTest()
	client := &recordingClient{permissionChoice: approveOnceOptionID}
	connection, permissionRelay := connectedPair(t, &recordingLauncher{}, client)
	openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	approvalSignal, isAnswered := permissionRelay.AskPermission(ctx, approvalRequest, "박예시에게 보낼까요?")
	if !isAnswered {
		t.Fatal("nobody was asked, so the call would have been held instead of run")
	}
	if approvalSignal != agentcontract.ApprovalSignalApprove {
		t.Fatalf("the answer read as %q, expected approve", approvalSignal)
	}
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if len(client.permissionAsked) != 1 {
		t.Fatalf("the client was asked %d times, expected once", len(client.permissionAsked))
	}
	asked := client.permissionAsked[0]
	if asked.ToolCall.ToolCallId != acp.ToolCallId(approvalgate.HeldCallID(approvalRequest.ToolName, approvalRequest.ToolInput)) {
		t.Fatalf("the question carried tool call id %q, which no restart could recompute", asked.ToolCall.ToolCallId)
	}
	if asked.ToolCall.Title == nil || *asked.ToolCall.Title != "박예시에게 보낼까요?" {
		t.Fatal("the question the runtime worded is not the question the person was asked")
	}
	if len(asked.Options) != 3 {
		t.Fatalf("the person was offered %d options, expected approve, approve-task and decline", len(asked.Options))
	}
}

func TestACallInAConversationNoSessionOwnsIsNotAsked(t *testing.T) {
	client := &recordingClient{permissionChoice: approveOnceOptionID}
	connection, permissionRelay := connectedPair(t, &recordingLauncher{}, client)
	openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, isAnswered := permissionRelay.AskPermission(ctx, mcpserver.ApprovalRequest{
		Platform:       "buzz",
		ConversationID: "conversation-nobody-opened",
		TaskRunID:      "task-2",
		ToolName:       "message_send",
	}, "보낼까요?")
	if isAnswered {
		t.Fatal("a call was answered by a session that owns another conversation")
	}
}

func TestDecliningTheCallReadsAsReject(t *testing.T) {
	client := &recordingClient{permissionChoice: rejectOnceOptionID}
	connection, permissionRelay := connectedPair(t, &recordingLauncher{}, client)
	openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	approvalSignal, isAnswered := permissionRelay.AskPermission(ctx, mcpserver.ApprovalRequest{
		Platform:       "buzz",
		ConversationID: "conversation-1",
		TaskRunID:      "task-1",
		ToolName:       "message_send",
	}, "보낼까요?")
	if !isAnswered || approvalSignal != agentcontract.ApprovalSignalReject {
		t.Fatalf("declining read as %q answered=%v", approvalSignal, isAnswered)
	}
}

type scriptedRouter struct {
	approvalSignal *agentcontract.ApprovalSignal
	planned        *[]agentcontract.AgentRequest
}

func (router scriptedRouter) Plan(_ context.Context, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	if router.planned != nil {
		*router.planned = append(*router.planned, request)
	}
	return agentcontract.TurnDecision{Approval: router.approvalSignal}, nil
}

func approvalSignalPointer(approvalSignal agentcontract.ApprovalSignal) *agentcontract.ApprovalSignal {
	return &approvalSignal
}

func answeringWithWords(t *testing.T, connection *acp.ClientSideConnection, sessionID acp.SessionId, words string) func(acp.RequestPermissionRequest) acp.PermissionOptionId {
	t.Helper()
	return func(request acp.RequestPermissionRequest) acp.PermissionOptionId {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		answer, errorValue := connection.CallExtension(ctx, ApprovalReplyExtensionMethod, ApprovalReplyRequest{
			SessionID:  string(sessionID),
			ToolCallID: string(request.ToolCall.ToolCallId),
			Reply:      words,
		})
		if errorValue != nil {
			t.Errorf("approval reply: %v", errorValue)
			return rejectOnceOptionID
		}
		read := ApprovalReplyResponse{}
		if errorValue := json.Unmarshal(answer, &read); errorValue != nil {
			t.Errorf("approval reply answer: %v", errorValue)
			return rejectOnceOptionID
		}
		return acp.PermissionOptionId(read.OptionID)
	}
}

func TestThePersonsWordsAreReadByTheRouterAndNotByTheRelay(t *testing.T) {
	planned := []agentcontract.AgentRequest{}
	client := &recordingClient{}
	connection, permissionRelay := connectedPairWithRouter(t, &recordingLauncher{}, client, scriptedRouter{
		approvalSignal: approvalSignalPointer(agentcontract.ApprovalSignalApprove),
		planned:        &planned,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	client.answerByAsking = answeringWithWords(t, connection, sessionID, "응 보내줘")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	approvalSignal, isAnswered := permissionRelay.AskPermission(ctx, approvalRequestForTest(), "박예시에게 보낼까요?")

	if !isAnswered || approvalSignal != agentcontract.ApprovalSignalApprove {
		t.Fatalf("the answer read as %q answered=%v", approvalSignal, isAnswered)
	}
	if len(planned) != 1 {
		t.Fatalf("the router was asked %d times, expected once", len(planned))
	}
	if planned[0].Prompt != "응 보내줘" {
		t.Fatalf("the router was given %q, and the person's own words are what it has to read", planned[0].Prompt)
	}
	// The router offers an approval at all only when it is told which call is
	// waiting, so a request without the task run can never come back approved.
	if planned[0].PendingConfirmation.TaskRunID != "task-1" {
		t.Fatalf("the router was not told which call is waiting (%q), so it was never offered an approval to give", planned[0].PendingConfirmation.TaskRunID)
	}
	if planned[0].PendingConfirmation.Question != "박예시에게 보낼까요?" {
		t.Fatalf("the router was asked about %q, not the question the person answered", planned[0].PendingConfirmation.Question)
	}
}

func TestAnAnswerTheRouterCannotReadIsNotAnApproval(t *testing.T) {
	client := &recordingClient{}
	connection, permissionRelay := connectedPairWithRouter(t, &recordingLauncher{}, client, scriptedRouter{
		approvalSignal: approvalSignalPointer(agentcontract.ApprovalSignalUnclear),
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	client.answerByAsking = answeringWithWords(t, connection, sessionID, "음")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	approvalSignal, isAnswered := permissionRelay.AskPermission(ctx, approvalRequestForTest(), "보낼까요?")

	if !isAnswered || approvalSignal != agentcontract.ApprovalSignalReject {
		t.Fatalf("an unclear answer read as %q, and a call would run that nobody agreed to", approvalSignal)
	}
}

func TestAnAnswerToACallNobodyIsWaitingOnIsRefused(t *testing.T) {
	client := &recordingClient{}
	connection, _ := connectedPairWithRouter(t, &recordingLauncher{}, client, scriptedRouter{
		approvalSignal: approvalSignalPointer(agentcontract.ApprovalSignalApprove),
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, errorValue := connection.CallExtension(ctx, ApprovalReplyExtensionMethod, ApprovalReplyRequest{
		SessionID:  string(sessionID),
		ToolCallID: "held-nobody-asked",
		Reply:      "응 보내줘",
	})
	if errorValue == nil {
		t.Fatal("a call nobody is waiting on was answered, so any client could approve anything")
	}
}

func heldCallForTest() agentcontract.HeldCall {
	return agentcontract.HeldCall{
		ToolName:      "message_send",
		ToolInput:     json.RawMessage(`{"targetType":"directMessage"}`),
		ApprovalScope: "message_send",
		Confirmation:  "박예시에게 보낼까요?",
	}
}

func runWaitingOnAHeldCall(t *testing.T, taskRunService *task.TaskRunService, conversationID string, heldCall agentcontract.HeldCall) agentcontract.TaskRun {
	t.Helper()
	taskRun := taskRunService.CreateTaskRun("person-sample", conversationID, "박예시한테 DM 보내줘")
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, agentcontract.TaskStatusWaitingApproval, heldCall.Confirmation); errorValue != nil {
		t.Fatalf("pause task run: %v", errorValue)
	}
	body, errorValue := json.Marshal(heldCall)
	if errorValue != nil {
		t.Fatalf("held call body: %v", errorValue)
	}
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventApprovalPendingCall, string(body))
	return taskRun
}

func reconnectedPair(t *testing.T, launcher TaskLauncher, client *recordingClient, taskRunService *task.TaskRunService) *acp.ClientSideConnection {
	t.Helper()
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: launcher,
		Directory:    staticDirectory{},
		TurnRouter:   scriptedRouter{},
		TaskRunStore: taskRunService,
	})
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

// The load response is answered before the questions go out, so the test waits
// for the question rather than for the load.
func awaitPermissionRequest(t *testing.T, client *recordingClient) acp.RequestPermissionRequest {
	t.Helper()
	select {
	case request := <-client.permissionAskedSignal:
		return request
	case <-time.After(5 * time.Second):
		t.Fatal("the reconnected client was never asked again, so the run waits for an answer nobody will give")
	}
	return acp.RequestPermissionRequest{}
}

func expectNobodyIsAskedAgain(t *testing.T, client *recordingClient) {
	t.Helper()
	select {
	case request := <-client.permissionAskedSignal:
		t.Fatalf("the client was asked about %q, which nobody is waiting on", request.ToolCall.ToolCallId)
	case <-time.After(500 * time.Millisecond):
	}
}

func hasTaskEvent(taskRunService *task.TaskRunService, taskRunID string, name string) bool {
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == name {
			return true
		}
	}
	return false
}

func TestLoadingASessionAsksAgainAboutTheCallItsRunStoppedOn(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	heldCall := heldCallForTest()
	runWaitingOnAHeldCall(t, taskRunService, "conversation-1", heldCall)
	client := &recordingClient{permissionAskedSignal: make(chan acp.RequestPermissionRequest, 4)}
	connection := reconnectedPair(t, &recordingLauncher{}, client, taskRunService)

	if errorValue := loadSessionForTest(t, connection, "session-the-relay-still-holds", sessionMeta("sample@example.test", "conversation-1")); errorValue != nil {
		t.Fatalf("load session: %v", errorValue)
	}

	asked := awaitPermissionRequest(t, client)
	if asked.ToolCall.ToolCallId != acp.ToolCallId(approvalgate.HeldCallID(heldCall.ToolName, heldCall.ToolInput)) {
		t.Fatalf("the question carried tool call id %q, which is not the one the client already has open", asked.ToolCall.ToolCallId)
	}
	if asked.ToolCall.Title == nil || *asked.ToolCall.Title != heldCall.Confirmation {
		t.Fatal("the person was asked something other than the question the run stopped on")
	}
	expectNobodyIsAskedAgain(t, client)
}

func TestApprovingTheReissuedQuestionResumesTheRunItBelongsTo(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	waitingRun := runWaitingOnAHeldCall(t, taskRunService, "conversation-1", heldCallForTest())
	launcher := &recordingLauncher{launchedSignal: make(chan agentruntime.TaskLaunchRequest, 4)}
	client := &recordingClient{
		permissionAskedSignal: make(chan acp.RequestPermissionRequest, 4),
		permissionChoice:      approveOnceOptionID,
	}
	connection := reconnectedPair(t, launcher, client, taskRunService)

	if errorValue := loadSessionForTest(t, connection, "session-the-relay-still-holds", sessionMeta("sample@example.test", "conversation-1")); errorValue != nil {
		t.Fatalf("load session: %v", errorValue)
	}

	resumed := agentruntime.TaskLaunchRequest{}
	select {
	case resumed = <-launcher.launchedSignal:
	case <-time.After(5 * time.Second):
		t.Fatal("the answered call never resumed, so the approval was collected and thrown away")
	}
	if !resumed.IsApprovalContinuation {
		t.Fatal("the resumed turn does not carry the approval, so the call would be asked about all over again")
	}
	if !resumed.IsRuntimeRestartResume {
		t.Fatal("the resumed turn does not read as a restart resume, so it looks like a fresh request")
	}
	if resumed.ExistingTaskRunID != waitingRun.TaskRunID {
		t.Fatalf("the turn resumed %q, expected the run that was waiting (%q)", resumed.ExistingTaskRunID, waitingRun.TaskRunID)
	}
	if !hasTaskEvent(taskRunService, waitingRun.TaskRunID, agentcontract.TaskEventApprovalDecided) {
		t.Fatal("the answer was never recorded on the run, so a second restart would ask about the same call again")
	}
}

func TestACallHeldInAnotherConversationIsNotReissued(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	runWaitingOnAHeldCall(t, taskRunService, "conversation-somewhere-else", heldCallForTest())
	client := &recordingClient{permissionAskedSignal: make(chan acp.RequestPermissionRequest, 4)}
	connection := reconnectedPair(t, &recordingLauncher{}, client, taskRunService)

	if errorValue := loadSessionForTest(t, connection, "session-the-relay-still-holds", sessionMeta("sample@example.test", "conversation-1")); errorValue != nil {
		t.Fatalf("load session: %v", errorValue)
	}

	expectNobodyIsAskedAgain(t, client)
}

func TestACallTheRequesterAlreadyAnsweredIsNotAskedAgain(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	answeredRun := runWaitingOnAHeldCall(t, taskRunService, "conversation-1", heldCallForTest())
	taskRunService.AppendTaskEvent(answeredRun.TaskRunID, agentcontract.TaskEventApprovalDecided, `{"decision":"confirm","source":"acp_permission"}`)
	client := &recordingClient{permissionAskedSignal: make(chan acp.RequestPermissionRequest, 4)}
	connection := reconnectedPair(t, &recordingLauncher{}, client, taskRunService)

	if errorValue := loadSessionForTest(t, connection, "session-the-relay-still-holds", sessionMeta("sample@example.test", "conversation-1")); errorValue != nil {
		t.Fatalf("load session: %v", errorValue)
	}

	expectNobodyIsAskedAgain(t, client)
}

func TestLoadingASessionThatNamesNobodyIsRefused(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	runWaitingOnAHeldCall(t, taskRunService, "conversation-1", heldCallForTest())
	client := &recordingClient{permissionAskedSignal: make(chan acp.RequestPermissionRequest, 4)}
	connection := reconnectedPair(t, &recordingLauncher{}, client, taskRunService)

	if errorValue := loadSessionForTest(t, connection, "session-the-relay-still-holds", nil); errorValue == nil {
		t.Fatal("a reloaded session that names nobody opened, and every tool it ran would run as the service account")
	}
	expectNobodyIsAskedAgain(t, client)
}
