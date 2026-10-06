package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/persona"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type MorningBriefingRepository interface {
	ReconcileMorningBriefings(ctx context.Context, desired []task.Schedule, unsettledPeople []string, referenceTime time.Time) error
}

type MorningBriefingAccounts interface {
	ListPlatformAccount() ([]identity.PlatformAccountIdentity, error)
}

type MorningBriefing struct {
	Repository           MorningBriefingRepository
	Accounts             MorningBriefingAccounts
	PolicyDocument       func() policy.PolicyDocument
	PersonAccessResolver PersonAccessResolver
	ActorFactory         security.WorkspaceActorFactory
	WorkspaceRootPath    string
	OpenDirectMessage    func(context.Context, string, string) (string, string, error)
	Logger               *slog.Logger
	directMessages       map[string]morningBriefingTarget
}

type morningBriefingTarget struct {
	conversationID string
	replyTargetID  string
}

func (briefing *MorningBriefing) Reconcile(ctx context.Context, referenceTime time.Time) error {
	document := briefing.PolicyDocument()
	if document.Company.TimeZone == "" {
		return fmt.Errorf("company timezone is required for morning briefing")
	}
	if _, errorValue := time.LoadLocation(document.Company.TimeZone); errorValue != nil {
		return errorValue
	}
	accounts, errorValue := briefing.Accounts.ListPlatformAccount()
	if errorValue != nil {
		return errorValue
	}
	schedules := make([]task.Schedule, 0, len(document.People))
	unsettledPeople := []string{}
	for _, person := range document.People {
		schedule, errorValue := briefing.personSchedule(ctx, person, accounts, document.Company.TimeZone, referenceTime)
		if errorValue != nil {
			briefing.Logger.Warn("morning_briefing.configuration_failed", "personID", person.PersonID, "error", errorValue)
			unsettledPeople = append(unsettledPeople, person.PersonID)
			continue
		}
		schedules = append(schedules, schedule)
	}
	return briefing.Repository.ReconcileMorningBriefings(ctx, schedules, unsettledPeople, referenceTime)
}

func (briefing *MorningBriefing) personSchedule(ctx context.Context, person policy.PersonPolicy, accounts []identity.PlatformAccountIdentity, timeZone string, referenceTime time.Time) (task.Schedule, error) {
	user, errorValue := briefing.readUser(ctx, person.PersonID)
	if errorValue != nil {
		return task.Schedule{}, errorValue
	}
	settings := *user.MorningBriefing
	schedule, errorValue := newMorningBriefingSchedule(person.PersonID, settings, timeZone, referenceTime)
	if errorValue != nil || !settings.Enabled {
		return schedule, errorValue
	}
	account, isFound := identity.DirectMessageAccount(person.PersonID, accounts)
	if !isFound {
		schedule.NextRunAt = nil
		return schedule, nil
	}
	conversationID, replyTargetID, errorValue := briefing.directMessage(ctx, account)
	if errorValue != nil {
		return task.Schedule{}, errorValue
	}
	schedule.Platform = account.Platform
	schedule.ConversationID = conversationID
	schedule.ReplyTargetID = replyTargetID
	return schedule, nil
}

func (briefing *MorningBriefing) readUser(ctx context.Context, personID string) (persona.User, error) {
	access := briefing.PersonAccessResolver.ResolvePersonAccess(personID)
	actor, errorValue := briefing.ActorFactory.Requester(ctx, security.WorkspaceActorRequest{PersonAccess: access, WorkspaceRootPath: briefing.WorkspaceRootPath})
	if errorValue != nil {
		return persona.User{}, errorValue
	}
	documentPath := filepath.Join(security.PersonHomeDirectoryPath(briefing.WorkspaceRootPath, personID), persona.UserDocumentRelativePath)
	document, errorValue := actor.ReadFile(ctx, documentPath, 64*1024)
	if security.IsActorNotFoundError(errorValue) {
		return persona.NormalizeUser(persona.User{}), nil
	}
	if errorValue != nil {
		return persona.User{}, errorValue
	}
	user, errorValue := persona.ParseUser(document)
	if errorValue != nil {
		return persona.User{}, errorValue
	}
	var fields map[string]json.RawMessage
	if errorValue := json.Unmarshal(document, &fields); errorValue != nil {
		return persona.User{}, errorValue
	}
	if _, hasSettings := fields["morningBriefing"]; !hasSettings {
		canonical, errorValue := persona.CanonicalUser(user)
		if errorValue != nil {
			return persona.User{}, errorValue
		}
		if errorValue := actor.WriteFile(ctx, documentPath, canonical); errorValue != nil {
			return persona.User{}, errorValue
		}
	}
	return user, nil
}

