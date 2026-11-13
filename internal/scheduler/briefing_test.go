package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/persona"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestNewMorningBriefingScheduleUsesConfiguredCompanyTime(t *testing.T) {
	referenceTime := time.Date(2026, time.March, 8, 1, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name     string
		timeZone string
		timeText string
		cron     string
	}{
		{name: "seoul", timeZone: "Asia/Seoul", timeText: "08:00", cron: "0 8 * * *"},
		{name: "new york", timeZone: "America/New_York", timeText: "08:30", cron: "30 8 * * *"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			settings, errorValue := morningBriefingSettings(persona.User{MorningBriefing: &persona.MorningBriefing{Enabled: true, Time: testCase.timeText}})
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			schedule, errorValue := newBriefingSchedule(task.BriefingKindMorning, "person-1", settings, testCase.timeZone, referenceTime)
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if schedule.CronExpression != testCase.cron || schedule.TimeZone != testCase.timeZone {
				t.Fatalf("unexpected schedule: %+v", schedule)
			}
			if schedule.NextRunAt == nil {
				t.Fatal("expected next run")
			}
			if schedule.NextRunAt.Location().String() != "UTC" {
				t.Fatalf("expected stored instant in UTC, got %s", schedule.NextRunAt.Location())
			}
		})
	}
}

func TestNewMorningBriefingScheduleRejectsInvalidTimeZone(t *testing.T) {
	_, errorValue := newBriefingSchedule(task.BriefingKindMorning, "person-1", briefingSettings{isEnabled: true, cronExpression: "0 8 * * *"}, "Mars/Olympus", time.Now())
	if errorValue == nil {
		t.Fatal("expected invalid timezone error")
	}
}

