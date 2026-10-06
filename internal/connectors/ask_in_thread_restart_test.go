package connectors

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestABlockedGoalTheLaunchFailureRecordedIsStillTheLatestActiveGoal(t *testing.T) {
	taskEvents := []task.TaskEvent{{Name: task.TaskEventLaunchGoalBlocked, Body: `{"goalID":"run-1","taskRunID":"run-1","originalInstruction":"정리해줘","status":"blocked"}`}}

	activeGoal := latestActiveGoal(taskEvents)

	if activeGoal.OriginalInstruction != "정리해줘" {
		t.Fatalf("a run blocked at intake lost its goal across the rename: %+v", activeGoal)
	}
}