func newMorningBriefingSchedule(personID string, settings persona.MorningBriefing, timeZone string, referenceTime time.Time) (task.Schedule, error) {
	if timeZone == "" {
		return task.Schedule{}, fmt.Errorf("company timezone is required for morning briefing")
	}
	if _, errorValue := time.LoadLocation(timeZone); errorValue != nil {
		return task.Schedule{}, errorValue
	}
	clock, errorValue := time.Parse("15:04", settings.Time)
	if errorValue != nil {
		return task.Schedule{}, errorValue
	}
	schedule := task.Schedule{
		ScheduleID: task.MorningBriefingScheduleID(personID), CreatorPersonID: personID,
		Name: "Morning briefing", Prompt: morningBriefingPrompt,
		ExecutionMode: task.ScheduleExecutionModeAgent, AgentProfileName: "default",
		Kind: task.ScheduleKindCron, CronExpression: fmt.Sprintf("%d %d * * *", clock.Minute(), clock.Hour()),
		TimeZone: timeZone, CreatedAt: referenceTime, UpdatedAt: referenceTime,
	}
	if !settings.Enabled {
		return schedule, nil
	}
	return (task.Scheduler{}).InitializeSchedule(schedule, referenceTime)
}

func (briefing *MorningBriefing) directMessage(ctx context.Context, account identity.PlatformAccountIdentity) (string, string, error) {
	key := account.Platform + ":" + account.ExternalUserID
	if target, isFound := briefing.directMessages[key]; isFound {
		return target.conversationID, target.replyTargetID, nil
	}
	conversationID, replyTargetID, errorValue := briefing.OpenDirectMessage(ctx, account.Platform, account.ExternalUserID)
	if errorValue != nil {
		return "", "", errorValue
	}
	if conversationID == "" || replyTargetID == "" {
		return "", "", fmt.Errorf("direct message resolver returned an empty delivery target")
	}
	if briefing.directMessages == nil {
		briefing.directMessages = make(map[string]morningBriefingTarget)
	}
	briefing.directMessages[key] = morningBriefingTarget{conversationID, replyTargetID}
	return conversationID, replyTargetID, nil
}

func (briefing *MorningBriefing) CanRun(ctx context.Context, schedule task.Schedule) (bool, error) {
	if !task.IsMorningBriefing(schedule) {
		return true, nil
	}
	document := briefing.PolicyDocument()
	for _, person := range document.People {
		if person.PersonID != schedule.CreatorPersonID {
			continue
		}
		user, errorValue := briefing.readUser(ctx, person.PersonID)
		if errorValue != nil {
			return false, errorValue
		}
		if !user.MorningBriefing.Enabled {
			return false, nil
		}
		accounts, errorValue := briefing.Accounts.ListPlatformAccount()
		if errorValue != nil {
			return false, errorValue
		}
		account, isFound := identity.DirectMessageAccount(person.PersonID, accounts)
		if !isFound || account.Platform != schedule.Platform {
			return false, nil
		}
		delete(briefing.directMessages, account.Platform+":"+account.ExternalUserID)
		conversationID, replyTargetID, errorValue := briefing.directMessage(ctx, account)
		if errorValue != nil || conversationID != schedule.ConversationID || replyTargetID != schedule.ReplyTargetID {
			return false, errorValue
		}
		desired, errorValue := newMorningBriefingSchedule(person.PersonID, *user.MorningBriefing, document.Company.TimeZone, time.Now())
		return user.MorningBriefing.Enabled && desired.CronExpression == schedule.CronExpression && desired.TimeZone == schedule.TimeZone, errorValue
	}
	return false, nil
}

const morningBriefingPrompt = `Prepare only the requester's own morning work briefing for today's date in the scheduled company timezone. Query live calendar and work-task records with personHints set to the requester's exact person ID or email from the request context. Never use company-wide scope or another person's identity. Include an event, leave or work task only when its recorded participants include the requester, or when it is an event open to the whole company, which records no participants; visibility in the company calendar does not make it the requester's work. Shared items that include the requester are relevant, but do not turn other participants' work into obligations for the requester or include a company-wide roll call. Cover today's events and due work, work currently in progress, overdue unfinished work, and upcoming planned work (nearest dates first; keep a long backlog compact). Include useful times, deadlines and links returned by the tools. Distinguish an empty result from a failed or incomplete lookup. Never invent an event, task, deadline, status, participant or obligation. Retrieve memory only when additional context would help interpret a real item; memory never overrides the current work records. Use the requester's language and preferences. Return one concise, helpful briefing; the scheduler delivers this final response privately to the requester. This is a read-only briefing: do not create or change work, schedules, profiles or messages, and do not send a separate message.`
