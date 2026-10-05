package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func (runner ScheduleRunner) skipEmptyMorningBriefing(ctx context.Context, request ScheduleRunRequest, referenceTime time.Time) bool {
	if !task.IsMorningBriefing(request.Schedule) {
		return false
	}
	requesterEmail := runner.taskLauncher.resolveRequesterEmail(TaskLaunchRequest{RequesterPersonID: request.Schedule.CreatorPersonID})
	toolSet := runner.morningBriefingToolSet(request, requesterEmail)
	isEmpty, errorValue := morningBriefingIsEmpty(ctx, toolSet, morningBriefingRequester{personID: request.Schedule.CreatorPersonID, email: requesterEmail}, request.Schedule.TimeZone, referenceTime)
	if errorValue != nil {
		slog.Warn("morning_briefing.preflight_failed", "taskScheduleID", request.Schedule.ScheduleID, "error", errorValue)
		return false
	}
	if isEmpty {
		slog.Info("morning_briefing.skipped_empty", "taskScheduleID", request.Schedule.ScheduleID, "referenceTime", referenceTime)
	}
	return isEmpty
}

func (runner ScheduleRunner) morningBriefingToolSet(request ScheduleRunRequest, requesterEmail string) *toolcontract.ToolSet {
	return runner.taskLauncher.toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:               request.Schedule.AgentProfileName,
		RequesterPersonID:         request.Schedule.CreatorPersonID,
		RequesterEmail:            requesterEmail,
		PersonAccess:              request.PersonAccess,
		TaskSource:                TaskLaunchSourceScheduled,
		IsScheduledRun:            true,
		Platform:                  request.Schedule.Platform,
		ConversationID:            task.ScheduleSessionID(request.Schedule.ScheduleID),
		RegisteredToolNameCeiling: []string{"task_list", "event_list"},
	})
}

type morningBriefingRequester struct {
	personID string
	email    string
}

type morningBriefingEvent struct {
	Source       string `json:"source"`
	Participants []struct {
		PersonID string `json:"personID"`
		Email    string `json:"email"`
	} `json:"participants"`
}

func (requester morningBriefingRequester) isOnScheduleOf(event morningBriefingEvent) bool {
	if len(event.Participants) == 0 {
		return event.Source != "leave"
	}
	for _, participant := range event.Participants {
		if participant.PersonID != "" && participant.PersonID == requester.personID {
			return true
		}
		if participant.Email != "" && strings.EqualFold(participant.Email, requester.email) {
			return true
		}
	}
	return false
}

func morningBriefingIsEmpty(ctx context.Context, toolSet *toolcontract.ToolSet, requester morningBriefingRequester, timeZone string, referenceTime time.Time) (bool, error) {
	requesterEmail := requester.email
	if strings.TrimSpace(requesterEmail) == "" {
		return false, errors.New("morning briefing requester email is required")
	}
	input, errorValue := json.Marshal(struct {
		PersonHints []string `json:"personHints"`
		EveryWeek   bool     `json:"everyWeek"`
		Limit       int      `json:"limit"`
	}{[]string{requesterEmail}, true, 1})
	if errorValue != nil {
		return false, errorValue
	}
	var tasks struct {
		Tasks           []json.RawMessage `json:"tasks"`
		Count           *int              `json:"count"`
		UnfinishedCount *int              `json:"unfinishedCount"`
	}
	if errorValue := readMorningBriefingRecords(ctx, toolSet, "task_list", input, &tasks); errorValue != nil {
		return false, errorValue
	}
	if tasks.Tasks == nil || tasks.Count == nil || *tasks.Count != len(tasks.Tasks) || tasks.UnfinishedCount == nil || *tasks.UnfinishedCount < 0 {
		return false, errors.New("task_list did not return a complete unfinished task count")
	}
	if *tasks.UnfinishedCount > 0 {
		return false, nil
	}
	return morningBriefingCalendarIsEmpty(ctx, toolSet, requester, timeZone, referenceTime)
}

func morningBriefingCalendarIsEmpty(ctx context.Context, toolSet *toolcontract.ToolSet, requester morningBriefingRequester, timeZone string, referenceTime time.Time) (bool, error) {
	input, errorValue := morningBriefingCalendarInput(timeZone, referenceTime)
	if errorValue != nil {
		return false, errorValue
	}
	var calendar struct {
		Events []morningBriefingEvent `json:"events"`
	}
	if errorValue := readMorningBriefingRecords(ctx, toolSet, "event_list", input, &calendar); errorValue != nil {
		return false, errorValue
	}
	if calendar.Events == nil {
		return false, errors.New("event_list did not return an events array")
	}
	for _, event := range calendar.Events {
		if requester.isOnScheduleOf(event) {
			return false, nil
		}
	}
	return true, nil
}

func morningBriefingCalendarInput(timeZone string, referenceTime time.Time) (json.RawMessage, error) {
	if timeZone == "" {
		return nil, errors.New("morning briefing timezone is required")
	}
	location, errorValue := time.LoadLocation(timeZone)
	if errorValue != nil {
		return nil, errorValue
	}
	localTime := referenceTime.In(location)
	start := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 0, 0, 0, 0, location)
	return json.Marshal(struct {
		StartsAt string `json:"startsAt"`
		EndsAt   string `json:"endsAt"`
	}{start.Format(time.RFC3339), start.AddDate(0, 0, 1).Format(time.RFC3339)})
}

func readMorningBriefingRecords(ctx context.Context, toolSet *toolcontract.ToolSet, toolName string, input json.RawMessage, result any) error {
	definition, isAvailable := toolSet.ToolDefinition(toolName)
	if !isAvailable || definition.SideEffectClass != toolcontract.ToolSideEffectRead {
		return fmt.Errorf("morning briefing read tool %s is unavailable", toolName)
	}
	response, errorValue := toolSet.Invoke(ctx, toolcontract.ToolInvocation{ToolName: toolName, Input: input})
	if errorValue != nil {
		return fmt.Errorf("morning briefing %s: %w", toolName, errorValue)
	}
	if response.Failure != nil {
		return fmt.Errorf("morning briefing %s failed: %s", toolName, response.Failure.Code)
	}
	if errorValue := json.Unmarshal(response.Output.Data, result); errorValue != nil {
		return fmt.Errorf("morning briefing %s response: %w", toolName, errorValue)
	}
	return nil
}
