package scheduler

import (
	"context"
	"encoding/json"
	"errors"
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

type BriefingRepository interface {
	ReconcileBriefings(ctx context.Context, kind task.BriefingKind, desired []task.Schedule, unsettledPeople []string, referenceTime time.Time) error
}

type BriefingAccounts interface {
	ListPlatformAccount() ([]identity.PlatformAccountIdentity, error)
}

type Briefings struct {
	Repository           BriefingRepository
	Accounts             BriefingAccounts
	PolicyDocument       func() policy.PolicyDocument
	PersonAccessResolver PersonAccessResolver
	ActorFactory         security.WorkspaceActorFactory
	WorkspaceRootPath    string
	OpenDirectMessage    func(context.Context, string, string) (string, string, error)
	Logger               *slog.Logger
	directMessages       map[string]directMessageTarget
}

type directMessageTarget struct {
	conversationID string
	replyTargetID  string
}

type briefingSettings struct {
	isEnabled      bool
	cronExpression string
}

type briefingDefinition struct {
	settingsField string
	name          string
	prompt        string
	settingsOf    func(persona.User) (briefingSettings, error)
}

var briefingDefinitions = map[task.BriefingKind]briefingDefinition{
	task.BriefingKindMorning: {settingsField: "morningBriefing", name: "Morning briefing", prompt: morningBriefingPrompt, settingsOf: morningBriefingSettings},
	task.BriefingKindWeekly:  {settingsField: "weeklyReport", name: "Weekly report", prompt: weeklyReportPrompt, settingsOf: weeklyReportSettings},
}

var cronWeekdays = map[string]int{"sunday": 0, "monday": 1, "tuesday": 2, "wednesday": 3, "thursday": 4, "friday": 5, "saturday": 6}

func morningBriefingSettings(user persona.User) (briefingSettings, error) {
	clock, errorValue := time.Parse("15:04", user.MorningBriefing.Time)
	if errorValue != nil {
		return briefingSettings{}, errorValue
	}
	return briefingSettings{user.MorningBriefing.Enabled, fmt.Sprintf("%d %d * * *", clock.Minute(), clock.Hour())}, nil
}

func weeklyReportSettings(user persona.User) (briefingSettings, error) {
	clock, errorValue := time.Parse("15:04", user.WeeklyReport.Time)
	if errorValue != nil {
		return briefingSettings{}, errorValue
	}
	weekday, isKnown := cronWeekdays[user.WeeklyReport.Day]
	if !isKnown {
		return briefingSettings{}, fmt.Errorf("unknown weekly report day %q", user.WeeklyReport.Day)
	}
	return briefingSettings{user.WeeklyReport.Enabled, fmt.Sprintf("%d %d * * %d", clock.Minute(), clock.Hour(), weekday)}, nil
}

func (briefings *Briefings) Reconcile(ctx context.Context, referenceTime time.Time) error {
	document := briefings.PolicyDocument()
	if errorValue := validateBriefingTimeZone(document.Company.TimeZone); errorValue != nil {
		return errorValue
	}
	accounts, errorValue := briefings.Accounts.ListPlatformAccount()
	if errorValue != nil {
		return errorValue
	}
	desired := map[task.BriefingKind][]task.Schedule{}
	unsettledPeople := map[task.BriefingKind][]string{}
	for _, person := range document.People {
		user, errorValue := briefings.readUser(ctx, person.PersonID)
		for _, kind := range task.BriefingKinds {
			schedule, scheduleError := briefings.personSchedule(ctx, kind, person.PersonID, user, errorValue, accounts, document.Company.TimeZone, referenceTime)
			if scheduleError != nil {
				briefings.Logger.Warn("briefing.configuration_failed", "kind", kind, "personID", person.PersonID, "error", scheduleError)
				unsettledPeople[kind] = append(unsettledPeople[kind], person.PersonID)
				continue
			}
			desired[kind] = append(desired[kind], schedule)
		}
	}
	reconcileErrors := []error{}
	for _, kind := range task.BriefingKinds {
		reconcileErrors = append(reconcileErrors, briefings.Repository.ReconcileBriefings(ctx, kind, desired[kind], unsettledPeople[kind], referenceTime))
	}
	return errors.Join(reconcileErrors...)
}

func (briefings *Briefings) personSchedule(ctx context.Context, kind task.BriefingKind, personID string, user persona.User, readError error, accounts []identity.PlatformAccountIdentity, timeZone string, referenceTime time.Time) (task.Schedule, error) {
	if readError != nil {
		return task.Schedule{}, readError
	}
	settings, errorValue := briefingDefinitions[kind].settingsOf(user)
	if errorValue != nil {
		return task.Schedule{}, errorValue
	}
	schedule, errorValue := newBriefingSchedule(kind, personID, settings, timeZone, referenceTime)
	if errorValue != nil || !settings.isEnabled {
		return schedule, errorValue
	}
	account, isFound := identity.DirectMessageAccount(personID, accounts)
	if !isFound {
		schedule.NextRunAt = nil
		return schedule, nil
	}
	conversationID, replyTargetID, errorValue := briefings.directMessage(ctx, account)
	if errorValue != nil {
		return task.Schedule{}, errorValue
	}
	schedule.Platform = account.Platform
	schedule.ConversationID = conversationID
	schedule.ReplyTargetID = replyTargetID
	return schedule, nil
}

func (briefings *Briefings) readUser(ctx context.Context, personID string) (persona.User, error) {
	access := briefings.PersonAccessResolver.ResolvePersonAccess(personID)
	actor, errorValue := briefings.ActorFactory.Requester(ctx, security.WorkspaceActorRequest{PersonAccess: access, WorkspaceRootPath: briefings.WorkspaceRootPath})
	if errorValue != nil {
		return persona.User{}, errorValue
	}
	documentPath := filepath.Join(security.PersonHomeDirectoryPath(briefings.WorkspaceRootPath, personID), persona.UserDocumentRelativePath)
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
	isMissingSettings, errorValue := isMissingBriefingSettings(document)
	if errorValue != nil || !isMissingSettings {
		return user, errorValue
	}
	canonical, errorValue := persona.CanonicalUser(user)
	if errorValue != nil {
		return persona.User{}, errorValue
	}
	return user, actor.WriteFile(ctx, documentPath, canonical)
}

func isMissingBriefingSettings(document []byte) (bool, error) {
	var fields map[string]json.RawMessage
	if errorValue := json.Unmarshal(document, &fields); errorValue != nil {
		return false, errorValue
	}
	for _, definition := range briefingDefinitions {
		if _, hasSettings := fields[definition.settingsField]; !hasSettings {
			return true, nil
		}
	}
	return false, nil
}

func validateBriefingTimeZone(timeZone string) error {
	if timeZone == "" {
		return fmt.Errorf("company timezone is required for briefings")
	}
	_, errorValue := time.LoadLocation(timeZone)
	return errorValue
}

func newBriefingSchedule(kind task.BriefingKind, personID string, settings briefingSettings, timeZone string, referenceTime time.Time) (task.Schedule, error) {
	if errorValue := validateBriefingTimeZone(timeZone); errorValue != nil {
		return task.Schedule{}, errorValue
	}
	definition := briefingDefinitions[kind]
	schedule := task.Schedule{
		ScheduleID: task.BriefingScheduleID(kind, personID), CreatorPersonID: personID,
		Name: definition.name, Prompt: definition.prompt,
		ExecutionMode: task.ScheduleExecutionModeAgent, AgentProfileName: "default",
		Kind: task.ScheduleKindCron, CronExpression: settings.cronExpression,
		TimeZone: timeZone, CreatedAt: referenceTime, UpdatedAt: referenceTime,
	}
	if !settings.isEnabled {
		return schedule, nil
	}
	return (task.Scheduler{}).InitializeSchedule(schedule, referenceTime)
}

func (briefings *Briefings) directMessage(ctx context.Context, account identity.PlatformAccountIdentity) (string, string, error) {
	key := account.Platform + ":" + account.ExternalUserID
	if target, isFound := briefings.directMessages[key]; isFound {
		return target.conversationID, target.replyTargetID, nil
	}
	conversationID, replyTargetID, errorValue := briefings.OpenDirectMessage(ctx, account.Platform, account.ExternalUserID)
	if errorValue != nil {
		return "", "", errorValue
	}
	if conversationID == "" || replyTargetID == "" {
		return "", "", fmt.Errorf("direct message resolver returned an empty delivery target")
	}
	if briefings.directMessages == nil {
		briefings.directMessages = make(map[string]directMessageTarget)
	}
	briefings.directMessages[key] = directMessageTarget{conversationID, replyTargetID}
	return conversationID, replyTargetID, nil
}

func (briefings *Briefings) CanRun(ctx context.Context, schedule task.Schedule) (bool, error) {
	kind, isBriefing := task.BriefingKindOf(schedule)
	if !isBriefing {
		return true, nil
	}
	document := briefings.PolicyDocument()
	for _, person := range document.People {
		if person.PersonID == schedule.CreatorPersonID {
			return briefings.canRunFor(ctx, kind, person.PersonID, schedule, document.Company.TimeZone)
		}
	}
	return false, nil
}

func (briefings *Briefings) canRunFor(ctx context.Context, kind task.BriefingKind, personID string, schedule task.Schedule, timeZone string) (bool, error) {
	user, errorValue := briefings.readUser(ctx, personID)
	if errorValue != nil {
		return false, errorValue
	}
	settings, errorValue := briefingDefinitions[kind].settingsOf(user)
	if errorValue != nil || !settings.isEnabled {
		return false, errorValue
	}
	accounts, errorValue := briefings.Accounts.ListPlatformAccount()
	if errorValue != nil {
		return false, errorValue
	}
	account, isFound := identity.DirectMessageAccount(personID, accounts)
	if !isFound || account.Platform != schedule.Platform {
		return false, nil
	}
	delete(briefings.directMessages, account.Platform+":"+account.ExternalUserID)
	conversationID, replyTargetID, errorValue := briefings.directMessage(ctx, account)
	if errorValue != nil || conversationID != schedule.ConversationID || replyTargetID != schedule.ReplyTargetID {
		return false, errorValue
	}
	return settings.cronExpression == schedule.CronExpression && timeZone == schedule.TimeZone, nil
}

const morningBriefingPrompt = `Prepare only the requester's own morning work briefing for today's date in the scheduled company timezone. Query live calendar and work-task records with personHints set to the requester's exact person ID or email from the request context. Never use company-wide scope or another person's identity. Include an event, leave or work task only when its recorded participants include the requester, or when it is an event open to the whole company, which records no participants; visibility in the company calendar does not make it the requester's work. Shared items that include the requester are relevant, but do not turn other participants' work into obligations for the requester or include a company-wide roll call. Cover today's events and due work, work currently in progress, overdue unfinished work, and upcoming planned work (nearest dates first; keep a long backlog compact). Include useful times, deadlines and links returned by the tools. Distinguish an empty result from a failed or incomplete lookup. Never invent an event, task, deadline, status, participant or obligation. Retrieve memory only when additional context would help interpret a real item; memory never overrides the current work records. Use the requester's language and preferences. Return one concise, helpful briefing; the scheduler delivers this final response privately to the requester. This is a read-only briefing: do not create or change work, schedules, profiles or messages, and do not send a separate message.`

const weeklyReportPrompt = `Prepare only the requester's own weekly work report for the current week, Monday through today, in the scheduled company timezone. Query live work-task records with personHints set to the requester's exact person ID or email from the request context; read the calendar only when it helps explain that work. Never use company-wide scope or another person's identity. Include a task only when its recorded participants include the requester. Summarize the work completed this week grouped by topic, one line per topic that combines related tasks, then the work still in progress, then anything overdue or planned for next week. Keep the whole report short; do not list every task. Include useful dates and links returned by the tools. Distinguish an empty result from a failed or incomplete lookup. Never invent a task, outcome, status, date or participant. Retrieve memory only when additional context would help interpret a real item; memory never overrides the current work records. Use the requester's language and preferences. Return one concise report; the scheduler delivers this final response privately to the requester. This is a read-only report: do not create or change work, schedules, profiles or messages, and do not send a separate message.`
