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
	gate.UseQuestionPoster(silentPoster{})

	if _, errorValue := gate.AwaitApproval(context.Background(), mcpserver.ApprovalRequest{
		RequesterPersonID: "person-1",
		TaskRunID:         taskRun.TaskRunID,
		ToolName:          "event_delete",
		ToolInput:         json.RawMessage(`{"eventID":"event-1"}`),
		SideEffectClass:   "external_send",
		ResponseLanguage:  "ko",
	}); errorValue != nil {
		t.Fatalf("expected the gate to hold the call: %v", errorValue)
	}
	return taskRunService.ListTaskEvent(taskRun.TaskRunID)
}

func TestACallHeldByTheHostGateKeepsTheLanguageTheRequesterIsAskedIn(t *testing.T) {
	taskEvents := heldCallTaskEvents(t)

	if responseLanguage := latestApprovalResponseLanguage(taskEvents); responseLanguage == "" {
		t.Fatal("the language the requester is asked in was lost between the gate and the connector")
	}
}

type silentPoster struct{}

func (silentPoster) PostQuestion(context.Context, mcpserver.ApprovalRequest, approvalgate.ParkedQuestion) error {
	return nil
}
