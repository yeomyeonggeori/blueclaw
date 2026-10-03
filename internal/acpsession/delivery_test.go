package acpsession

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type connectorReplyRecord struct {
	ReplyKind  string `json:"replyKind"`
	DispatchID string `json:"dispatchID"`
	Reason     string `json:"reason"`
}

func connectorRepliesRecorded(t *testing.T, taskRunService *task.TaskRunService, taskRunID string, name string) []connectorReplyRecord {
	t.Helper()
	records := []connectorReplyRecord{}
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name != name {
			continue
		}
		record := connectorReplyRecord{}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &record); errorValue != nil {
			t.Fatalf("%s body: %v", name, errorValue)
		}
		records = append(records, record)
	}
	return records
}

func promptInThread(t *testing.T, connection *acp.ClientSideConnection, sessionID acp.SessionId, replyTargetID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, errorValue := connection.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock("박예시한테 DM 보내줘")},
		Meta: map[string]any{MessageMetaKey: map[string]any{
			"messageID":     "message-7",
			"replyTargetID": replyTargetID,
			"context":       map[string]any{"conversationType": "dm"},
		}},
	}); errorValue != nil {
		t.Fatalf("prompt: %v", errorValue)
	}
}

func TestAReplyIsRecordedSentOnceWithTheMessageTheRelayPosted(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	answered := taskRunService.CreateTaskRun("person-sample", "conversation-1", "박예시한테 DM 보내줘")
	client := &recordingClient{}
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: &recordingLauncher{reply: "보냈습니다", taskRunID: answered.TaskRunID},
		Directory:    staticDirectory{},
		TurnRouter:   scriptedRouter{},
		TaskRunStore: taskRunService,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptInThread(t, connection, sessionID, "buzz:conversation-1:message-7")

	sent := connectorRepliesRecorded(t, taskRunService, answered.TaskRunID, agentcontract.TaskEventConnectorReplySent)
	if len(sent) != 1 || sent[0].DispatchID != "posted-1" {
		t.Fatalf("the reply is recorded as sent %+v, expected once, as the message the relay posted (posted-1)", sent)
	}
	if len(client.deliveries) != 1 || client.deliveries[0].ReplyTargetID != "buzz:conversation-1:message-7" {
		t.Fatalf("the reply named %+v, expected the thread of the message it answers", client.deliveries)
	}
}

func TestAReplyTheRelayCouldNotPostIsRecordedUndeliveredNotSent(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	answered := taskRunService.CreateTaskRun("person-sample", "conversation-1", "박예시한테 DM 보내줘")
	client := &recordingClient{undeliveredBecause: "chatd refused the post with 503"}
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: &recordingLauncher{reply: "보냈습니다", taskRunID: answered.TaskRunID},
		Directory:    staticDirectory{},
		TurnRouter:   scriptedRouter{},
		TaskRunStore: taskRunService,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptInThread(t, connection, sessionID, "buzz:conversation-1:message-7")

	if sent := connectorRepliesRecorded(t, taskRunService, answered.TaskRunID, agentcontract.TaskEventConnectorReplySent); len(sent) != 0 {
		t.Fatalf("a reply the relay could not post is recorded as sent: %+v", sent)
	}
	failed := connectorRepliesRecorded(t, taskRunService, answered.TaskRunID, agentcontract.TaskEventConnectorReplyFailed)
	if len(failed) != 1 || !strings.Contains(failed[0].Reason, "chatd refused the post with 503") {
		t.Fatalf("the reply is recorded as failed %+v, expected once, with the reason the relay gave", failed)
	}
}

