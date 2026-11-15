package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

func TestShouldNotifyUserOnlyWhenNothingWasDelivered(t *testing.T) {
	sweeper := StaleTaskSweeper{Notifier: stubStaleTaskNotifier{}}
	cases := []struct {
		name         string
		taskRun      task.TaskRun
		shouldNotify bool
	}{
		{"blocked with delivered result", task.TaskRun{Status: task.TaskStatusBlocked, Result: "already told the user"}, false},
		{"blocked with no delivery", task.TaskRun{Status: task.TaskStatusBlocked}, true},
		{"waiting approval", task.TaskRun{Status: task.TaskStatusWaitingApproval, Result: "question was asked"}, true},
		{"waiting user input", task.TaskRun{Status: task.TaskStatusWaitingUserInput}, true},
	}
	for _, testCase := range cases {
		if sweeper.shouldNotifyUser(testCase.taskRun) != testCase.shouldNotify {
			t.Fatalf("%s: shouldNotifyUser = %v, want %v", testCase.name, !testCase.shouldNotify, testCase.shouldNotify)
		}
	}
}

func TestShouldNotifyUserRequiresNotifier(t *testing.T) {
	sweeper := StaleTaskSweeper{}
	if sweeper.shouldNotifyUser(task.TaskRun{Status: task.TaskStatusWaitingApproval}) {
		t.Fatal("a sweeper without a notifier must expire quietly")
	}
}

func TestStaleTaskUserFacingReasonNamesTheWait(t *testing.T) {
	if !strings.Contains(staleTaskUserFacingReason("waiting_expired"), "closed without one") {
		t.Fatal("waiting expiry must explain the unanswered wait")
	}
	if !strings.Contains(staleTaskUserFacingReason("blocked_expired"), "closed") {
		t.Fatal("blocked expiry must state the task was closed")
	}
}

type countingStaleTaskNotifier struct {
	taskRunService *task.TaskRunService
	notices        []string
}

func (notifier *countingStaleTaskNotifier) FailUnresumedInterruptedTaskRun(_ context.Context, taskRun task.TaskRun, reason string) bool {
	notifier.notices = append(notifier.notices, reason)
	_, errorValue := notifier.taskRunService.FailTaskRun(taskRun.TaskRunID, reason)
	return errorValue == nil
}

func parkedRunWithAPendingHold(t *testing.T, taskRunService *task.TaskRunService, status task.TaskStatus) task.TaskRun {
	t.Helper()
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "일정 삭제")
	holdrecord.Open(taskRunService, taskRun.TaskRunID, agentcontract.HeldCall{ToolName: "event_delete", Confirmation: "삭제할까요?"}, nil)
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, status, "삭제할까요?"); errorValue != nil {
		t.Fatal(errorValue)
	}
	return taskRun
}

func TestEveryParkedQuestionExpiresAfterOneDayWithItsHoldClosedAndOneNotice(t *testing.T) {
	for _, status := range []task.TaskStatus{task.TaskStatusWaitingApproval, task.TaskStatusWaitingUserInput} {
		taskRunService := task.NewTaskRunService(task.NewTaskEventService())
		notifier := &countingStaleTaskNotifier{taskRunService: taskRunService}
		sweeper := StaleTaskSweeper{TaskRunService: taskRunService, Notifier: notifier}
		taskRun := parkedRunWithAPendingHold(t, taskRunService, status)

		if swept := sweeper.SweepOnce(context.Background(), time.Now().Add(23*time.Hour)); swept != 0 {
			t.Fatalf("%s: a question asked less than a day ago was expired", status)
		}
		sweeper.SweepOnce(context.Background(), time.Now().Add(25*time.Hour))
		sweeper.SweepOnce(context.Background(), time.Now().Add(26*time.Hour))

		holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID))
		if len(holds) != 1 || holds[0].State != holdrecord.StateRejected {
			t.Fatalf("%s: the expired question's hold still waits: %+v", status, holds)
		}
		if len(notifier.notices) != 1 {
			t.Fatalf("%s: the requester was told %d times, expected once", status, len(notifier.notices))
		}
		if expired, _ := taskRunService.FindTaskRun(taskRun.TaskRunID); expired.Status != task.TaskStatusFailed {
			t.Fatalf("%s: the run is still %s a day after its question", status, expired.Status)
		}
	}
}

type stubStaleTaskNotifier struct{}

func (stubStaleTaskNotifier) FailUnresumedInterruptedTaskRun(_ context.Context, _ task.TaskRun, _ string) bool {
	return true
}
