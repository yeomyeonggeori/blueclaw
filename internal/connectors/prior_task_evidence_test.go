package connectors

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestPriorTaskCarriesRecordedCallsInsteadOfInventedAttempts(t *testing.T) {
	context := priorTaskContextForTaskRun(task.TaskRun{
		TaskRunID: "previous", Prompt: "retry", Status: task.TaskStatusFailed,
		FailureReason: "Both records failed to register.",
	}, []task.TaskEvent{
		{Name: "tool.record_create.requested", Body: `{"toolName":"record_create","input":{"title":"first"}}`},
		{Name: "tool.record_create.result", Body: `{"observationID":"obs-001","tool":"record_create","toolInput":{"title":"first"},"failure":{"kind":"external_service","code":"operation_failed","stage":"result_validation","userSafeSummary":"response contract failed"},"effects":[{"objectType":"task","effect":"created","id":"task-1"}]}`},
		{Name: "agent.failure_report", Body: `{"attempts":[{"tool":"record_create","title":"second"}]}`},
	})
	if len(context.RecordedAttempts) != 1 {
		t.Fatalf("expected exactly the executed call, got %+v", context.RecordedAttempts)
	}
	attempt := context.RecordedAttempts[0]
	if string(attempt.ToolInput) != `{"title":"first"}` || attempt.Failure.Stage != "result_validation" || len(attempt.Effects) != 1 {
		t.Fatalf("lost the evidence that lets recovery question the old report: %+v", attempt)
	}
}

func TestPriorTaskEvidenceIsBoundedAndSaysWhatWasOmitted(t *testing.T) {
	events := []task.TaskEvent{}
	for index := 0; index < priorTaskAttemptLimit+3; index++ {
		events = append(events, task.TaskEvent{Name: "tool.shell.result", Body: fmt.Sprintf(`{"observationID":"obs-%d","toolInput":{"command":"%s"}}`, index, strings.Repeat("x", priorTaskInputByteLimit))})
	}
	attempts, omittedCount := priorTaskRecordedAttempts(events)
	if len(attempts) != priorTaskAttemptLimit || omittedCount != 3 || attempts[0].ObservationID != "obs-3" {
		t.Fatalf("evidence window is not bounded: attempts=%d omitted=%d", len(attempts), omittedCount)
	}
	for _, attempt := range attempts {
		if len(attempt.ToolInput) != 0 || !attempt.ToolInputOmitted {
			t.Fatalf("large input was not explicitly omitted: %+v", attempt)
		}
	}
}
