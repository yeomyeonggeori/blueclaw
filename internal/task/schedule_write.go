package task

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrScheduleRequesterRequired       = errors.New("requester is required")
	ErrScheduleConversationRequired    = errors.New("platform and conversationID are required")
	ErrScheduleReplyTargetRequired     = errors.New("replyTargetID is required")
	ErrScheduleTaskInstructionRequired = errors.New("taskInstruction is required")
	ErrScheduleUpdateFieldRequired     = errors.New("at least one field to change is required")
	ErrScheduleKindInvalid             = errors.New("kind must be once, interval or cron")
	ErrScheduleTimeZoneInvalid         = errors.New("timeZone must be a valid IANA time zone")
	ErrScheduleRunAtInvalid            = errors.New("runAt must be RFC3339")
	ErrScheduleExpiresAtInvalid        = errors.New("expiresAt must be a future RFC3339 timestamp")
	ErrScheduleRepeatPolicyRequired    = errors.New("interval and cron schedules require repeatPolicy finite or unbounded")
	ErrScheduleFiniteBoundRequired     = errors.New("finite interval and cron schedules require expiresAt or maxRunCount")
	ErrScheduleNoFutureRun             = errors.New("task schedule has no future run")
)

type ScheduleCreateInput struct {
	Description     string
	TaskInstruction string
	Kind            string
	RunAt           string
	ExpiresAt       string
	IntervalSecond  int
	CronExpression  string
	TimeZone        string
	MaxRunCount     int
	RepeatPolicy    string
}

type ScheduleUpdateInput struct {
	Description      *string
	TaskInstruction  *string
	AgentProfileName *string
	Kind             *string
	RunAt            *string
	ExpiresAt        *string
	IntervalSecond   *int
	CronExpression   *string
	TimeZone         *string
	MaxRunCount      *int
	RepeatPolicy     *string
}

type ScheduleDeliveryBinding struct {
	Platform       string
	ConversationID string
	ReplyTargetID  string
}

type ScheduleCreateContext struct {
	CreatorPersonID  string
	AgentProfileName string
	Delivery         ScheduleDeliveryBinding
	CompanyTimeZone  string
	ReferenceTime    time.Time
}

type ScheduleMutationResult struct {
	ScheduleID       string     `json:"scheduleID"`
	Description      string     `json:"description"`
	TaskInstruction  string     `json:"taskInstruction"`
	TimeZone         string     `json:"timeZone"`
	Kind             string     `json:"kind"`
	RunAt            *time.Time `json:"runAt,omitempty"`
	IntervalSecond   int        `json:"intervalSecond,omitempty"`
	CronExpression   string     `json:"cronExpression,omitempty"`
	MaxRunCount      int        `json:"maxRunCount,omitempty"`
	ExpiresAt        *time.Time `json:"expiresAt,omitempty"`
	NextRunAt        *time.Time `json:"nextRunAt,omitempty"`
	ConversationID   string     `json:"conversationID"`
	ReplyTargetID    string     `json:"replyTargetID"`
	AgentProfileName string     `json:"agentProfileName"`
}

func BuildScheduleCreate(input ScheduleCreateInput, createContext ScheduleCreateContext) (Schedule, error) {
	if errorValue := requireScheduleDelivery(createContext); errorValue != nil {
		return Schedule{}, errorValue
	}
	taskInstruction := strings.TrimSpace(input.TaskInstruction)
	if taskInstruction == "" {
		return Schedule{}, ErrScheduleTaskInstructionRequired
	}
	kind, errorValue := parseScheduleKind(input.Kind)
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	timeZone, errorValue := normalizeScheduleTimeZone(input.TimeZone, createContext.CompanyTimeZone)
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	runAt, errorValue := parseScheduleRunAt(input.RunAt)
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	expiresAt, errorValue := parseScheduleExpiresAt(input.ExpiresAt, createContext.ReferenceTime)
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	schedule := Schedule{
		ScheduleID:       NewIdentifier(),
		CreatorPersonID:  strings.TrimSpace(createContext.CreatorPersonID),
		Name:             firstNonBlankScheduleValue(input.Description, taskInstruction),
		Prompt:           taskInstruction,
		ExecutionMode:    ScheduleExecutionModeAgent,
		AgentProfileName: firstNonBlankScheduleValue(createContext.AgentProfileName, "default"),
		Platform:         strings.TrimSpace(createContext.Delivery.Platform),
		ConversationID:   strings.TrimSpace(createContext.Delivery.ConversationID),
		ReplyTargetID:    strings.TrimSpace(createContext.Delivery.ReplyTargetID),
		TimeZone:         timeZone,
		Kind:             kind,
		RunAt:            runAt,
		IntervalSecond:   input.IntervalSecond,
		CronExpression:   strings.TrimSpace(input.CronExpression),
		MaxRunCount:      input.MaxRunCount,
		ExpiresAt:        expiresAt,
		CreatedAt:        createContext.ReferenceTime,
		UpdatedAt:        createContext.ReferenceTime,
		NextAttemptAt:    &createContext.ReferenceTime,
	}
	if errorValue := validateScheduleRepeatPolicy(schedule, input.RepeatPolicy); errorValue != nil {
		return Schedule{}, errorValue
	}
	return schedule, nil
}

