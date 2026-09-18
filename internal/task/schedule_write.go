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
	Name             string     `json:"name"`
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

func BuildScheduleCreate(input ScheduleCreateInput, createContext ScheduleCreateContext) (TaskSchedule, error) {
	if errorValue := requireScheduleDelivery(createContext); errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	taskInstruction := strings.TrimSpace(input.TaskInstruction)
	if taskInstruction == "" {
		return TaskSchedule{}, ErrScheduleTaskInstructionRequired
	}
	kind, errorValue := parseScheduleKind(input.Kind)
	if errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	timeZone, errorValue := normalizeScheduleTimeZone(input.TimeZone, createContext.CompanyTimeZone)
	if errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	runAt, errorValue := parseScheduleRunAt(input.RunAt)
	if errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	expiresAt, errorValue := parseScheduleExpiresAt(input.ExpiresAt, createContext.ReferenceTime)
	if errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	taskSchedule := TaskSchedule{
		TaskScheduleID:   NewIdentifier(),
		CreatorPersonID:  strings.TrimSpace(createContext.CreatorPersonID),
		Name:             firstNonBlankScheduleValue(input.Description, taskInstruction),
		Prompt:           taskInstruction,
		ExecutionMode:    TaskScheduleExecutionModeAgent,
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
	if errorValue := validateScheduleRepeatPolicy(taskSchedule, input.RepeatPolicy); errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	return taskSchedule, nil
}

func InitializeScheduleCreate(input ScheduleCreateInput, createContext ScheduleCreateContext) (TaskSchedule, error) {
	taskSchedule, errorValue := BuildScheduleCreate(input, createContext)
	if errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	return initializedScheduleWithFutureRun(taskSchedule, createContext.ReferenceTime)
}

func ApplyScheduleUpdate(taskSchedule TaskSchedule, input ScheduleUpdateInput, companyTimeZone string, referenceTime time.Time) (TaskSchedule, error) {
	if input.Description != nil {
		taskSchedule.Name = strings.TrimSpace(*input.Description)
	}
	if input.TaskInstruction != nil {
		taskInstruction := strings.TrimSpace(*input.TaskInstruction)
		if taskInstruction == "" {
			return TaskSchedule{}, ErrScheduleTaskInstructionRequired
		}
		taskSchedule.Prompt = taskInstruction
	}
	if input.AgentProfileName != nil {
		taskSchedule.AgentProfileName = strings.TrimSpace(*input.AgentProfileName)
	}
	if input.TimeZone != nil {
		timeZone, errorValue := normalizeScheduleTimeZone(*input.TimeZone, companyTimeZone)
		if errorValue != nil {
			return TaskSchedule{}, errorValue
		}
		taskSchedule.TimeZone = timeZone
	}
	updatedTaskSchedule, errorValue := applyScheduleUpdateCadence(taskSchedule, input, referenceTime)
	if errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	updatedTaskSchedule.UpdatedAt = referenceTime
	updatedTaskSchedule.NextAttemptAt = &referenceTime
	return initializedScheduleWithFutureRun(updatedTaskSchedule, referenceTime)
}

func ScheduleUpdateChangesNothing(input ScheduleUpdateInput) bool {
	return input.Description == nil && input.TaskInstruction == nil && input.AgentProfileName == nil &&
		input.Kind == nil && input.RunAt == nil && input.ExpiresAt == nil &&
		input.IntervalSecond == nil && input.CronExpression == nil &&
		input.TimeZone == nil && input.MaxRunCount == nil && input.RepeatPolicy == nil
}

func ProjectScheduleMutation(taskSchedule TaskSchedule) ScheduleMutationResult {
	return ScheduleMutationResult{
		ScheduleID:       taskSchedule.TaskScheduleID,
		Name:             taskSchedule.Name,
		TaskInstruction:  taskSchedule.Prompt,
		TimeZone:         taskSchedule.TimeZone,
		Kind:             string(taskSchedule.Kind),
		RunAt:            taskSchedule.RunAt,
		IntervalSecond:   taskSchedule.IntervalSecond,
		CronExpression:   taskSchedule.CronExpression,
		MaxRunCount:      taskSchedule.MaxRunCount,
		ExpiresAt:        taskSchedule.ExpiresAt,
		NextRunAt:        taskSchedule.NextRunAt,
		ConversationID:   taskSchedule.ConversationID,
		ReplyTargetID:    taskSchedule.ReplyTargetID,
		AgentProfileName: taskSchedule.AgentProfileName,
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
		ErrScheduleKindInvalid,
		ErrScheduleTimeZoneInvalid,
		ErrScheduleRunAtInvalid,
		ErrScheduleExpiresAtInvalid,
		ErrScheduleRepeatPolicyRequired,
		ErrScheduleFiniteBoundRequired,
		ErrScheduleNoFutureRun,
	} {
		if errors.Is(errorValue, inputError) {
			return true
		}
	}
	return false
}

func applyScheduleUpdateCadence(taskSchedule TaskSchedule, input ScheduleUpdateInput, referenceTime time.Time) (TaskSchedule, error) {
	if input.Kind != nil {
		kind, errorValue := parseScheduleKind(*input.Kind)
		if errorValue != nil {
			return TaskSchedule{}, errorValue
		}
		taskSchedule.Kind = kind
	}
	if input.RunAt != nil {
		runAt, errorValue := parseScheduleRunAt(*input.RunAt)
		if errorValue != nil {
			return TaskSchedule{}, errorValue
		}
		taskSchedule.RunAt = runAt
	}
	if input.ExpiresAt != nil {
		expiresAt, errorValue := parseScheduleExpiresAt(*input.ExpiresAt, referenceTime)
		if errorValue != nil {
			return TaskSchedule{}, errorValue
		}
		taskSchedule.ExpiresAt = expiresAt
	}
	if input.IntervalSecond != nil {
		taskSchedule.IntervalSecond = *input.IntervalSecond
	}
	if input.CronExpression != nil {
		taskSchedule.CronExpression = strings.TrimSpace(*input.CronExpression)
	}
	if input.MaxRunCount != nil {
		taskSchedule.MaxRunCount = *input.MaxRunCount
	}
	normalizeScheduleCadenceFields(&taskSchedule)
	if errorValue := validateScheduleRepeatPolicy(taskSchedule, scheduleRepeatPolicyOf(input)); errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	return taskSchedule, nil
}

func initializedScheduleWithFutureRun(taskSchedule TaskSchedule, referenceTime time.Time) (TaskSchedule, error) {
	initializedTaskSchedule, errorValue := (TaskScheduler{}).InitializeTaskSchedule(taskSchedule, referenceTime)
	if errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	if initializedTaskSchedule.NextRunAt == nil {
		return TaskSchedule{}, ErrScheduleNoFutureRun
	}
	return initializedTaskSchedule, nil
}

func normalizeScheduleCadenceFields(taskSchedule *TaskSchedule) {
	switch taskSchedule.Kind {
	case TaskScheduleKindOnce:
		taskSchedule.IntervalSecond = 0
		taskSchedule.CronExpression = ""
		taskSchedule.MaxRunCount = 0
	case TaskScheduleKindInterval:
		taskSchedule.CronExpression = ""
	case TaskScheduleKindCron:
		taskSchedule.IntervalSecond = 0
	}
}

func validateScheduleRepeatPolicy(taskSchedule TaskSchedule, repeatPolicy string) error {
	if taskSchedule.Kind != TaskScheduleKindInterval && taskSchedule.Kind != TaskScheduleKindCron {
		return nil
	}
	if taskSchedule.MaxRunCount > 0 || taskSchedule.ExpiresAt != nil {
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

func parseScheduleKind(value string) (TaskScheduleKind, error) {
	switch strings.TrimSpace(value) {
	case string(TaskScheduleKindOnce):
		return TaskScheduleKindOnce, nil
	case string(TaskScheduleKindInterval):
		return TaskScheduleKindInterval, nil
	case string(TaskScheduleKindCron):
		return TaskScheduleKindCron, nil
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
