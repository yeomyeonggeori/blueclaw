package connectors

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestUserSteerTaskProfileSourceReferenceResolvesRealPlatform(t *testing.T) {
	profile := userSteerTaskProfile("mattermost", "task-1", "")
	if platformFromSourceReference(profile.sourceReference) != "mattermost" {
		t.Fatalf("steer source reference must resolve the real platform, got %q", profile.sourceReference)
	}
}

func TestPlatformFromSourceReferenceRejectsResumePrefixes(t *testing.T) {
	for _, sourceReference := range []string{"auto_resume:task-1", "user_steer:task-1", "steer:task-1"} {
		if platform := platformFromSourceReference(sourceReference); platform != "" {
			t.Fatalf("non-adapter resume prefix must not resolve as a platform, %q gave %q", sourceReference, platform)
		}
	}
	if platformFromSourceReference("mattermost:thread:abc") != "mattermost" {
		t.Fatal("a real platform prefix must still resolve")
	}
}

func TestResumePausedTaskForSteerWithoutLaunchContextSendsNoticeWithoutOrphan(t *testing.T) {
	connectorRuntime, _, harness := newStubbedTestConnectorRuntime(t)
	harness.Reply = "저장된 컨텍스트에서 이 작업을 재개할 수 없습니다."
	pausedTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "사이트 만들어")
	event := testInboundEvent("message-steer-resume")
	sendReply := func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		return "dispatch-1", nil
	}

	result, errorValue := connectorRuntime.resumePausedTaskForSteer(context.Background(), "test", event, ReplyTarget{}, pausedTaskRun, "이어서 해", agentcontract.TurnDecision{}, sendReply)

	if errorValue != nil {
		t.Fatalf("missing launch context must be handled with a notice, got error %v", errorValue)
	}
	if !result.isHandled {
		t.Fatal("missing launch context must be handled, not silently dropped")
	}
	if connectorTaskEventsContain(connectorRuntime, pausedTaskRun.TaskRunID, "task.steer.requested", "") {
		t.Fatal("must not write an orphan task.steer.requested when resume is unavailable")
	}
	if !connectorTaskEventsContain(connectorRuntime, pausedTaskRun.TaskRunID, "task.steer.resume_unavailable", "") {
		t.Fatal("expected a task.steer.resume_unavailable diagnostic event")
	}
}

func TestInterruptedTaskTurnDecisionInheritsHighestRecordedEffort(t *testing.T) {
	taskEvents := []task.TaskEvent{
		{Name: "agent.intake", Body: `{"effortLevel":"deep","taskComplexity":"complex"}`},
		{Name: "agent.intake", Body: `{"effortLevel":"standard","taskComplexity":"normal","reason":"runtime_restart_auto_resume"}`},
		{Name: "agent.budget_escalated", Body: `{"newEffortLevel":"extended"}`},
	}
	decision := interruptedTaskTurnDecision(taskEvents, "ko")
	if decision.TaskLevel != agentcontract.TaskLevelHigh {
		t.Fatalf("resumed task must inherit the highest recorded task level, got %q", decision.TaskLevel)
	}
}

func TestInterruptedTaskTurnDecisionDefaultsToStandardEffort(t *testing.T) {
	decision := interruptedTaskTurnDecision([]task.TaskEvent{{Name: "agent.intake", Body: "not-json"}}, "ko")
	if decision.TaskLevel != agentcontract.TaskLevelLow {
		t.Fatalf("resumed task without recorded task level must default to low, got %q", decision.TaskLevel)
	}
}

func TestAnAnswerReplacesTheObjectiveTheTaskStoppedOn(t *testing.T) {
	stoppedToAsk := []task.TaskEvent{{Name: "agent.goal.waiting_user_input", Body: `{"goalID":"task-1","currentObjective":"the request lacks detail, so it has to be confirmed","status":"waiting_user_input"}`}}
	answered := "register the 18 August Shanghai edatec meeting as a completed task"

	activeGoal := interruptedTaskActiveGoalWithInstruction(task.TaskRun{TaskRunID: "task-1", Prompt: "해줘"}, stoppedToAsk, "note", answered)

	if activeGoal.CurrentObjective != answered {
		t.Fatalf("a task that stopped to ask keeps asking until the answer becomes its objective, got %q", activeGoal.CurrentObjective)
	}
}

func TestARestartCarriesNoAnswerAndKeepsTheObjective(t *testing.T) {
	inProgress := []task.TaskEvent{{Name: "agent.goal.waiting_user_input", Body: `{"goalID":"task-1","currentObjective":"draft the quarterly report","status":"active"}`}}

	activeGoal := interruptedTaskActiveGoalWithInstruction(task.TaskRun{TaskRunID: "task-1", Prompt: "해줘"}, inProgress, "note", "")

	if activeGoal.CurrentObjective != "draft the quarterly report" {
		t.Fatalf("a restart said nothing new, so the work in flight stands, got %q", activeGoal.CurrentObjective)
	}
}