func InitializeScheduleCreate(input ScheduleCreateInput, createContext ScheduleCreateContext) (Schedule, error) {
	schedule, errorValue := BuildScheduleCreate(input, createContext)
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	return initializedScheduleWithFutureRun(schedule, createContext.ReferenceTime)
}

func ApplyScheduleUpdate(schedule Schedule, input ScheduleUpdateInput, companyTimeZone string, referenceTime time.Time) (Schedule, error) {
	if input.Description != nil {
		schedule.Name = strings.TrimSpace(*input.Description)
	}
	if input.TaskInstruction != nil {
		taskInstruction := strings.TrimSpace(*input.TaskInstruction)
		if taskInstruction == "" {
			return Schedule{}, ErrScheduleTaskInstructionRequired
		}
		schedule.Prompt = taskInstruction
	}
	if input.AgentProfileName != nil {
		schedule.AgentProfileName = strings.TrimSpace(*input.AgentProfileName)
	}
	if input.TimeZone != nil {
		timeZone, errorValue := normalizeScheduleTimeZone(*input.TimeZone, companyTimeZone)
		if errorValue != nil {
			return Schedule{}, errorValue
		}
		schedule.TimeZone = timeZone
	}
	updatedSchedule, errorValue := applyScheduleUpdateCadence(schedule, input, referenceTime)
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	updatedSchedule.UpdatedAt = referenceTime
	updatedSchedule.NextAttemptAt = &referenceTime
	return initializedScheduleWithFutureRun(updatedSchedule, referenceTime)
}

func ScheduleUpdateChangesNothing(input ScheduleUpdateInput) bool {
	return input.Description == nil && input.TaskInstruction == nil && input.AgentProfileName == nil &&
		input.Kind == nil && input.RunAt == nil && input.ExpiresAt == nil &&
		input.IntervalSecond == nil && input.CronExpression == nil &&
		input.TimeZone == nil && input.MaxRunCount == nil && input.RepeatPolicy == nil
}

func ProjectScheduleMutation(schedule Schedule) ScheduleMutationResult {
	return ScheduleMutationResult{
		ScheduleID:       schedule.ScheduleID,
		Description:      schedule.Name,
		TaskInstruction:  schedule.Prompt,
		TimeZone:         schedule.TimeZone,
		Kind:             string(schedule.Kind),
		RunAt:            schedule.RunAt,
		IntervalSecond:   schedule.IntervalSecond,
		CronExpression:   schedule.CronExpression,
		MaxRunCount:      schedule.MaxRunCount,
		ExpiresAt:        schedule.ExpiresAt,
		NextRunAt:        schedule.NextRunAt,
		ConversationID:   schedule.ConversationID,
		ReplyTargetID:    schedule.ReplyTargetID,
		AgentProfileName: schedule.AgentProfileName,
	}
}

func requireScheduleDelivery(createContext ScheduleCreateContext) error {
	if strings.TrimSpace(createContext.CreatorPersonID) == "" {
		return ErrScheduleRequesterRequired
	}
	if strings.TrimSpace(createContext.Delivery.Platform) == "" || strings.TrimSpace(createContext.Delivery.ConversationID) == "" {
		return ErrScheduleConversationRequired
	}
	if strings.TrimSpace(createContext.Delivery.ReplyTargetID) == "" {
		return ErrScheduleReplyTargetRequired
	}
	return nil
}

func normalizeScheduleTimeZone(value string, companyTimeZone string) (string, error) {
	timeZone := ScheduleTimeZoneName(firstNonBlankScheduleValue(value, companyTimeZone))
	if _, errorValue := time.LoadLocation(timeZone); errorValue != nil {
		return "", ErrScheduleTimeZoneInvalid
	}
	return timeZone, nil
}

