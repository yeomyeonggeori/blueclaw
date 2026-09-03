package connectors

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
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
	}
	decision := interruptedTaskTurnDecision(taskEvents, "ko")
	if decision.TaskLevel != agentcontract.TaskLevelMedium {
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

// The incident this guards: a task paused on "delete the duplicate post"
// absorbed "edit the post and add the image instead" as a steer, kept its
// precomputed continue_task routing with the old delete contract, and executed
// a delete approved for the old objective. A steer that carries the person's
// words must go back through intake with nothing precomputed and no approval
// carried over.
func TestASteerWithNewWordsLaunchesThroughIntakeAgain(t *testing.T) {
	launchRequest := agentruntime.TaskLaunchRequest{
		Prompt:                  "두 번째로 중복 올린 글 삭제해줘.",
		IsApprovalContinuation:  true,
		IsRuntimeRestartResume:  true,
		PrecomputedTurnDecision: &agentcontract.TurnDecision{Route: agentcontract.TurnRouteContinueTask},
	}
	event := PlatformInboundEvent{Prompt: "삭제가 아니라 글을 수정해서 원본 이미지도 넣어줘"}

	steered := steeredTaskLaunchRequest(launchRequest, event, "게시글 수정 및 원본 이미지 추가")

	if steered.Prompt != "삭제가 아니라 글을 수정해서 원본 이미지도 넣어줘" {
		t.Fatalf("the person's own words must drive the re-intake, got %q", steered.Prompt)
	}
	if steered.PrecomputedTurnDecision != nil {
		t.Fatal("a steered launch must not carry a precomputed route; intake decides refine versus revise")
	}
	if steered.IsApprovalContinuation {
		t.Fatal("a call approved for the old objective must not be carried out under the new one")
	}
	if steered.IsRuntimeRestartResume {
		t.Fatal("a steered launch is a new ask, not a restart resume")
	}
}

func TestASteerWithNothingNewKeepsTheResumeShape(t *testing.T) {
	launchRequest := agentruntime.TaskLaunchRequest{
		Prompt:                  "해줘",
		IsApprovalContinuation:  true,
		IsRuntimeRestartResume:  true,
		PrecomputedTurnDecision: &agentcontract.TurnDecision{Route: agentcontract.TurnRouteContinueTask},
	}

	steered := steeredTaskLaunchRequest(launchRequest, PlatformInboundEvent{}, "")

	if steered.PrecomputedTurnDecision == nil || !steered.IsApprovalContinuation || !steered.IsRuntimeRestartResume {
		t.Fatal("a steer that says nothing new resumes the task as it was")
	}
}

// The normal launch imports the triggering message's attachments only after
// busy routing declines; a steer resume launches from inside that routing, so
// it must import them itself. Older attachments stay lazy: they are read on
// demand by the url standing in their message.
func TestResumePausedTaskForSteerImportsVisibleAttachments(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.Reply = "이어서 진행하겠습니다."
	pausedTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "글 수정해줘")
	connectorRuntime.taskRunService.AppendTaskEvent(pausedTaskRun.TaskRunID, taskstate.TaskEventAgentTaskLaunched,
		`{"sourceReference":"test:thread:abc","platform":"test","conversationID":"direct-1","replyTargetID":"reply-target-1","requesterPersonID":"person-1"}`)
	event := testInboundEvent("message-steer-attachments")
	event.Context.InputAttachments = []InputAttachment{{
		Platform:    "test",
		URL:         "https://relay.test/media/abc.png",
		MessageID:   "message-steer-attachments",
		Filename:    "image",
		ContentType: "image/png",
	}}
	sendReply := func(context.Context, ReplyTarget, OutboundReply) (string, error) { return "dispatch-1", nil }

	_, errorValue := connectorRuntime.resumePausedTaskForSteer(context.Background(), "test", event, ReplyTarget{}, pausedTaskRun, "원본 이미지를 넣어서 수정해줘", agentcontract.TurnDecision{}, sendReply)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(adapter.inputAttachmentImportRequests) == 0 {
		t.Fatal("a steered launch must import the conversation's visible attachments before launching")
	}
	imported := adapter.inputAttachmentImportRequests[0]
	if len(imported.InputAttachments) != 1 || imported.InputAttachments[0].URL != "https://relay.test/media/abc.png" {
		t.Fatalf("expected the message's attachment to be imported, got %+v", imported.InputAttachments)
	}
}
