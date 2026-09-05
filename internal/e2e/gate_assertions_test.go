package e2e

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func gateNamedEvent(name string) task.TaskEvent {
	return task.TaskEvent{Name: name}
}

func gateCheckpointEvent(message string) task.TaskEvent {
	return task.TaskEvent{Name: "agent.checkpoint.sent", Body: fmt.Sprintf(`{"toolName":"alpha","message":%q}`, message)}
}

func TestAssertEventSubsequence(t *testing.T) {
	events := []task.TaskEvent{gateNamedEvent("a"), gateNamedEvent("b"), gateNamedEvent("c")}
	if errorValue := assertEventSubsequence(events, []string{"a", "c"}); errorValue != nil {
		t.Fatalf("expected subsequence a,c to match: %v", errorValue)
	}
	if assertEventSubsequence(events, []string{"c", "a"}) == nil {
		t.Fatal("expected order violation c,a to fail")
	}
	if assertEventSubsequence(events, []string{"a", "z"}) == nil {
		t.Fatal("expected missing event z to fail")
	}
	if errorValue := assertEventSubsequence(events, nil); errorValue != nil {
		t.Fatalf("expected empty expectation to pass: %v", errorValue)
	}
}

func TestAssertTurnResultGateFields(t *testing.T) {
	passingResult := VirtualTurnResult{
		TaskStatus:    task.TaskStatusCompleted,
		FinishMessage: "final reply",
		Events: []task.TaskEvent{
			gateNamedEvent("tool.alpha.requested"),
			gateCheckpointEvent("The task is in progress"),
			gateNamedEvent("tool.alpha.result"),
		},
	}
	passingTurn := VirtualTurn{
		ExpectedSequence:          []string{"tool.alpha.requested", "tool.alpha.result"},
		ExpectedCheckpointReplies: []string{"in progress"},
		ForbiddenEvents:           []string{"agent.no_progress_loop_stopped"},
		ExpectedTaskStatus:        task.TaskStatusCompleted,
	}
	if errorValue := assertTurnResult("", passingTurn, passingResult); errorValue != nil {
		t.Fatalf("expected gate to pass: %v", errorValue)
	}

	failureCases := map[string]VirtualTurn{
		"checkpoint fragment missing": {ExpectedCheckpointReplies: []string{"a phrase that does not exist"}},
		"sequence order violated":     {ExpectedSequence: []string{"tool.alpha.result", "tool.alpha.requested"}},
		"task status mismatch":        {ExpectedTaskStatus: task.TaskStatusBlocked},
	}
	for name, failingTurn := range failureCases {
		if assertTurnResult("", failingTurn, passingResult) == nil {
			t.Fatalf("expected gate to fail for %q", name)
		}
	}

	stalledResult := passingResult
	stalledResult.Events = append([]task.TaskEvent{gateNamedEvent("agent.no_progress_loop_stopped")}, passingResult.Events...)
	if assertTurnResult("", VirtualTurn{ForbiddenEvents: []string{"agent.no_progress_loop_stopped"}}, stalledResult) == nil {
		t.Fatal("expected forbidden no-progress event to fail the gate")
	}
}

func TestStructuralGateAllowsFormerPresentationWordingFragments(t *testing.T) {
	turn := VirtualTurn{
		ExpectedResponse:   VirtualResponseReply,
		ExpectedToolCalls:  []string{"shell"},
		ExpectedTaskStatus: task.TaskStatusCompleted,
	}
	completedResult := VirtualTurnResult{
		TaskStatus:    task.TaskStatusCompleted,
		DidReply:      true,
		FinishMessage: "PPT 파일을 직접 생성할 수 없다는 안내와 credentials 설명을 포함한 답변",
		Events:        []task.TaskEvent{gateNamedEvent("tool.shell.result")},
	}
	if errorValue := assertTurnResult("", turn, completedResult); errorValue != nil {
		t.Fatalf("expected structural gate to ignore reply wording, got %v", errorValue)
	}

	completedResult.Events = nil
	if assertTurnResult("", turn, completedResult) == nil {
		t.Fatal("expected missing required shell result to fail the structural gate")
	}
}

func TestStreamProgressObserverFormatsReplyAndTool(t *testing.T) {
	buffer := &bytes.Buffer{}
	observe := streamProgressObserver(buffer)
	observe(task.RawTurnEvent{Name: "agent.checkpoint.sent", Body: `{"toolName":"alpha","message":"in progress"}`})
	observe(task.RawTurnEvent{Name: "tool.web_search.requested", Body: "{}"})
	observe(task.RawTurnEvent{Name: "tool.web_search.result", Body: "{}"})
	output := buffer.String()
	if !strings.Contains(output, "reply: in progress") {
		t.Fatalf("expected reply line, got %q", output)
	}
	if !strings.Contains(output, "tool: web_search") {
		t.Fatalf("expected tool line, got %q", output)
	}
	if strings.Count(output, "\n") != 2 {
		t.Fatalf("expected 2 progress lines (tool result ignored), got %q", output)
	}
}

func TestCheckpointReplyMalformedBodyDoesNotPanic(t *testing.T) {
	events := []task.TaskEvent{{Name: "agent.checkpoint.sent", Body: "{not json"}}
	if checkpointRepliesContain(events, "anything") {
		t.Fatal("expected malformed checkpoint body to yield no replies")
	}
}

func TestLooseModeStillEnforcesStructuralFields(t *testing.T) {
	turnResult := VirtualTurnResult{
		TaskStatus:    task.TaskStatusCompleted,
		FinishMessage: "final reply",
		Events:        []task.TaskEvent{gateNamedEvent("agent.no_progress_loop_stopped")},
	}
	if errorValue := assertLooseTurnResult(VirtualTurn{}, turnResult); errorValue != nil {
		t.Fatalf("expected loose mode without structural expectations to pass: %v", errorValue)
	}
	if assertLooseTurnResult(VirtualTurn{ForbiddenEvents: []string{"agent.no_progress_loop_stopped"}}, turnResult) == nil {
		t.Fatal("expected loose mode to still enforce forbidden events")
	}
	if assertLooseTurnResult(VirtualTurn{ExpectedSequence: []string{"missing.event"}}, turnResult) == nil {
		t.Fatal("expected loose mode to still enforce expected sequence")
	}
}