func IsScheduleWriteInputError(errorValue error) bool {
	for _, inputError := range []error{
		ErrScheduleRequesterRequired,
		ErrScheduleConversationRequired,
		ErrScheduleReplyTargetRequired,
		ErrScheduleTaskInstructionRequired,
		ErrScheduleUpdateFieldRequired,
		ErrScheduleKindInvalid,
		ErrScheduleTimeZoneInvalid,
		ErrScheduleRunAtInvalid,
		ErrScheduleExpiresAtInvalid,
		ErrScheduleRepeatPolicyRequired,
		ErrScheduleFiniteBoundRequired,
		ErrScheduleNoFutureRun,
		ErrScheduleLimitReached,
		errorInvalidSchedule,
		errorInvalidCronExpression,
		errorUnableToFindNextTaskRun,
	} {
		if errors.Is(errorValue, inputError) {
			return true
		}
	}
	return false
}

func applyScheduleUpdateCadence(schedule Schedule, input ScheduleUpdateInput, referenceTime time.Time) (Schedule, error) {
	if input.Kind != nil {
		kind, errorValue := parseScheduleKind(*input.Kind)
		if errorValue != nil {
			return Schedule{}, errorValue
		}
		schedule.Kind = kind
	}
	if input.RunAt != nil {
		runAt, errorValue := parseScheduleRunAt(*input.RunAt)
		if errorValue != nil {
			return Schedule{}, errorValue
		}
		schedule.RunAt = runAt
	}
	if input.ExpiresAt != nil {
		expiresAt, errorValue := parseScheduleExpiresAt(*input.ExpiresAt, referenceTime)
		if errorValue != nil {
			return Schedule{}, errorValue
		}
		schedule.ExpiresAt = expiresAt
	}
	if input.IntervalSecond != nil {
		schedule.IntervalSecond = *input.IntervalSecond
	}
	if input.CronExpression != nil {
		schedule.CronExpression = strings.TrimSpace(*input.CronExpression)
	}
	if input.MaxRunCount != nil {
		schedule.MaxRunCount = *input.MaxRunCount
	}
	normalizeScheduleCadenceFields(&schedule)
	if errorValue := validateScheduleRepeatPolicy(schedule, scheduleRepeatPolicyOf(input)); errorValue != nil {
		return Schedule{}, errorValue
	}
	return schedule, nil
}

func initializedScheduleWithFutureRun(schedule Schedule, referenceTime time.Time) (Schedule, error) {
	initializedSchedule, errorValue := (Scheduler{}).InitializeSchedule(schedule, referenceTime)
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	if initializedSchedule.NextRunAt == nil {
		return Schedule{}, ErrScheduleNoFutureRun
	}
	return initializedSchedule, nil
}

func normalizeScheduleCadenceFields(schedule *Schedule) {
	switch schedule.Kind {
	case ScheduleKindOnce:
		schedule.IntervalSecond = 0
		schedule.CronExpression = ""
		schedule.MaxRunCount = 0
	case ScheduleKindInterval:
		schedule.CronExpression = ""
	case ScheduleKindCron:
		schedule.IntervalSecond = 0
	}
}

func validateScheduleRepeatPolicy(schedule Schedule, repeatPolicy string) error {
	if schedule.Kind != ScheduleKindInterval && schedule.Kind != ScheduleKindCron {
		return nil
	}
	if schedule.MaxRunCount > 0 || schedule.ExpiresAt != nil {
		return nil
	}
	switch strings.TrimSpace(repeatPolicy) {
	case "unbounded":
		return nil
	case "finite":
		return ErrScheduleFiniteBoundRequired
	default:
		return ErrScheduleRepeatPolicyRequired
	}
}

func scheduleRepeatPolicyOf(input ScheduleUpdateInput) string {
	if input.RepeatPolicy == nil {
		return ""
	}
	return *input.RepeatPolicy
}

func parseScheduleKind(value string) (ScheduleKind, error) {
	switch strings.TrimSpace(value) {
	case string(ScheduleKindOnce):
		return ScheduleKindOnce, nil
	case string(ScheduleKindInterval):
		return ScheduleKindInterval, nil
	case string(ScheduleKindCron):
		return ScheduleKindCron, nil
	default:
		return "", ErrScheduleKindInvalid
	}
}

func parseScheduleRunAt(value string) (*time.Time, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil, nil
	}
	runAt, errorValue := time.Parse(time.RFC3339, trimmedValue)
	if errorValue != nil {
		return nil, ErrScheduleRunAtInvalid
	}
	return &runAt, nil
}

func parseScheduleExpiresAt(value string, referenceTime time.Time) (*time.Time, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil, nil
	}
	expiresAt, errorValue := time.Parse(time.RFC3339, trimmedValue)
	if errorValue != nil {
		return nil, ErrScheduleExpiresAtInvalid
	}
	expiresAt = expiresAt.UTC()
	if !expiresAt.After(referenceTime) {
		return nil, ErrScheduleExpiresAtInvalid
	}
	return &expiresAt, nil
}

func firstNonBlankScheduleValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
