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
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type recordingLauncher struct {
	mutex          sync.Mutex
	launched       []agentruntime.TaskLaunchRequest
	reply          string
	attachments    []toolcontract.FileAttachment
	taskRunID      string
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
		TaskRun:       agentcontract.TaskRun{TaskRunID: firstNonEmpty(request.ExistingTaskRunID, launcher.taskRunID), Status: agentcontract.TaskStatusCompleted},
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
	mutex                 sync.Mutex
	connection            *acp.ClientSideConnection
	notifications         []acp.SessionNotification
	messages              []string
	thoughts              []string
	resourceLinks         []acp.ContentBlockResourceLink
	deliveries            []Delivery
	postedMessages        int
	undeliveredBecause    string
	refusedFiles          map[string]string
	posted                []string
	isSilent              bool
	permissionAsked       []acp.RequestPermissionRequest
	permissionAskedSignal chan acp.RequestPermissionRequest
	permissionChoice      acp.PermissionOptionId
	answerByAsking        func(acp.RequestPermissionRequest) acp.PermissionOptionId
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

func (client *recordingClient) RequestPermission(ctx context.Context, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	client.reportDelivery(ctx, request.Meta, "")
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

func connectorRuntimeForTest(taskRunStore taskstate.TaskRunStore) *connectors.ConnectorRuntime {
	taskRunService, isShared := taskRunStore.(*task.TaskRunService)
	if !isShared {
		taskRunService = task.NewTaskRunService(task.NewTaskEventService())
	}
	return connectors.NewConnectorRuntime(identity.NewIdentityService(policy.PolicyProjection{}), nil, taskRunService, task.NewTaskEventService(), silentLogger())
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func connectedPair(t *testing.T, launcher TaskLauncher, client *recordingClient) (*acp.ClientSideConnection, *PermissionRelay) {
	return connectedPairWithReader(t, launcher, client, scriptedReader{})
}

func connectedPairWithReader(t *testing.T, launcher TaskLauncher, client *recordingClient, replyReader approvalreply.Reader) (*acp.ClientSideConnection, *PermissionRelay) {
	t.Helper()
	return connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: launcher,
		Directory:    staticDirectory{},
		ReplyReader:  replyReader,
	})
}

func connectedPairWithCollaborators(t *testing.T, client *recordingClient, collaborators Collaborators) (*acp.ClientSideConnection, *PermissionRelay) {
	t.Helper()
	permissionRelay := NewPermissionRelay(silentLogger())
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
		ReplyReader:  scriptedReader{},
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

func TestHeldCallReachesTheRequesterOverTheSessionThatOwnsTheConversation(t *testing.T) {
	approvalRequest := approvalRequestForTest()
	client := &recordingClient{permissionChoice: approveOnceOptionID}
	connection, permissionRelay := connectedPair(t, &recordingLauncher{}, client)
	openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	answer, isAnswered := permissionRelay.AskPermission(ctx, approvalRequest, approvalgate.PermissionQuestion{Confirmation: "박예시에게 보낼까요?"})
	if !isAnswered {
		t.Fatal("nobody was asked, so the call would have been held instead of run")
	}
	if answer.Signal != agentcontract.ApprovalSignalApprove {
		t.Fatalf("the answer read as %q, expected approve", answer.Signal)
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
	if len(asked.Options) != 2 {
		t.Fatalf("the person was offered %d options, expected approve and decline", len(asked.Options))
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
	}, approvalgate.PermissionQuestion{Confirmation: "보낼까요?"})
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
	answer, isAnswered := permissionRelay.AskPermission(ctx, mcpserver.ApprovalRequest{
		Platform:       "buzz",
		ConversationID: "conversation-1",
		TaskRunID:      "task-1",
		ToolName:       "message_send",
	}, approvalgate.PermissionQuestion{Confirmation: "보낼까요?"})
	if !isAnswered || answer.Signal != agentcontract.ApprovalSignalReject {
		t.Fatalf("declining read as %q answered=%v", answer.Signal, isAnswered)
	}
}

type scriptedReader struct {
	optionID string
	asked    *[]approvalreply.Question
}

func (reader scriptedReader) Read(_ context.Context, question approvalreply.Question, reply string, observe agentcontract.LLMCallObserver) (string, bool, error) {
	if reader.asked != nil {
		*reader.asked = append(*reader.asked, question)
	}
	observe(agentcontract.LLMCallRecord{Kind: agentcontract.LLMCallKindDecision, DecidedMessageIDs: []string{reply}})
	return reader.optionID, reader.optionID != "", nil
}

func approvalSignalPointer(approvalSignal agentcontract.ApprovalSignal) *agentcontract.ApprovalSignal {
	return &approvalSignal
}

func answeringWithWords(t *testing.T, connection *acp.ClientSideConnection, sessionID acp.SessionId, words string) func(acp.RequestPermissionRequest) acp.PermissionOptionId {
	t.Helper()
	return answeringWithReply(t, connection, sessionID, ApprovalReplyRequest{Reply: words})
}

func answeringWithReply(t *testing.T, connection *acp.ClientSideConnection, sessionID acp.SessionId, reply ApprovalReplyRequest) func(acp.RequestPermissionRequest) acp.PermissionOptionId {
	t.Helper()
	return func(request acp.RequestPermissionRequest) acp.PermissionOptionId {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		reply.SessionID = string(sessionID)
		reply.ToolCallID = string(request.ToolCall.ToolCallId)
		answer, errorValue := connection.CallExtension(ctx, ApprovalReplyExtensionMethod, reply)
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
	asked := []approvalreply.Question{}
	client := &recordingClient{}
	connection, permissionRelay := connectedPairWithReader(t, &recordingLauncher{}, client, scriptedReader{optionID: string(approveOnceOptionID), asked: &asked})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	client.answerByAsking = answeringWithWords(t, connection, sessionID, "응 보내줘")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	answer, isAnswered := permissionRelay.AskPermission(ctx, approvalRequestForTest(), approvalgate.PermissionQuestion{Confirmation: "박예시에게 보낼까요?"})

	if !isAnswered || answer.Signal != agentcontract.ApprovalSignalApprove {
		t.Fatalf("the answer read as %q answered=%v", answer.Signal, isAnswered)
	}
	if len(asked) != 1 || asked[0].Text != "박예시에게 보낼까요?" {
		t.Fatalf("the reader was asked %+v, expected once about the question the person answered", asked)
	}
}

func TestAnAnswerTheReaderCannotReadIsNotAnAnswer(t *testing.T) {
	client := &recordingClient{}
	connection, permissionRelay := connectedPairWithReader(t, &recordingLauncher{}, client, scriptedReader{})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	client.answerByAsking = answeringWithWords(t, connection, sessionID, "음")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	answer, isAnswered := permissionRelay.AskPermission(ctx, approvalRequestForTest(), approvalgate.PermissionQuestion{Confirmation: "보낼까요?"})

	if isAnswered {
		t.Fatalf("a reply the reader could not read decided the call as %q", answer.Signal)
	}
}

func TestAReplyOutsideTheQuestionsThreadIsNotAnAnswerAndIsNotRead(t *testing.T) {
	asked := []approvalreply.Question{}
	client := &recordingClient{}
	connection, permissionRelay := connectedPairWithReader(t, &recordingLauncher{}, client, scriptedReader{optionID: string(approveOnceOptionID), asked: &asked})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	client.answerByAsking = answeringWithReply(t, connection, sessionID, ApprovalReplyRequest{Reply: "ㅇ", ReplyTargetID: "conversation-1:other-root", IsThread: true})
	approvalRequest := approvalRequestForTest()
	approvalRequest.ReplyTargetID = "conversation-1:question-root"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, isAnswered := permissionRelay.AskPermission(ctx, approvalRequest, approvalgate.PermissionQuestion{Confirmation: "보낼까요?"})

	if isAnswered || len(asked) != 0 {
		t.Fatalf("a reply in another thread answered=%v and the reader was asked %d times", isAnswered, len(asked))
	}
}

func TestTheCallThatReadAReplyIsRecordedInTheWaitingRunsLedger(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-sample", "conversation-1", "박예시한테 DM 보내줘")
	client := &recordingClient{}
	connection, permissionRelay := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: &recordingLauncher{},
		Directory:    staticDirectory{},
		ReplyReader:  scriptedReader{optionID: string(approveOnceOptionID)},
		TaskRunStore: taskRunService,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	client.answerByAsking = answeringWithReply(t, connection, sessionID, ApprovalReplyRequest{Reply: "ㅇ", MessageID: "message-reply"})
	approvalRequest := approvalRequestForTest()
	approvalRequest.TaskRunID = taskRun.TaskRunID

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	permissionRelay.AskPermission(ctx, approvalRequest, approvalgate.PermissionQuestion{Confirmation: "보낼까요?"})

	calls := []agentcontract.TaskEvent{}
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRun.TaskRunID) {
		if taskEvent.Name == agentcontract.TaskEventLLMCall {
			calls = append(calls, taskEvent)
		}
	}
	if len(calls) != 1 || !strings.Contains(calls[0].Body, "ㅇ") {
		t.Fatalf("the ledger of the run that asked holds %+v, expected the call that read the reply", calls)
	}
}

func TestAnAnswerToACallNobodyIsWaitingOnIsRefused(t *testing.T) {
	client := &recordingClient{}
	connection, _ := connectedPairWithReader(t, &recordingLauncher{}, client, scriptedReader{optionID: string(approveOnceOptionID)})
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
		ReplyReader:  scriptedReader{},
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

func TestAReissuedQuestionThatWasNeverPostedIsLeftForTheRelayToPost(t *testing.T) {
	asked := reissuedQuestionAfterRecording(t, nil)

	if deliveryOf(t, asked).AlreadyPosted {
		t.Fatal("a question nobody posted was reissued as already posted, so nobody would ever see it")
	}
}

func TestAReissuedQuestionThatWasPostedIsNotPostedTwice(t *testing.T) {
	asked := reissuedQuestionAfterRecording(t, func(taskRunService *task.TaskRunService, taskRunID string) {
		taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventConnectorReplySent, `{"replyKind":"approval_question","dispatchID":"question-message"}`)
	})

	if !deliveryOf(t, asked).AlreadyPosted {
		t.Fatal("a question the person already has was reissued without saying so, so it is posted a second time")
	}
}

func reissuedQuestionAfterRecording(t *testing.T, record func(*task.TaskRunService, string)) acp.RequestPermissionRequest {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	waitingRun := runWaitingOnAHeldCall(t, taskRunService, "conversation-1", heldCallForTest())
	if record != nil {
		record(taskRunService, waitingRun.TaskRunID)
	}
	client := &recordingClient{permissionAskedSignal: make(chan acp.RequestPermissionRequest, 4)}
	connection := reconnectedPair(t, &recordingLauncher{}, client, taskRunService)
	if errorValue := loadSessionForTest(t, connection, "session-the-relay-still-holds", sessionMeta("sample@example.test", "conversation-1")); errorValue != nil {
		t.Fatalf("load session: %v", errorValue)
	}
	return awaitPermissionRequest(t, client)
}

func deliveryOf(t *testing.T, request acp.RequestPermissionRequest) Delivery {
	t.Helper()
	document, errorValue := json.Marshal(request.Meta[DeliveryMetaKey])
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	delivery := Delivery{}
	if errorValue := json.Unmarshal(document, &delivery); errorValue != nil {
		t.Fatal(errorValue)
	}
	return delivery
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
	approvalrecord.SettleSignal(taskRunService, answeredRun.TaskRunID, approvalSignalPointer(agentcontract.ApprovalSignalApprove), "acp_permission")
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

func TestAChoiceIsReadFromThePersonsWordsAgainstTheOfferedOptions(t *testing.T) {
	asked := []approvalreply.Question{}
	client := &recordingClient{}
	connection, permissionRelay := connectedPairWithReader(t, &recordingLauncher{}, client, scriptedReader{optionID: "choose:offHours", asked: &asked})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	client.answerByAsking = answeringWithWords(t, connection, sessionID, "새벽에 해")
	choices := []approvalrecord.Choice{{Key: "offHours", StartsAt: "2099-10-03T03:00:00+09:00"}, {Key: "now"}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	answer, isAnswered := permissionRelay.AskPermission(ctx, approvalRequestForTest(), approvalgate.PermissionQuestion{Confirmation: "업데이트할까요?", Choices: choices})

	if !isAnswered || answer.ChoiceKey != "offHours" {
		t.Fatalf("the choice read as %+v answered=%v", answer, isAnswered)
	}
	offered := client.permissionAsked[0].Options
	if len(offered) != 3 || offered[0].OptionId != "choose:offHours" || offered[1].OptionId != "choose:now" || offered[2].OptionId != rejectOnceOptionID {
		t.Fatalf("the client was offered %+v, expected the later time, now, and declining, in that order", offered)
	}
	if len(asked) != 1 || len(asked[0].Options) != 3 {
		t.Fatalf("the reader was asked %+v, expected one question among the offered options and cancelling", asked)
	}
}

func TestTheReaderIsOfferedTheOptionsTheClientWasSentWithTheirMeanings(t *testing.T) {
	sent := permissionOptions(nil)

	offered := readerOptionsOf(sent)

	if len(offered) != 2 || offered[0].ID != string(approveOnceOptionID) || offered[1].ID != string(rejectOnceOptionID) {
		t.Fatalf("expected the ids the client was sent, got %+v", offered)
	}
	if offered[0].Meaning != approvalreply.AllowMeaning(sent[0].Name) || offered[1].Meaning != approvalreply.RejectMeaning {
		t.Fatalf("expected an allowing option to mean going ahead with its name and a rejecting one to mean declining, got %+v", offered)
	}
}
