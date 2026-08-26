package connectors

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func heldCallTaskEvents(t *testing.T) []task.TaskEvent {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "channel-1", "내일 회의 지워줘")
	gate := approvalgate.New(taskRunService)

	if _, errorValue := gate.AwaitApproval(context.Background(), mcpserver.ApprovalRequest{
		RequesterPersonID: "person-1",
		TaskRunID:         taskRun.TaskRunID,
		ToolName:          "event_delete",
		ToolInput:         json.RawMessage(`{"eventID":"event-1"}`),
		ApprovalScope:     "calendar",
		SideEffectClass:   "external_send",
		ResponseLanguage:  "ko",
	}); errorValue != nil {
		t.Fatalf("expected the gate to hold the call: %v", errorValue)
	}
	return taskRunService.ListTaskEvent(taskRun.TaskRunID)
}

func TestACallHeldByTheHostGateReachesTheRequesterAsAQuestion(t *testing.T) {
	taskEvents := heldCallTaskEvents(t)

	if question := latestApprovalQuestion(taskEvents); question == "" {
		t.Fatalf("a held call the requester is never told about waits forever, got %+v", taskEvents)
	}
	if responseLanguage := latestApprovalResponseLanguage(taskEvents); responseLanguage == "" {
		t.Fatal("the language the requester is asked in was lost between the gate and the connector")
	}
}

func TestACallHeldByTheHostGateCanBeApprovedForTheWholeTask(t *testing.T) {
	if approvalScope := pendingApprovalScope(heldCallTaskEvents(t)); approvalScope != "calendar" {
		t.Fatalf("confirm_task grants the scope this reader finds, so an empty one makes it a no-op, got %q", approvalScope)
	}
}
