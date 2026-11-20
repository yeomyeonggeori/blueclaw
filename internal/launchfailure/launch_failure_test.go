package launchfailure

import (
	"context"
	"errors"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func noticeLanguageFor(t *testing.T, requestLanguage string) string {
	t.Helper()
	completer := NewCompleter(task.NewTaskRunService(task.NewTaskEventService()), nil)
	completer.UseCompanyLocaleProvider(func() string { return "ko" })
	turnResult := completer.CompleteLaunchFailure(context.Background(), agentcontract.AgentTurnRequest{
		RequesterIdentity: agentcontract.RequesterIdentity{RequesterPersonID: "person-1"},
		Prompt:            "주간 보고 써줘",
		ResponseLanguage:  requestLanguage,
	}, "launch", "task_retry", errors.New("the agent could not be reached"))
	return turnResult.FailureNotice.Language
}

func TestAFailureNoticeWithNoLanguageOfItsOwnIsWrittenInTheCompanysLanguage(t *testing.T) {
	if language := noticeLanguageFor(t, ""); language != "ko" {
		t.Fatalf("expected the company's language, got %q", language)
	}
}

func TestAFailureNoticeKeepsTheLanguageItWasAskedIn(t *testing.T) {
	if language := noticeLanguageFor(t, "en"); language != "en" {
		t.Fatalf("expected the request's own language, got %q", language)
	}
}
