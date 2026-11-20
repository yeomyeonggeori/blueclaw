package agentruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
)

func TestARunReportedWithoutRunningIsWordedInTheCompanysLanguage(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskLauncher := NewTaskLauncher(harnesstest.New(taskRunService), taskRunService, NewToolCatalogBuilder())
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, nil))
	taskLauncher.UseCompanyLocaleProvider(func() string { return "ko" })

	launchResult := taskLauncher.FailWithoutRunning(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceScheduled,
		RequesterPersonID: "person-1",
		ConversationID:    "schedule-1",
		Prompt:            "Write the weekly work report",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	}, "scheduled_start_window", errors.New("the run scheduled for noon was not done in time"))

	if language := launchResult.TurnResult.FailureNotice.Language; language != "ko" {
		t.Fatalf("a scheduled run that never started is reported in the company's language, got %q", language)
	}
}
