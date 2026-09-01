package agentruntime

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

type clockRecordingTurnRouter struct {
	routed agentcontract.AgentRequest
}

func (router *clockRecordingTurnRouter) Plan(
	ctx context.Context,
	request agentcontract.AgentRequest,
) (agentcontract.TurnDecision, error) {
	return router.PlanObserved(ctx, request, nil)
}

func (router *clockRecordingTurnRouter) PlanObserved(
	_ context.Context,
	request agentcontract.AgentRequest,
	_ *agentcontract.TurnRouterCallLedger,
) (agentcontract.TurnDecision, error) {
	router.routed = request
	return agentcontract.TurnDecision{Route: agentcontract.TurnRouteStartTask}, nil
}

func TestTheRouterThatDecidesTheTurnIsToldWhatDayItIs(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskLauncher := NewTaskLauncher(harnesstest.New(taskRunService), taskRunService, NewToolCatalogBuilder())
	router := &clockRecordingTurnRouter{}
	taskLauncher.UseTurnRouter(router)
	taskLauncher.UseCompanyProvider(func() agentcontract.CompanyContext {
		return agentcontract.CompanyContext{Name: "여명거리", TimeZone: "Asia/Seoul"}
	})

	if _, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "금요일에 휴가 쓸게",
		ResponseLanguage:  "ko",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	}); errorValue != nil {
		t.Fatal(errorValue)
	}

	if router.routed.EnvironmentNow.IsZero() {
		t.Fatal("a router that is not told the date resolves 금요일 against nothing")
	}
	if router.routed.Company.TimeZone != "Asia/Seoul" {
		t.Fatalf("the router reads the clock in the company's zone, got %q", router.routed.Company.TimeZone)
	}
	if !router.routed.EnvironmentNow.Equal(router.routed.TurnStartedAt) {
		t.Fatalf(
			"the turn and the environment disagree about now: %s and %s",
			router.routed.TurnStartedAt,
			router.routed.EnvironmentNow,
		)
	}
}
