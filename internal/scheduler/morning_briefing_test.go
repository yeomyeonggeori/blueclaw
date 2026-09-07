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
			schedule, errorValue := newMorningBriefingSchedule("person-1", persona.MorningBriefing{Enabled: true, Time: testCase.timeText}, testCase.timeZone, referenceTime)
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
	_, errorValue := newMorningBriefingSchedule("person-1", persona.DefaultMorningBriefing(), "Mars/Olympus", time.Now())
	if errorValue == nil {
		t.Fatal("expected invalid timezone error")
	}
}

func TestMorningBriefingAccountRequiresOneAccountPerPlatform(t *testing.T) {
	accounts := []identity.PlatformAccountIdentity{
		{Platform: "buzz", ExternalUserID: "z", PersonID: "person-1"},
		{Platform: "mattermost", ExternalUserID: "mattermost-1", PersonID: "person-1"},
	}
	account, isFound := morningBriefingAccount("person-1", accounts)
	if !isFound || account.Platform != "buzz" {
		t.Fatalf("expected deterministic platform selection, got %+v, %v", account, isFound)
	}
	accounts = append(accounts, identity.PlatformAccountIdentity{Platform: "buzz", ExternalUserID: "a", PersonID: "person-1"})
	if _, isFound = morningBriefingAccount("person-1", accounts); isFound {
		t.Fatal("expected same-platform ambiguity to fail closed")
	}
}

func TestMorningBriefingReconcileReadsUsersAndRemovesMissingRosterEntries(t *testing.T) {
	rootPath := t.TempDir()
	firstUser := persona.DefaultMorningBriefing()
	firstDocument, errorValue := persona.CanonicalUser(persona.User{MorningBriefing: &firstUser})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	writePersonaUser(t, rootPath, "person-1", firstDocument)
	writePersonaUser(t, rootPath, "person-2", firstDocument)
	repository := &recordingMorningBriefingRepository{}
	briefing := &MorningBriefing{
		Repository: repository,
		Accounts:   recordingMorningBriefingAccounts{accounts: []identity.PlatformAccountIdentity{{Platform: "buzz", ExternalUserID: "user-1", PersonID: "person-1"}}},
		PolicyDocument: func() policy.PolicyDocument {
			return policy.PolicyDocument{Company: policy.CompanyPolicy{TimeZone: "Asia/Seoul"}, People: []policy.PersonPolicy{{PersonID: "person-1"}, {PersonID: "person-2"}}}
		},
		PersonAccessResolver: morningBriefingPersonAccessResolver{},
		ActorFactory:         morningBriefingActorFactory{rootPath: rootPath},
		WorkspaceRootPath:    rootPath,
		OpenDirectMessage: func(context.Context, string, string) (string, string, error) {
			return "conversation-1", "reply-1", nil
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if errorValue := briefing.Reconcile(context.Background(), time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(repository.schedules) != 2 || repository.schedules[0].TaskScheduleID != task.MorningBriefingScheduleID("person-1") {
		t.Fatalf("expected one schedule per roster person, got %+v", repository.schedules)
	}
	if repository.schedules[0].ConversationID != "conversation-1" || repository.schedules[0].ReplyTargetID != "reply-1" {
		t.Fatalf("expected resolved direct message target, got %+v", repository.schedules[0])
	}
	if repository.schedules[1].NextRunAt != nil {
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
	briefing := &MorningBriefing{
		Accounts: recordingMorningBriefingAccounts{accounts: []identity.PlatformAccountIdentity{{Platform: "buzz", ExternalUserID: "user-1", PersonID: "person-1"}}},
		PolicyDocument: func() policy.PolicyDocument {
			return policy.PolicyDocument{Company: policy.CompanyPolicy{TimeZone: "Asia/Seoul"}, People: []policy.PersonPolicy{{PersonID: "person-1"}}}
		},
		PersonAccessResolver: morningBriefingPersonAccessResolver{},
		ActorFactory:         morningBriefingActorFactory{rootPath: rootPath},
		WorkspaceRootPath:    rootPath,
		OpenDirectMessage: func(context.Context, string, string) (string, string, error) {
			directMessageCalls++
			return conversationID, replyTargetID, nil
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	schedule, errorValue := newMorningBriefingSchedule("person-1", settings, "Asia/Seoul", time.Now())
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

func TestMorningBriefingReconcileReturnsUserReadError(t *testing.T) {
	briefing := &MorningBriefing{
		Repository: &recordingMorningBriefingRepository{},
		Accounts:   recordingMorningBriefingAccounts{},
		PolicyDocument: func() policy.PolicyDocument {
			return policy.PolicyDocument{Company: policy.CompanyPolicy{TimeZone: "Asia/Seoul"}, People: []policy.PersonPolicy{{PersonID: "person-1"}}}
		},
		PersonAccessResolver: morningBriefingPersonAccessResolver{},
		ActorFactory:         morningBriefingActorFactory{requestError: errors.New("read unavailable")},
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if errorValue := briefing.Reconcile(context.Background(), time.Now()); errorValue != nil {
		t.Fatalf("expected per-person read error to be reconciled as unavailable, got %v", errorValue)
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

type recordingMorningBriefingRepository struct{ schedules []task.TaskSchedule }

func (repository *recordingMorningBriefingRepository) ReconcileMorningBriefings(_ context.Context, schedules []task.TaskSchedule, _ time.Time) error {
	repository.schedules = schedules
	return nil
}

type recordingMorningBriefingAccounts struct {
	accounts []identity.PlatformAccountIdentity
}

func (accounts recordingMorningBriefingAccounts) ListPlatformAccount() ([]identity.PlatformAccountIdentity, error) {
	return accounts.accounts, nil
}

type morningBriefingPersonAccessResolver struct{}

func (morningBriefingPersonAccessResolver) ResolvePersonAccess(personID string) policy.PersonAccess {
	return policy.PersonAccess{PersonID: personID}
}

type morningBriefingActorFactory struct {
	rootPath     string
	requestError error
}

func (factory morningBriefingActorFactory) Requester(context.Context, security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	if factory.requestError != nil {
		return nil, factory.requestError
	}
	return morningBriefingActor{rootPath: factory.rootPath}, nil
}

func (factory morningBriefingActorFactory) CanListDirectory(context.Context) bool { return false }

type morningBriefingActor struct{ rootPath string }

func (actor morningBriefingActor) Run(context.Context, security.CommandRequest) (security.CommandResult, error) {
	return security.CommandResult{}, errors.New("unsupported")
}
func (actor morningBriefingActor) MkdirAll(context.Context, string) error {
	return errors.New("unsupported")
}
func (actor morningBriefingActor) WriteFile(context.Context, string, []byte) error {
	return errors.New("unsupported")
}
func (actor morningBriefingActor) ReadFile(_ context.Context, path string, _ int64) ([]byte, error) {
	return os.ReadFile(path)
}
func (actor morningBriefingActor) BundleDirectory(context.Context, string, security.WorkspaceActorBundleOptions) (security.WorkspaceActorBundle, error) {
	return security.WorkspaceActorBundle{}, errors.New("unsupported")
}
func (actor morningBriefingActor) ListDirectory(context.Context, string) ([]security.WorkspaceActorDirectoryEntry, error) {
	return nil, errors.New("unsupported")
}
func (actor morningBriefingActor) Stat(context.Context, string) (security.WorkspaceActorStat, error) {
	return security.WorkspaceActorStat{}, errors.New("unsupported")
}
