package tui

import "testing"

func TestFilterWaitingApprovalKeepsOnlyWaitingApprovalStatus(testInstance *testing.T) {
	taskRuns := []TaskRun{
		{TaskRunID: "task-1", Status: TaskStatusRunning},
		{TaskRunID: "task-2", Status: TaskStatusWaitingApproval},
		{TaskRunID: "task-3", Status: TaskStatusCompleted},
		{TaskRunID: "task-4", Status: TaskStatusWaitingApproval},
	}

	waitingTaskRuns := FilterWaitingApproval(taskRuns)

	if len(waitingTaskRuns) != 2 || waitingTaskRuns[0].TaskRunID != "task-2" || waitingTaskRuns[1].TaskRunID != "task-4" {
		testInstance.Fatalf("unexpected filtered task runs: %+v", waitingTaskRuns)
	}
}

func TestFilterWaitingApprovalReturnsEmptySliceNotNil(testInstance *testing.T) {
	waitingTaskRuns := FilterWaitingApproval([]TaskRun{{TaskRunID: "task-1", Status: TaskStatusRunning}})
	if waitingTaskRuns == nil {
		testInstance.Fatal("expected a non-nil empty slice")
	}
	if len(waitingTaskRuns) != 0 {
		testInstance.Fatalf("expected no task runs, got %+v", waitingTaskRuns)
	}
}

func TestApprovalDecisionForKeyMapsShortcuts(testInstance *testing.T) {
	testCases := map[string]string{
		"y": ApprovalDecisionApprove,
		"n": ApprovalDecisionReject,
	}
	for key, expectedDecision := range testCases {
		decision, isRecognized := ApprovalDecisionForKey(key)
		if !isRecognized || decision != expectedDecision {
			testInstance.Fatalf("key %q: expected %q, got %q (recognized=%v)", key, expectedDecision, decision, isRecognized)
		}
	}
}

func TestApprovalDecisionForKeyRejectsUnknownKeys(testInstance *testing.T) {
	if _, isRecognized := ApprovalDecisionForKey("x"); isRecognized {
		testInstance.Fatal("expected unknown key to be rejected")
	}
}
