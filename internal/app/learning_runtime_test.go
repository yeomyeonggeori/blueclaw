package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestLearningExperienceUsesCompletedTaskLedger(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "find the sample policy")
	taskRun.Status = agentcontract.TaskStatusCompleted
	taskRun.Result = "policy found"
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "tool.policy_search.result", "policy found")
	request := agentruntime.TaskLaunchRequest{RequesterPersonID: "person-1", Prompt: taskRun.Prompt}
	result := agentruntime.TaskLaunchResult{TurnResult: agentcontract.AgentTurnResult{TaskRun: taskRun, FinishMessage: "done"}, ToolNames: []string{"policy_search"}}

	experience, isComplete := learningExperienceFromTask(request, result, taskRunService, nil)
	if !isComplete || experience.Audience != "person:person-1" {
		t.Fatalf("expected completed person experience, got %+v", experience)
	}
	var outcome learningTaskOutcome
	if errorValue := json.Unmarshal(experience.Outcome, &outcome); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(outcome.Events) != 2 || outcome.Events[1].Name != "tool.policy_search.result" {
		t.Fatalf("expected task event ledger in outcome, got %+v", outcome.Events)
	}
}

func TestLearningExperienceMarksOversizedEvidenceWithoutClippingIt(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "large result")
	taskRun.Status = agentcontract.TaskStatusCompleted
	originalBody := strings.Repeat("x", maximumLearningBody+1)
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "tool.result", originalBody)
	result := agentruntime.TaskLaunchResult{TurnResult: agentcontract.AgentTurnResult{TaskRun: taskRun}}
	var outcome learningTaskOutcome
	if errorValue := json.Unmarshal(learningExperienceOutcome(taskRunService, result, nil), &outcome); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !outcome.Incomplete || outcome.OmittedEvents != 1 || len(outcome.Events) != 2 {
		t.Fatalf("expected incomplete evidence metadata, got %+v", outcome)
	}
	if !outcome.Events[1].BodyOmitted || outcome.Events[1].ByteLength != len(originalBody) || outcome.Events[1].SHA256 == "" || outcome.Events[1].Body != "" {
		t.Fatalf("expected immutable omission metadata, got %+v", outcome.Events[1])
	}
}

func TestLearningExperienceIgnoresOpenTask(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "still working")
	experience, isComplete := learningExperienceFromTask(
		agentruntime.TaskLaunchRequest{RequesterPersonID: "person-1", Prompt: taskRun.Prompt},
		agentruntime.TaskLaunchResult{TurnResult: agentcontract.AgentTurnResult{TaskRun: taskRun}},
		taskRunService,
		nil,
	)
	if isComplete || experience.TaskID != "" {
		t.Fatalf("expected open task to be ignored, got %+v", experience)
	}
}
