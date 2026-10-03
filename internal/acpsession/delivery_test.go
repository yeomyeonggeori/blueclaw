package acpsession

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
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

func TestAReplyCarryingAFileNamesItsTypeAndIsRecordedSentOnceTheRelayPostsEach(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	answered := taskRunService.CreateTaskRun("person-sample", "conversation-1", "한 장짜리 PDF 만들어줘")
	client := &recordingClient{}
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: &recordingLauncher{
			reply:     "만들었습니다",
			taskRunID: answered.TaskRunID,
			attachments: []toolcontract.FileAttachment{{
				DevicePath:  "/workspace/private/people/person-sample/분기 보고 100%.pdf",
				Filename:    "분기 보고 100%.pdf",
				ContentType: "application/pdf",
				SizeBytes:   2048,
			}},
		},
		Directory:    staticDirectory{},
		TurnRouter:   scriptedRouter{},
		TaskRunStore: taskRunService,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptInThread(t, connection, sessionID, "buzz:conversation-1:message-7")

	if len(client.resourceLinks) != 1 {
		t.Fatalf("the reply handed over %d files, expected the one it made", len(client.resourceLinks))
	}
	link := client.resourceLinks[0]
	if link.Name != "분기 보고 100%.pdf" || link.MimeType == nil || *link.MimeType != "application/pdf" || link.Size == nil || *link.Size != 2048 {
		t.Fatalf("the file was handed over as %+v, expected its name, application/pdf and 2048 bytes", link)
	}
	if located, errorValue := url.Parse(link.Uri); errorValue != nil || located.Scheme != "file" || located.Path != "/workspace/private/people/person-sample/분기 보고 100%.pdf" {
		t.Fatalf("the file was named by %q, which does not read back as the path it lives at", link.Uri)
	}
	if len(client.deliveries) != 2 {
		t.Fatalf("the relay was asked to confirm %d posts, expected the words and the file each", len(client.deliveries))
	}
	sent := connectorRepliesRecorded(t, taskRunService, answered.TaskRunID, agentcontract.TaskEventConnectorReplySent)
	if len(sent) != 1 || sent[0].DispatchID != "posted-2" {
		t.Fatalf("the reply is recorded as sent %+v, expected once, as the message carrying its words, posted after its file", sent)
	}
}

func TestAReplyWhoseFileTheRelayCouldNotPostIsRecordedUndelivered(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	answered := taskRunService.CreateTaskRun("person-sample", "conversation-1", "한 장짜리 PDF 만들어줘")
	client := &recordingClient{undeliveredBecause: "the messenger refused report.pdf with 413"}
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: &recordingLauncher{
			taskRunID:   answered.TaskRunID,
			attachments: []toolcontract.FileAttachment{{DevicePath: "/workspace/private/people/person-sample/report.pdf", Filename: "report.pdf", ContentType: "application/pdf"}},
		},
		Directory:    staticDirectory{},
		TurnRouter:   scriptedRouter{},
		TaskRunStore: taskRunService,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptInThread(t, connection, sessionID, "buzz:conversation-1:message-7")

	if sent := connectorRepliesRecorded(t, taskRunService, answered.TaskRunID, agentcontract.TaskEventConnectorReplySent); len(sent) != 0 {
		t.Fatalf("a file the relay could not post is recorded as sent: %+v", sent)
	}
	failed := connectorRepliesRecorded(t, taskRunService, answered.TaskRunID, agentcontract.TaskEventConnectorReplyFailed)
	if replies := repliesOfKind(failed, "success"); len(replies) != 1 || !strings.Contains(replies[0].Reason, "refused report.pdf with 413") {
		t.Fatalf("the reply is recorded as failed %+v, expected once, with the reason the relay gave", failed)
	}
	if notices := repliesOfKind(failed, "delivery_failure_notice"); len(notices) != 1 {
		t.Fatalf("a relay that posts nothing failed the notice about the file %d times, expected the notice tried once and recorded failed", len(notices))
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

type deliveryFailureReportRecord struct {
	Phase  string                      `json:"phase"`
	Report agentcontract.FailureReport `json:"report"`
}

func deliveryFailureReportsRecorded(t *testing.T, taskRunService *task.TaskRunService, taskRunID string) []agentcontract.FailureReport {
	t.Helper()
	reports := []agentcontract.FailureReport{}
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name != agentcontract.TaskEventAgentFailureReport {
			continue
		}
		record := deliveryFailureReportRecord{}
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &record); errorValue != nil {
			t.Fatalf("failure report body: %v", errorValue)
		}
		if record.Phase == "delivery" {
			reports = append(reports, record.Report)
		}
	}
	return reports
}

func repliesOfKind(records []connectorReplyRecord, replyKind string) []connectorReplyRecord {
	matching := []connectorReplyRecord{}
	for _, record := range records {
		if record.ReplyKind == replyKind {
			matching = append(matching, record)
		}
	}
	return matching
}