func TestAReplyFromARunResumedWithNoTurnOpenNamesItsOwnThreadAndIsRecordedSent(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	heldCall := heldCallForTest()
	waitingRun := taskRunService.CreateTaskRunWithOrigin("person-sample", taskstate.TaskRunOrigin{ConversationID: "conversation-1", ReplyTargetID: "buzz:conversation-1:message-7"}, "박예시한테 DM 보내줘")
	if _, errorValue := taskRunService.PauseTaskRun(waitingRun.TaskRunID, agentcontract.TaskStatusWaitingApproval, heldCall.Confirmation); errorValue != nil {
		t.Fatalf("pause task run: %v", errorValue)
	}
	heldBody, _ := json.Marshal(heldCall)
	taskRunService.AppendTaskEvent(waitingRun.TaskRunID, agentcontract.TaskEventApprovalPendingCall, string(heldBody))
	launcher := &recordingLauncher{reply: "보냈습니다", launchedSignal: make(chan agentruntime.TaskLaunchRequest, 4)}
	client := &recordingClient{permissionChoice: approveOnceOptionID}
	connection := reconnectedPair(t, launcher, client, taskRunService)

	if errorValue := loadSessionForTest(t, connection, "session-the-relay-still-holds", sessionMeta("sample@example.test", "conversation-1")); errorValue != nil {
		t.Fatalf("load session: %v", errorValue)
	}

	sent := awaitConnectorReplies(t, taskRunService, waitingRun.TaskRunID, agentcontract.TaskEventConnectorReplySent)
	if len(sent) != 1 || sent[0].DispatchID == "" {
		t.Fatalf("the resumed run's reply is recorded as sent %+v, expected once, as the message the relay posted", sent)
	}
	client.mutex.Lock()
	defer client.mutex.Unlock()
	replyDeliveries := []Delivery{}
	for _, delivery := range client.deliveries {
		if delivery.DeliveryID != "" {
			replyDeliveries = append(replyDeliveries, delivery)
		}
	}
	if len(replyDeliveries) != 1 || replyDeliveries[0].ReplyTargetID != "buzz:conversation-1:message-7" {
		t.Fatalf("the resumed run's reply named %+v, expected the thread the run was asked in", replyDeliveries)
	}
}

func TestAnApprovalQuestionNamesItsThreadAndIsRecordedSentOnceTheRelayPostsIt(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	waitingRun := taskRunService.CreateTaskRun("person-sample", "conversation-1", "박예시한테 DM 보내줘")
	client := &recordingClient{permissionChoice: approveOnceOptionID}
	connection, permissionRelay := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: &recordingLauncher{},
		Directory:    staticDirectory{},
		TurnRouter:   scriptedRouter{},
		TaskRunStore: taskRunService,
	})
	openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	approvalRequest := approvalRequestForTest()
	approvalRequest.TaskRunID = waitingRun.TaskRunID
	approvalRequest.ReplyTargetID = "buzz:conversation-1:message-9"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, isAnswered := permissionRelay.AskPermission(ctx, approvalRequest, approvalgate.PermissionQuestion{Confirmation: "박예시에게 보낼까요?"}); !isAnswered {
		t.Fatal("nobody was asked")
	}

	if len(client.deliveries) != 1 || client.deliveries[0].ReplyTargetID != "buzz:conversation-1:message-9" {
		t.Fatalf("the question named %+v, expected the thread of the turn that asked it", client.deliveries)
	}
	sent := connectorRepliesRecorded(t, taskRunService, waitingRun.TaskRunID, agentcontract.TaskEventConnectorReplySent)
	if len(sent) != 1 || sent[0].DispatchID != "posted-1" || sent[0].ReplyKind != "approval_question" {
		t.Fatalf("the question is recorded as sent %+v, expected once, as the approval question the relay posted (posted-1)", sent)
	}
}

