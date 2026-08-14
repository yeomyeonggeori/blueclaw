package connectors

import (
	"context"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"log/slog"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

func TestCompletedTaskReplyCarriesModelWordingAndNativeAttachments(t *testing.T) {
	identityService := identity.NewIdentityService(policy.PolicyProjection{})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	connectorRuntimeHarness := harnesstest.New(taskRunService)
	connectorRuntime := NewConnectorRuntime(identityService, connectorRuntimeHarness, taskRunService, taskEventService, slog.Default())
	connectorRuntime.UseIntakeClassifier(connectorRuntimeHarness)
	connectorRuntime.UseReplyGenerator(connectorRuntimeHarness)

	var sentReply OutboundReply
	sendReply := func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
		sentReply = reply
		return "dispatch-1", nil
	}

	turnResult := agentcontract.AgentTurnResult{
		TaskRun:       task.TaskRun{TaskRunID: "task-1", Status: task.TaskStatusCompleted},
		FinishMessage: "Done: sandbox:/mnt/data/deck.pptx",
		Attachments:   []toolcontract.FileAttachment{{Filename: "deck.pptx", DevicePath: "/workspace/private/people/p1/tmp/deck.pptx"}},
	}

	_, errorValue := connectorRuntime.dispatchTaskReply(context.Background(), "mattermost", &testAdapter{}, PlatformInboundEvent{SenderID: "sender-1"}, ReplyTarget{}, turnResult, "", sendReply)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(sentReply.Attachments) != 1 {
		t.Fatalf("expected the deliverable attachment to be carried, got %d", len(sentReply.Attachments))
	}
	if sentReply.Message != turnResult.FinishMessage {
		t.Fatalf("expected connector to preserve model wording, got %q", sentReply.Message)
	}
}

func TestFailedTaskReplyPreservesModelWording(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	connectorRuntime := NewConnectorRuntime(
		identity.NewIdentityService(policy.PolicyProjection{}),
		harnesstest.New(taskRunService),
		taskRunService,
		taskEventService,
		slog.Default(),
	)
	message := "The failure details were read from file:///tmp/report.txt."
	var sentReply OutboundReply
	sendReply := func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
		sentReply = reply
		return "dispatch-1", nil
	}
	turnResult := agentcontract.AgentTurnResult{
		TaskRun:    task.TaskRun{TaskRunID: "task-1", Status: task.TaskStatusFailed},
		UserNotice: message,
	}

	_, errorValue := connectorRuntime.dispatchTaskReply(context.Background(), "mattermost", &testAdapter{}, PlatformInboundEvent{SenderID: "sender-1"}, ReplyTarget{}, turnResult, "", sendReply)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.HasPrefix(sentReply.Message, message) {
		t.Fatalf("expected connector to preserve failure wording, got %q", sentReply.Message)
	}
}

func TestSuppressedReplyStillDeliversApprovalQuestion(t *testing.T) {
	waitingApproval := agentcontract.AgentTurnResult{
		TaskRun:                task.TaskRun{TaskRunID: "task-approval", Status: task.TaskStatusWaitingApproval},
		ReplySuppressionReason: "ambient_duty_no_reply",
	}
	if decision := decideTaskReply(waitingApproval, false, false); decision.Kind != taskReplyDecisionSendUserNotice {
		t.Fatalf("expected a waiting-approval task to deliver its question despite suppression, got %+v", decision)
	}
	waitingInput := agentcontract.AgentTurnResult{
		TaskRun:                task.TaskRun{TaskRunID: "task-input", Status: task.TaskStatusWaitingUserInput},
		ReplySuppressionReason: "ambient_duty_no_reply",
	}
	if decision := decideTaskReply(waitingInput, false, false); decision.Kind != taskReplyDecisionSendUserNotice {
		t.Fatalf("expected a waiting-user-input task to deliver its question despite suppression, got %+v", decision)
	}
	completed := agentcontract.AgentTurnResult{
		TaskRun:                task.TaskRun{TaskRunID: "task-done", Status: task.TaskStatusCompleted},
		ReplySuppressionReason: "ambient_duty_no_reply",
	}
	if decision := decideTaskReply(completed, false, false); decision.Kind != taskReplyDecisionSuppressRequested {
		t.Fatalf("expected a completed ambient task to stay suppressed, got %+v", decision)
	}
}