func promptForFiles(t *testing.T, client *recordingClient, taskRunService *task.TaskRunService, taskRunID string, words string, attachments []toolcontract.FileAttachment) {
	t.Helper()
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: &recordingLauncher{reply: words, taskRunID: taskRunID, attachments: attachments},
		Directory:    staticDirectory{},
		TurnRouter:   scriptedRouter{},
		TaskRunStore: taskRunService,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	promptInThread(t, connection, sessionID, "buzz:conversation-1:message-7")
}

func samplePDF(filename string) toolcontract.FileAttachment {
	return toolcontract.FileAttachment{DevicePath: "/workspace/private/people/person-sample/" + filename, Filename: filename, ContentType: "application/pdf"}
}

func TestAReplyPostsItsFilesBeforeTheWordsThatDescribeThem(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	answered := taskRunService.CreateTaskRun("person-sample", "conversation-1", "견적서와 거래명세서 만들어줘")
	client := &recordingClient{}

	promptForFiles(t, client, taskRunService, answered.TaskRunID, "두 파일을 첨부했습니다", []toolcontract.FileAttachment{samplePDF("quote.pdf"), samplePDF("statement.pdf")})

	expected := []string{"file:quote.pdf", "file:statement.pdf", "words:두 파일을 첨부했습니다"}
	if strings.Join(client.posted, "|") != strings.Join(expected, "|") {
		t.Fatalf("the reply was posted as %v, expected its files before its words %v", client.posted, expected)
	}
	if sent := connectorRepliesRecorded(t, taskRunService, answered.TaskRunID, agentcontract.TaskEventConnectorReplySent); len(sent) != 1 || sent[0].DispatchID != "posted-3" {
		t.Fatalf("the reply is recorded as sent %+v, expected once, as the message carrying its words (posted-3)", sent)
	}
}

func TestAReplyWhoseFileDidNotReachThePersonPostsNoWordsAndANoticeNamingTheFile(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	answered := taskRunService.CreateTaskRun("person-sample", "conversation-1", "한 장짜리 PDF 만들어줘")
	client := &recordingClient{refusedFiles: map[string]string{"report.pdf": "the messenger refused report.pdf with 413"}}

	promptForFiles(t, client, taskRunService, answered.TaskRunID, "PDF를 첨부했습니다", []toolcontract.FileAttachment{samplePDF("report.pdf")})

	for _, posted := range client.posted {
		if posted == "words:PDF를 첨부했습니다" {
			t.Fatalf("the words written for a file that never arrived were posted anyway: %v", client.posted)
		}
	}
	failed := repliesOfKind(connectorRepliesRecorded(t, taskRunService, answered.TaskRunID, agentcontract.TaskEventConnectorReplyFailed), "success")
	if len(failed) != 1 || !strings.Contains(failed[0].Reason, "refused report.pdf with 413") {
		t.Fatalf("the reply is recorded as failed %+v, expected once, with the reason the relay gave", failed)
	}
	notices := repliesOfKind(connectorRepliesRecorded(t, taskRunService, answered.TaskRunID, agentcontract.TaskEventConnectorReplySent), "delivery_failure_notice")
	if len(notices) != 1 {
		t.Fatalf("the person was told of the missing file %d times, expected once", len(notices))
	}
	reports := deliveryFailureReportsRecorded(t, taskRunService, answered.TaskRunID)
	if len(reports) != 1 {
		t.Fatalf("the notice was written from %d delivery reports, expected one", len(reports))
	}
	report := reports[0]
	if !report.ArtifactRequired || len(report.AttachmentFilenames) != 0 || !strings.Contains(report.SafeFailureSummary, "report.pdf") || report.OriginalRequest == "" {
		t.Fatalf("the notice was handed %+v, expected it to name report.pdf as not delivered, nothing as delivered, and the request", report)
	}
}

func TestAReplyWhoseSecondFileDidNotArriveNamesTheFirstAsDelivered(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	answered := taskRunService.CreateTaskRun("person-sample", "conversation-1", "계약서와 부속서 만들어줘")
	client := &recordingClient{refusedFiles: map[string]string{"annex.pdf": "chatd did not answer"}}

	promptForFiles(t, client, taskRunService, answered.TaskRunID, "두 파일을 첨부했습니다", []toolcontract.FileAttachment{samplePDF("contract.pdf"), samplePDF("annex.pdf")})

	reports := deliveryFailureReportsRecorded(t, taskRunService, answered.TaskRunID)
	if len(reports) != 1 {
		t.Fatalf("the notice was written from %d delivery reports, expected one", len(reports))
	}
	report := reports[0]
	if strings.Join(report.AttachmentFilenames, ",") != "contract.pdf" || !strings.Contains(report.SafeFailureSummary, "annex.pdf") || strings.Contains(report.SafeFailureSummary, "contract.pdf") {
		t.Fatalf("the notice was handed %+v, expected contract.pdf as delivered and annex.pdf as not", report)
	}
}
