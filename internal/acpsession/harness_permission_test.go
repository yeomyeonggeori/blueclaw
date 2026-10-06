package acpsession

import (
	"context"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

func harnessQuestionForTest() approvalgate.HarnessPermissionQuestion {
	title := "Force-push the branch to origin?"
	return approvalgate.HarnessPermissionQuestion{
		Text:     title,
		ToolCall: acp.ToolCallUpdate{ToolCallId: "harness-call-1", Title: &title, RawInput: map[string]any{"command": "git push --force"}},
		Options: []acp.PermissionOption{
			{OptionId: "allow-once", Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow"},
			{OptionId: "reject-once", Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject"},
		},
	}
}

type harnessAnswer struct {
	outcome    acp.RequestPermissionOutcome
	isAnswered bool
}

func askHarnessPermissionInBackground(permissionRelay *PermissionRelay, ctx context.Context) <-chan harnessAnswer {
	answers := make(chan harnessAnswer, 1)
	go func() {
		outcome, isAnswered := answered(permissionRelay.AskHarnessPermission(ctx, approvalRequestForTest(), harnessQuestionForTest()))
		answers <- harnessAnswer{outcome: outcome, isAnswered: isAnswered}
	}()
	return answers
}

func TestAHarnessQuestionIsPutToThePersonWithTheOptionsTheHarnessOffered(t *testing.T) {
	client := &recordingClient{permissionAskedSignal: make(chan acp.RequestPermissionRequest, 4)}
	connection, permissionRelay := connectedPairWithReader(t, &recordingLauncher{}, client, scriptedReader{})
	openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	client.answerByAsking = func(acp.RequestPermissionRequest) acp.PermissionOptionId { return "allow-once" }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	approvalRequest := approvalRequestForTest()
	approvalRequest.ReplyTargetID = "buzz:conversation-1:thread-root"
	permissionRelay.AskHarnessPermission(ctx, approvalRequest, harnessQuestionForTest())

	asked := awaitPermissionRequest(t, client)
	if asked.ToolCall.Title == nil || *asked.ToolCall.Title != "Force-push the branch to origin?" || asked.ToolCall.ToolCallId != "harness-call-1" {
		t.Fatalf("the person was asked %+v, expected the harness's own question", asked.ToolCall)
	}
	if len(asked.Options) != 2 || asked.Options[0].OptionId != "allow-once" || asked.Options[1].OptionId != "reject-once" {
		t.Fatalf("the person was offered %+v, expected exactly what the harness offered", asked.Options)
	}
	delivery, isNamed := deliveryNamedIn(asked.Meta)
	if !isNamed || delivery.DeliveryID == "" || delivery.ReplyTargetID != "buzz:conversation-1:thread-root" {
		t.Fatalf("the question was not addressed to the thread it came from: %+v", delivery)
	}
}

func TestTheOptionThePersonChoseIsReturnedToTheHarness(t *testing.T) {
	client := &recordingClient{}
	connection, permissionRelay := connectedPairWithReader(t, &recordingLauncher{}, client, scriptedReader{optionID: "allow-once"})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	client.answerByAsking = answeringWithWords(t, connection, sessionID, "yes, push it")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outcome, isAnswered := answered(permissionRelay.AskHarnessPermission(ctx, approvalRequestForTest(), harnessQuestionForTest()))

	if !isAnswered || outcome.Selected == nil || outcome.Selected.OptionId != "allow-once" {
		t.Fatalf("expected the harness to be answered with allow-once, got %+v answered=%v", outcome, isAnswered)
	}
}

func TestAnAnswerNamingNoOptionTheHarnessOfferedIsNotAnAnswer(t *testing.T) {
	client := &recordingClient{}
	connection, permissionRelay := connectedPairWithReader(t, &recordingLauncher{}, client, scriptedReader{})
	openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	client.answerByAsking = func(acp.RequestPermissionRequest) acp.PermissionOptionId { return approveOnceOptionID }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outcome, isAnswered := answered(permissionRelay.AskHarnessPermission(ctx, approvalRequestForTest(), harnessQuestionForTest()))

	if isAnswered {
		t.Fatalf("an option the harness never offered decided its call: %+v", outcome)
	}
}

func TestAReplyThatIsNotAnAnswerKeepsTheHarnessQuestionOpen(t *testing.T) {
	client := &recordingClient{}
	connection, permissionRelay := connectedPairWithReader(t, &recordingLauncher{}, client, scriptedReader{})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))
	personAnswers := make(chan acp.PermissionOptionId)
	client.answerByAsking = func(request acp.RequestPermissionRequest) acp.PermissionOptionId {
		answeringWithWords(t, connection, sessionID, "hmm")(request)
		return <-personAnswers
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	answers := askHarnessPermissionInBackground(permissionRelay, ctx)

	select {
	case answer := <-answers:
		t.Fatalf("a reply that is not an answer closed the question: %+v", answer)
	case <-time.After(500 * time.Millisecond):
	}
	personAnswers <- "reject-once"
	answer := <-answers
	if !answer.isAnswered || answer.outcome.Selected.OptionId != "reject-once" {
		t.Fatalf("expected the later clear answer to decide, got %+v", answer)
	}
}

func TestAHarnessQuestionNobodyCanBeAskedIsNotAnswered(t *testing.T) {
	permissionRelay := NewPermissionRelay(silentLogger())

	outcome, isAnswered := answered(permissionRelay.AskHarnessPermission(context.Background(), approvalRequestForTest(), harnessQuestionForTest()))

	if isAnswered {
		t.Fatalf("a conversation nobody holds answered: %+v", outcome)
	}
}

func runWithAnUnansweredHarnessQuestion(t *testing.T, taskRunService *task.TaskRunService) string {
	t.Helper()
	taskRun := taskRunService.CreateTaskRun("person-sample", "conversation-1", "push the branch")
	gate := approvalgate.New(taskRunService)
	gate.UsePermissionAsker(processEndedMidQuestion{})
	approvalRequest := approvalRequestForTest()
	approvalRequest.TaskRunID = taskRun.TaskRunID
	if _, isAnswered := answered(gate.AskHarnessPermission(context.Background(), approvalRequest, harnessQuestionForTest())); isAnswered {
		t.Fatal("the process ended before anyone could answer")
	}
	return taskRun.TaskRunID
}

func TestAHarnessQuestionTheRestartInterruptedIsAskedAgainOnce(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	runWithAnUnansweredHarnessQuestion(t, taskRunService)
	client := &recordingClient{permissionAskedSignal: make(chan acp.RequestPermissionRequest, 4)}
	connection := reconnectedPair(t, &recordingLauncher{}, client, taskRunService)

	if errorValue := loadSessionForTest(t, connection, "session-after-restart", sessionMeta("sample@example.test", "conversation-1")); errorValue != nil {
		t.Fatalf("load session: %v", errorValue)
	}

	asked := awaitPermissionRequest(t, client)
	if asked.ToolCall.Title == nil || *asked.ToolCall.Title != "Force-push the branch to origin?" {
		t.Fatalf("the reissued question was %+v, expected the harness's own", asked.ToolCall.Title)
	}
	expectNobodyIsAskedAgain(t, client)
}

func TestApprovingTheReissuedHarnessQuestionRecordsItAndResumesTheRun(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunID := runWithAnUnansweredHarnessQuestion(t, taskRunService)
	launcher := &recordingLauncher{launchedSignal: make(chan agentruntime.TaskLaunchRequest, 4)}
	client := &recordingClient{permissionAskedSignal: make(chan acp.RequestPermissionRequest, 4), permissionChoice: approveOnceOptionID}
	connection := reconnectedPair(t, launcher, client, taskRunService)

	if errorValue := loadSessionForTest(t, connection, "session-after-restart", sessionMeta("sample@example.test", "conversation-1")); errorValue != nil {
		t.Fatalf("load session: %v", errorValue)
	}

	select {
	case resumed := <-launcher.launchedSignal:
		if resumed.ExistingTaskRunID != taskRunID {
			t.Fatalf("resumed %q, expected %q", resumed.ExistingTaskRunID, taskRunID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the answered question never resumed its run")
	}
	holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRunID))
	if len(holds) != 1 || holds[0].State != holdrecord.StateApproved {
		t.Fatalf("expected the hold approved so the harness's retry runs on it, got %+v", holds)
	}
}

func TestAHarnessQuestionThatWasPostedIsNotPostedAgainAfterARestart(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunID := runWithAnUnansweredHarnessQuestion(t, taskRunService)
	taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventConnectorReplySent, `{"replyKind":"approval_question","dispatchID":"question-message"}`)
	client := &recordingClient{permissionAskedSignal: make(chan acp.RequestPermissionRequest, 4)}
	connection := reconnectedPair(t, &recordingLauncher{}, client, taskRunService)

	if errorValue := loadSessionForTest(t, connection, "session-after-restart", sessionMeta("sample@example.test", "conversation-1")); errorValue != nil {
		t.Fatalf("load session: %v", errorValue)
	}

	if !deliveryOf(t, awaitPermissionRequest(t, client)).IsAlreadyPosted {
		t.Fatal("a question the person already has was reissued as unposted")
	}
}

type processEndedMidQuestion struct{}

func (processEndedMidQuestion) AskPermission(context.Context, mcpserver.ApprovalRequest, approvalgate.PermissionQuestion) (approvalgate.ApprovalAnswer, approvalgate.AskStatus) {
	return approvalgate.ApprovalAnswer{}, approvalgate.AskInterrupted
}

func (processEndedMidQuestion) AskHarnessPermission(context.Context, mcpserver.ApprovalRequest, approvalgate.HarnessPermissionQuestion) (acp.RequestPermissionOutcome, approvalgate.AskStatus) {
	return acp.RequestPermissionOutcome{}, approvalgate.AskInterrupted
}
