package connectors

import (
	"context"
	"reflect"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

type approvingTurnRouter struct{}

func (router approvingTurnRouter) Plan(ctx context.Context, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	return router.PlanObserved(ctx, request, nil)
}

func (approvingTurnRouter) PlanObserved(context.Context, agentcontract.AgentRequest, *agentcontract.IntakeCallLedger) (agentcontract.TurnDecision, error) {
	approval := agentcontract.ApprovalSignalApprove
	return agentcontract.TurnDecision{Route: agentcontract.TurnRouteContinueTask, Approval: &approval}, nil
}

func TestASessionTurnLeavesARunWaitingOnApprovalToTheSessionThatAsked(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	harness := harnesstest.New(taskRunService)
	connectorRuntime, _ := connectorRuntimeForHarness(t, harness, harnessGateway(harness), harness, approvingTurnRouter{}, taskRunService, testLanguageModel{reply: "stub"})
	running := seedAbandonedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "박예시한테 DM 보내줘")
	if _, errorValue := connectorRuntime.taskRunService.PauseTaskRun(running.TaskRunID, task.TaskStatusWaitingApproval, "박예시에게 보낼까요?"); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-yes")
	event.Prompt = "응 보내"
	isThread := false
	event.IsThread = &isThread
	sessionTurn := connectorRuntime.OpenSessionTurn(context.Background(), event, "person-1", func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		t.Fatal("the session turn answered a message whose approval the session asks for itself")
		return "", nil
	})

	launchRequest, isAnswered, errorValue := sessionTurn.ContinueOpenInteractions(context.Background(), agentruntime.TaskLaunchRequest{Prompt: event.Prompt})

	if errorValue != nil || isAnswered {
		t.Fatalf("the turn ended in settlement: answered=%v error=%v", isAnswered, errorValue)
	}
	if launchRequest.ExistingTaskRunID != "" {
		t.Fatalf("the message continued %q as an approval, which would carry out a call the session is still asking about", launchRequest.ExistingTaskRunID)
	}
}

func TestSessionTurnGatewayFactsMatchConnectorFactsAndScopeOpenTasks(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	connectorRuntime.UseAgentIdentityProvider(func() agentcontract.AgentIdentity {
		return agentcontract.AgentIdentity{Name: "김인턴", Handle: "internkim"}
	})
	company := agentcontract.CompanyContext{Name: "주식회사 여명거리", TimeZone: "Asia/Seoul"}
	connectorRuntime.UseCompanyProvider(func() agentcontract.CompanyContext { return company })

	priorTask := connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{
		ConversationID: "direct-1",
		ReplyTargetID:  "thread-current",
		IsThread:       true,
	}, "이전 작업")
	if _, errorValue := connectorRuntime.taskRunService.CompleteTaskRun(priorTask.TaskRunID, "완료"); errorValue != nil {
		t.Fatal(errorValue)
	}
	seedWaitingQuestionInThread(t, connectorRuntime, "thread-current")
	event := threadReply("message-current", "thread-current")
	event.Context = VisibleContext{
		ConversationType: "direct",
		Messages:         []VisibleContextMessage{{Speaker: "이샘플", SpeakerHandle: "sample", Text: "이전 메시지"}},
		Sender: VisibleContextSender{
			Platform:    "test",
			SenderID:    "sender-user",
			Name:        "이샘플",
			CallingName: "샘플",
			Handle:      "sample",
		},
		InputAttachments: []InputAttachment{{Platform: "test", URL: "https://example.test/report.csv", Filename: "report.csv"}},
	}
	event.InputParts = []agentcontract.AgentPart{{
		Type: agentcontract.AgentPartTypeFile,
		File: &agentcontract.AgentFilePart{Filename: "report.csv", ContentType: "text/csv", SizeBytes: 42},
	}}

	sessionFacts := connectorRuntime.gatewayFactsForTurn(context.Background(), connectorRuntime.OpenSessionTurn(context.Background(), event, "person-1", nil).turn)
	connectorFacts := connectorRuntime.inboundGatewayFacts(context.Background(), adapter, event)

	if !reflect.DeepEqual(sessionFacts.AgentIdentity, connectorFacts.AgentIdentity) || sessionFacts.AgentIdentity.Name != "김인턴" || sessionFacts.AgentIdentity.Handle != "internkim" {
		t.Fatalf("the session facts lost the configured agent identity: %+v", sessionFacts.AgentIdentity)
	}
	if !reflect.DeepEqual(sessionFacts.Messages, connectorFacts.Messages) {
		t.Fatalf("the session facts differed from the connector's: session=%+v connector=%+v", sessionFacts.Messages, connectorFacts.Messages)
	}
	if sessionFacts.Messages[0].SenderName != "이샘플" || sessionFacts.Messages[0].SenderHandle != "sample" || len(sessionFacts.Messages[0].InputParts) != 1 || len(sessionFacts.Messages[0].Attachments) != 1 {
		t.Fatalf("the session facts lost sender or attachment facts: %+v", sessionFacts.Messages[0])
	}
	if !reflect.DeepEqual(sessionFacts.VisibleContext, event.Context.ToAgentVisibleContext()) || !reflect.DeepEqual(sessionFacts.Company, company) {
		t.Fatalf("the session facts lost the current context or company: context=%+v company=%+v", sessionFacts.VisibleContext, sessionFacts.Company)
	}
	if sessionFacts.OpenTask == nil || sessionFacts.OpenTask.PostedQuestion != "Set a Monday reminder to send it?" || sessionFacts.FinishedTask != nil {
		t.Fatalf("the session facts lost the waiting question: open=%+v finished=%+v", sessionFacts.OpenTask, sessionFacts.FinishedTask)
	}

	rootEvent := event
	isThread := false
	rootEvent.IsThread = &isThread
	rootEvent.ReplyTargetID = "message-root"
	rootFacts := connectorRuntime.gatewayFactsForTurn(context.Background(), connectorRuntime.OpenSessionTurn(context.Background(), rootEvent, "person-1", nil).turn)
	if rootFacts.OpenTask != nil || rootFacts.FinishedTask != nil {
		t.Fatalf("a root message inherited another thread's task: open=%+v finished=%+v", rootFacts.OpenTask, rootFacts.FinishedTask)
	}
	if !reflect.DeepEqual(rootFacts.Messages, sessionFacts.Messages) || !reflect.DeepEqual(rootFacts.VisibleContext, sessionFacts.VisibleContext) {
		t.Fatalf("changing thread scope changed the current message facts: messages=%+v context=%+v", rootFacts.Messages, rootFacts.VisibleContext)
	}
}