func TestBriefingReconcileReadsUsersAndRemovesMissingRosterEntries(t *testing.T) {
	rootPath := t.TempDir()
	firstUser := persona.DefaultMorningBriefing()
	firstDocument, errorValue := persona.CanonicalUser(persona.User{MorningBriefing: &firstUser})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	writePersonaUser(t, rootPath, "person-1", firstDocument)
	writePersonaUser(t, rootPath, "person-2", firstDocument)
	repository := &recordingBriefingRepository{}
	briefing := &Briefings{
		Repository: repository,
		Accounts:   recordingBriefingAccounts{accounts: []identity.PlatformAccountIdentity{{Platform: "buzz", ExternalUserID: "user-1", PersonID: "person-1"}}},
		PolicyDocument: func() policy.PolicyDocument {
			return policy.PolicyDocument{Company: policy.CompanyPolicy{TimeZone: "Asia/Seoul"}, People: []policy.PersonPolicy{{PersonID: "person-1"}, {PersonID: "person-2"}}}
		},
		PersonAccessResolver: briefingPersonAccessResolver{},
		ActorFactory:         briefingActorFactory{rootPath: rootPath},
		WorkspaceRootPath:    rootPath,
		OpenDirectMessage: func(context.Context, string, string) (string, string, error) {
			return "conversation-1", "reply-1", nil
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if errorValue := briefing.Reconcile(context.Background(), time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(repository.schedulesByKind[task.BriefingKindMorning]) != 2 || repository.schedulesByKind[task.BriefingKindMorning][0].ScheduleID != task.BriefingScheduleID(task.BriefingKindMorning, "person-1") {
		t.Fatalf("expected one schedule per roster person, got %+v", repository.schedulesByKind)
	}
	if repository.schedulesByKind[task.BriefingKindMorning][0].ConversationID != "conversation-1" || repository.schedulesByKind[task.BriefingKindMorning][0].ReplyTargetID != "reply-1" {
		t.Fatalf("expected resolved direct message target, got %+v", repository.schedulesByKind[task.BriefingKindMorning][0])
	}
	if repository.schedulesByKind[task.BriefingKindMorning][1].NextRunAt != nil {
		t.Fatal("expected person without a messenger account to stay disabled")
	}
}

func TestMorningBriefingCanRunReflectsChangedSettings(t *testing.T) {
	rootPath := t.TempDir()
	conversationID := "conversation-1"
	replyTargetID := "reply-1"
	directMessageCalls := 0
	settings := persona.DefaultMorningBriefing()
	document, errorValue := persona.CanonicalUser(persona.User{MorningBriefing: &settings})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	writePersonaUser(t, rootPath, "person-1", document)
	briefing := &Briefings{
		Accounts: recordingBriefingAccounts{accounts: []identity.PlatformAccountIdentity{{Platform: "buzz", ExternalUserID: "user-1", PersonID: "person-1"}}},
		PolicyDocument: func() policy.PolicyDocument {
			return policy.PolicyDocument{Company: policy.CompanyPolicy{TimeZone: "Asia/Seoul"}, People: []policy.PersonPolicy{{PersonID: "person-1"}}}
		},
		PersonAccessResolver: briefingPersonAccessResolver{},
		ActorFactory:         briefingActorFactory{rootPath: rootPath},
		WorkspaceRootPath:    rootPath,
		OpenDirectMessage: func(context.Context, string, string) (string, string, error) {
			directMessageCalls++
			return conversationID, replyTargetID, nil
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	morningSettings, errorValue := morningBriefingSettings(persona.User{MorningBriefing: &settings})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	schedule, errorValue := newBriefingSchedule(task.BriefingKindMorning, "person-1", morningSettings, "Asia/Seoul", time.Now())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	schedule.Platform = "buzz"
	schedule.ConversationID = conversationID
	schedule.ReplyTargetID = replyTargetID
	canRun, errorValue := briefing.CanRun(context.Background(), schedule)
	if errorValue != nil || !canRun {
		t.Fatalf("expected enabled schedule to run: %v", errorValue)
	}
	conversationID = "conversation-2"
	canRun, errorValue = briefing.CanRun(context.Background(), schedule)
	if errorValue != nil || canRun {
		t.Fatalf("expected stale direct message target not to run: %v", errorValue)
	}
	callsBeforeDisable := directMessageCalls
	settings.Enabled = false
	document, errorValue = persona.CanonicalUser(persona.User{MorningBriefing: &settings})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	writePersonaUser(t, rootPath, "person-1", document)
	canRun, errorValue = briefing.CanRun(context.Background(), schedule)
	if errorValue != nil || canRun {
		t.Fatalf("expected disabled schedule not to run: %v", errorValue)
	}
	if directMessageCalls != callsBeforeDisable {
		t.Fatal("expected disabled schedule not to resolve a direct message")
	}
	settings.Enabled = true
	settings.Time = "09:00"
	document, errorValue = persona.CanonicalUser(persona.User{MorningBriefing: &settings})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	writePersonaUser(t, rootPath, "person-1", document)
	canRun, errorValue = briefing.CanRun(context.Background(), schedule)
	if errorValue != nil || canRun {
		t.Fatalf("expected changed time not to run: %v", errorValue)
	}
}

func TestBriefingReconcileLeavesAPersonUnsettledWhenTheMessengerIsNotUp(t *testing.T) {
	rootPath := t.TempDir()
	settings := persona.DefaultMorningBriefing()
	document, errorValue := persona.CanonicalUser(persona.User{MorningBriefing: &settings})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	writePersonaUser(t, rootPath, "person-1", document)
	repository := &recordingBriefingRepository{}
	briefing := &Briefings{
		Repository: repository,
		Accounts:   recordingBriefingAccounts{accounts: []identity.PlatformAccountIdentity{{Platform: "buzz", ExternalUserID: "user-1", PersonID: "person-1"}}},
		PolicyDocument: func() policy.PolicyDocument {
			return policy.PolicyDocument{Company: policy.CompanyPolicy{TimeZone: "Asia/Seoul"}, People: []policy.PersonPolicy{{PersonID: "person-1"}}}
		},
		PersonAccessResolver: briefingPersonAccessResolver{},
		ActorFactory:         briefingActorFactory{rootPath: rootPath},
		WorkspaceRootPath:    rootPath,
		OpenDirectMessage: func(context.Context, string, string) (string, string, error) {
			return "", "", errors.New("dial tcp 127.0.0.1:18090: connect: connection refused")
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if errorValue := briefing.Reconcile(context.Background(), time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(repository.schedulesByKind[task.BriefingKindMorning]) != 0 {
		t.Fatalf("expected no settled schedule while the messenger is down, got %+v", repository.schedulesByKind)
	}
	if len(repository.unsettledPeopleByKind[task.BriefingKindMorning]) != 1 || repository.unsettledPeopleByKind[task.BriefingKindMorning][0] != "person-1" {
		t.Fatalf("expected person-1 to be left unsettled rather than removed, got %v", repository.unsettledPeopleByKind)
	}
}

func TestBriefingReconcileReturnsUserReadError(t *testing.T) {
	briefing := &Briefings{
		Repository: &recordingBriefingRepository{},
		Accounts:   recordingBriefingAccounts{},
		PolicyDocument: func() policy.PolicyDocument {
			return policy.PolicyDocument{Company: policy.CompanyPolicy{TimeZone: "Asia/Seoul"}, People: []policy.PersonPolicy{{PersonID: "person-1"}}}
		},
		PersonAccessResolver: briefingPersonAccessResolver{},
		ActorFactory:         briefingActorFactory{requestError: errors.New("read unavailable")},
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if errorValue := briefing.Reconcile(context.Background(), time.Now()); errorValue != nil {
		t.Fatalf("expected per-person read error to be reconciled as unavailable, got %v", errorValue)
	}
}

func TestWeeklyReportScheduleRunsOnTheConfiguredDayAndTime(t *testing.T) {
	for _, testCase := range []struct {
		day  string
		time string
		cron string
	}{
		{day: "friday", time: "12:00", cron: "0 12 * * 5"},
		{day: "sunday", time: "18:30", cron: "30 18 * * 0"},
	} {
		settings, errorValue := weeklyReportSettings(persona.User{WeeklyReport: &persona.WeeklyReport{Enabled: true, Day: testCase.day, Time: testCase.time}})
		if errorValue != nil || settings.cronExpression != testCase.cron || !settings.isEnabled {
			t.Fatalf("%s %s: expected %q, got %+v (%v)", testCase.day, testCase.time, testCase.cron, settings, errorValue)
		}
	}
}

func TestBriefingReconcileSettlesEveryKindForEachPerson(t *testing.T) {
	rootPath := t.TempDir()
	document, errorValue := persona.CanonicalUser(persona.NormalizeUser(persona.User{}))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	writePersonaUser(t, rootPath, "person-1", document)
	repository := &recordingBriefingRepository{}
	briefings := &Briefings{
		Repository: repository,
		Accounts:   recordingBriefingAccounts{accounts: []identity.PlatformAccountIdentity{{Platform: "buzz", ExternalUserID: "user-1", PersonID: "person-1"}}},
		PolicyDocument: func() policy.PolicyDocument {
			return policy.PolicyDocument{Company: policy.CompanyPolicy{TimeZone: "Asia/Seoul"}, People: []policy.PersonPolicy{{PersonID: "person-1"}}}
		},
		PersonAccessResolver: briefingPersonAccessResolver{},
		ActorFactory:         briefingActorFactory{rootPath: rootPath},
		WorkspaceRootPath:    rootPath,
		OpenDirectMessage: func(context.Context, string, string) (string, string, error) {
			return "conversation-1", "reply-1", nil
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if errorValue := briefings.Reconcile(context.Background(), time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)); errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, kind := range task.BriefingKinds {
		schedules := repository.schedulesByKind[kind]
		if len(schedules) != 1 || schedules[0].ScheduleID != task.BriefingScheduleID(kind, "person-1") || schedules[0].NextRunAt == nil {
			t.Fatalf("expected one running %s briefing for person-1, got %+v", kind, schedules)
		}
	}
	weekly := repository.schedulesByKind[task.BriefingKindWeekly][0]
	if weekly.CronExpression != "0 12 * * 5" || weekly.ConversationID != "conversation-1" {
		t.Fatalf("expected the default weekly report on Friday noon in the direct message, got %+v", weekly)
	}
}

func TestEveryBriefingKindHasADefinition(t *testing.T) {
	for _, kind := range task.BriefingKinds {
		if _, isDefined := briefingDefinitions[kind]; !isDefined {
			t.Fatalf("briefing kind %s has no definition", kind)
		}
	}
	if len(briefingDefinitions) != len(task.BriefingKinds) {
		t.Fatalf("briefing definitions %d do not match kinds %v", len(briefingDefinitions), task.BriefingKinds)
	}
}

func writePersonaUser(t *testing.T, rootPath string, personID string, document []byte) {
	t.Helper()
	path := filepath.Join(security.PersonHomeDirectoryPath(rootPath, personID), persona.UserDocumentRelativePath)
	if errorValue := os.MkdirAll(filepath.Dir(path), 0o750); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(path, document, 0o640); errorValue != nil {
		t.Fatal(errorValue)
	}
}

type recordingBriefingRepository struct {
	schedulesByKind       map[task.BriefingKind][]task.Schedule
	unsettledPeopleByKind map[task.BriefingKind][]string
}

func (repository *recordingBriefingRepository) ReconcileBriefings(_ context.Context, kind task.BriefingKind, schedules []task.Schedule, unsettledPeople []string, _ time.Time) error {
	if repository.schedulesByKind == nil {
		repository.schedulesByKind = map[task.BriefingKind][]task.Schedule{}
		repository.unsettledPeopleByKind = map[task.BriefingKind][]string{}
	}
	repository.schedulesByKind[kind] = schedules
	repository.unsettledPeopleByKind[kind] = unsettledPeople
	return nil
}

type recordingBriefingAccounts struct {
	accounts []identity.PlatformAccountIdentity
}

func (accounts recordingBriefingAccounts) ListPlatformAccount() ([]identity.PlatformAccountIdentity, error) {
	return accounts.accounts, nil
}

type briefingPersonAccessResolver struct{}

func (briefingPersonAccessResolver) ResolvePersonAccess(personID string) policy.PersonAccess {
	return policy.PersonAccess{PersonID: personID}
}

type briefingActorFactory struct {
	rootPath     string
	requestError error
}

func (factory briefingActorFactory) Requester(context.Context, security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	if factory.requestError != nil {
		return nil, factory.requestError
	}
	return briefingActor{rootPath: factory.rootPath}, nil
}

func (factory briefingActorFactory) CanListDirectory(context.Context) bool { return false }

type briefingActor struct{ rootPath string }

func (actor briefingActor) Run(context.Context, security.CommandRequest) (security.CommandResult, error) {
	return security.CommandResult{}, errors.New("unsupported")
}
func (actor briefingActor) MkdirAll(context.Context, string) error {
	return errors.New("unsupported")
}
func (actor briefingActor) WriteFile(context.Context, string, []byte) error {
	return errors.New("unsupported")
}
func (actor briefingActor) ReadFile(_ context.Context, path string, _ int64) ([]byte, error) {
	return os.ReadFile(path)
}
func (actor briefingActor) ListDirectory(context.Context, string) ([]security.WorkspaceActorDirectoryEntry, error) {
	return nil, errors.New("unsupported")
}
func (actor briefingActor) Stat(context.Context, string) (security.WorkspaceActorStat, error) {
	return security.WorkspaceActorStat{}, errors.New("unsupported")
}