func TestAnApprovalQuestionTheRelayCouldNotPostIsRecordedUndelivered(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	waitingRun := taskRunService.CreateTaskRun("person-sample", "conversation-1", "박예시한테 DM 보내줘")
	client := &recordingClient{permissionChoice: approveOnceOptionID, undeliveredBecause: "chatd refused the post with 503"}
	connection, permissionRelay := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: &recordingLauncher{},
		Directory:    staticDirectory{},
		TurnRouter:   scriptedRouter{},
		TaskRunStore: taskRunService,
	})
	openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	approvalRequest := approvalRequestForTest()
	approvalRequest.TaskRunID = waitingRun.TaskRunID

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	permissionRelay.AskPermission(ctx, approvalRequest, approvalgate.PermissionQuestion{Confirmation: "박예시에게 보낼까요?"})

	if sent := connectorRepliesRecorded(t, taskRunService, waitingRun.TaskRunID, agentcontract.TaskEventConnectorReplySent); len(sent) != 0 {
		t.Fatalf("a question the relay could not post is recorded as sent: %+v", sent)
	}
	failed := connectorRepliesRecorded(t, taskRunService, waitingRun.TaskRunID, agentcontract.TaskEventConnectorReplyFailed)
	if len(failed) != 1 || !strings.Contains(failed[0].Reason, "chatd refused the post with 503") {
		t.Fatalf("the question is recorded as failed %+v, expected once, with the reason the relay gave", failed)
	}
}

func TestAReplyTheRelayNeverReportsOnIsRecordedUndelivered(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	answered := taskRunService.CreateTaskRun("person-sample", "conversation-1", "박예시한테 DM 보내줘")
	client := &recordingClient{isSilent: true}
	agent := NewAgent(Collaborators{
		TaskLauncher: &recordingLauncher{reply: "보냈습니다", taskRunID: answered.TaskRunID},
		Directory:    staticDirectory{},
		TurnRouter:   scriptedRouter{},
		TaskRunStore: taskRunService,
		SessionTurns: connectorRuntimeForTest(taskRunService),
	}, NewPermissionRelay(silentLogger()), silentLogger())
	agent.deliveryReportWait = 50 * time.Millisecond
	connection := connectAgentTo(t, agent, client)
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptInThread(t, connection, sessionID, "buzz:conversation-1:message-7")

	if sent := connectorRepliesRecorded(t, taskRunService, answered.TaskRunID, agentcontract.TaskEventConnectorReplySent); len(sent) != 0 {
		t.Fatalf("a reply nobody confirmed is recorded as sent: %+v", sent)
	}
	if failed := connectorRepliesRecorded(t, taskRunService, answered.TaskRunID, agentcontract.TaskEventConnectorReplyFailed); len(failed) != 1 {
		t.Fatalf("a reply nobody confirmed is recorded as failed %d times, expected once", len(failed))
	}
}

func awaitConnectorReplies(t *testing.T, taskRunService *task.TaskRunService, taskRunID string, name string) []connectorReplyRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if records := connectorRepliesRecorded(t, taskRunService, taskRunID, name); len(records) > 0 {
			return records
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func TestAReissuedQuestionNamesTheThreadItsRunWasAskedIn(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	heldCall := heldCallForTest()
	waitingRun := taskRunService.CreateTaskRunWithOrigin("person-sample", taskstate.TaskRunOrigin{ConversationID: "conversation-1", ReplyTargetID: "buzz:conversation-1:message-7"}, "박예시한테 DM 보내줘")
	if _, errorValue := taskRunService.PauseTaskRun(waitingRun.TaskRunID, agentcontract.TaskStatusWaitingApproval, heldCall.Confirmation); errorValue != nil {
		t.Fatalf("pause task run: %v", errorValue)
	}
	heldBody, _ := json.Marshal(heldCall)
	taskRunService.AppendTaskEvent(waitingRun.TaskRunID, agentcontract.TaskEventApprovalPendingCall, string(heldBody))
	client := &recordingClient{permissionAskedSignal: make(chan acp.RequestPermissionRequest, 4)}
	connection := reconnectedPair(t, &recordingLauncher{}, client, taskRunService)

	if errorValue := loadSessionForTest(t, connection, "session-the-relay-still-holds", sessionMeta("sample@example.test", "conversation-1")); errorValue != nil {
		t.Fatalf("load session: %v", errorValue)
	}

	asked := awaitPermissionRequest(t, client)
	delivery, isNamed := deliveryNamedIn(asked.Meta)
	if !isNamed || delivery.ReplyTargetID != "buzz:conversation-1:message-7" {
		t.Fatalf("the reissued question named %+v, expected the thread its run was asked in", delivery)
	}
}
