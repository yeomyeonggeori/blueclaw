package connectors

import (
	"context"
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
	connectorRuntime, _ := connectorRuntimeForHarness(t, harness, harness, harness, approvingTurnRouter{}, taskRunService, testLanguageModel{reply: "stub"})
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
	if launchRequest.IsApprovalContinuation || launchRequest.ExistingTaskRunID != "" {
		t.Fatalf("the message continued %q as an approval, which would carry out a call the session is still asking about", launchRequest.ExistingTaskRunID)
	}
}
