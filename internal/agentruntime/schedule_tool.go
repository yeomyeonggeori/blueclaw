package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type scheduleCreateToolInput struct {
	Name             string `json:"name"`
	TaskInstruction  string `json:"taskInstruction"`
	AgentProfileName string `json:"agentProfileName"`
	Kind             string `json:"kind"`
	RunAt            string `json:"runAt"`
	ExpiresAt        string `json:"expiresAt"`
	IntervalSecond   int    `json:"intervalSecond"`
	CronExpression   string `json:"cronExpression"`
	TimeZone         string `json:"timeZone"`
	MaxRunCount      int    `json:"maxRunCount"`
	RepeatPolicy     string `json:"repeatPolicy"`
}

type scheduleCancelToolInput struct {
	Scope       string   `json:"scope"`
	ScheduleIDs []string `json:"scheduleIDs"`
}

type scheduleListToolInput struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

type scheduleUpdateToolInput struct {
	ScheduleID       string  `json:"scheduleID"`
	Name             *string `json:"name"`
	TaskInstruction  *string `json:"taskInstruction"`
	AgentProfileName *string `json:"agentProfileName"`
	Kind             *string `json:"kind"`
	RunAt            *string `json:"runAt"`
	ExpiresAt        *string `json:"expiresAt"`
	IntervalSecond   *int    `json:"intervalSecond"`
	CronExpression   *string `json:"cronExpression"`
	TimeZone         *string `json:"timeZone"`
	MaxRunCount      *int    `json:"maxRunCount"`
	RepeatPolicy     *string `json:"repeatPolicy"`
}

type scheduleCancelOperationResult struct {
	CancelledScheduleIDs       []string `json:"cancelledScheduleIDs"`
	CancelledScheduleCount     int      `json:"cancelledScheduleCount"`
	CancelledTaskRunCount      int      `json:"cancelledTaskRunCount"`
	CancelledWaitCount         int      `json:"cancelledWaitCount"`
	EffectiveCancellationCount int      `json:"effectiveCancellationCount"`
	Cancelled                  bool     `json:"cancelled"`
}

type scheduleCreateToolResult struct {
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

type scheduleListToolOutput struct {
	Schedules []scheduleListToolItem `json:"schedules"`
}

type scheduleListToolItem struct {
	ScheduleID      string     `json:"scheduleID"`
	TaskInstruction string     `json:"taskInstruction"`
	Description     string     `json:"description,omitempty"`
	Cadence         string     `json:"cadence"`
	CronExpression  string     `json:"cronExpression,omitempty"`
	RunAt           *time.Time `json:"runAt,omitempty"`
	Status          string     `json:"status"`
	NextRunAt       *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt       *time.Time `json:"lastRunAt,omitempty"`
}

var (
	errScheduleCancelScopeInvalid = errors.New("schedule cancellation scope is invalid")
	errScheduleCancelIDsRequired  = errors.New("scheduleIDs are required for scheduleIDs scope")
	errScheduleCancelIDsInvalid   = errors.New("scheduleIDs must be exact nonblank identifiers")
	errScheduleKindInvalid        = errors.New("schedule kind is invalid")
	errScheduleIDRequired         = errors.New("scheduleID must be an exact nonblank identifier")
	errScheduleUpdateRequired     = errors.New("schedule_update requires at least one field to change")
)

func validateScheduleID(value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return errScheduleIDRequired
	}
	return nil
}

func validateScheduleCancelIDs(scope task.TaskScheduleCancelScope, scheduleIDs []string) error {
	if scope != task.TaskScheduleCancelScopeScheduleIDs && len(scheduleIDs) > 0 {
		return errScheduleCancelIDsInvalid
	}
	if scope == task.TaskScheduleCancelScopeScheduleIDs && len(scheduleIDs) == 0 {
		return errScheduleCancelIDsRequired
	}
	seenScheduleIDs := map[string]bool{}
	for _, scheduleID := range scheduleIDs {
		if validateScheduleID(scheduleID) != nil || seenScheduleIDs[scheduleID] {
			return errScheduleCancelIDsInvalid
		}
		seenScheduleIDs[scheduleID] = true
	}
	return nil
}

func validateScheduleUpdate(input scheduleUpdateToolInput) error {
	if input.Name != nil || input.TaskInstruction != nil || input.AgentProfileName != nil ||
		input.Kind != nil || input.RunAt != nil || input.ExpiresAt != nil ||
		input.IntervalSecond != nil || input.CronExpression != nil ||
		input.TimeZone != nil || input.MaxRunCount != nil || input.RepeatPolicy != nil {
		return nil
	}
	return errScheduleUpdateRequired
}

func taskScheduleIDs(taskSchedules []task.TaskSchedule) []string {
	scheduleIDs := make([]string, 0, len(taskSchedules))
	for _, taskSchedule := range taskSchedules {
		scheduleIDs = append(scheduleIDs, taskSchedule.TaskScheduleID)
	}
	return scheduleIDs
}

func scheduleListToolResult(output scheduleListToolOutput) toolcontract.ToolResult {
	document := json.RawMessage(marshalToolResult(output))
	return toolcontract.ToolSuccessData(string(document), document)
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerScheduleTools(toolRegistry *toolcontract.ToolSet, handlerContext toolHandlerContext) {
	if toolCatalogBuilder.taskScheduleRepository != nil && strings.TrimSpace(handlerContext.request.RequesterPersonID) != "" {
		toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[scheduleListToolInput, scheduleListToolOutput]{
			Definition: toolcontract.ToolDefinition{
				Name:        "schedule_list",
				Description: "List active scheduled tasks created by the current requester. Use it to answer what reminders or recurring tasks are currently scheduled.",
				InputSchema: scheduleListInputSchema,
			},
			Handler: func(toolContext context.Context, input scheduleListToolInput) (scheduleListToolOutput, error) {
				return toolCatalogBuilder.listScheduleTool(input, handlerContext)
			},
			Result: scheduleListToolResult,
		})
	}
	if !handlerContext.request.IsScheduledRun {
		toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[scheduleCreateToolInput, toolcontract.ToolResult]{
			Definition: toolcontract.ToolDefinition{
				Name:        "schedule_create",
				Description: "Create a scheduled task for the current requester and reply target. Put only the work to perform at run time in taskInstruction. Do not copy the original scheduling request into taskInstruction. Cadence and stop conditions must be represented only by kind, runAt, intervalSecond, cronExpression, expiresAt, and maxRunCount. For interval or cron schedules, set repeatPolicy to finite when the user gave an end condition and include expiresAt or maxRunCount; set repeatPolicy to unbounded only when the user explicitly wants no end.",
				InputSchema: scheduleCreateInputSchema,
			},
			Handler: func(toolContext context.Context, input scheduleCreateToolInput) (toolcontract.ToolResult, error) {
				return toolCatalogBuilder.createScheduleTool(toolContext, input, handlerContext)
			},
			Result: toolcontract.IdentityToolResult,
		})
		toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[scheduleUpdateToolInput, toolcontract.ToolResult]{
			Definition: toolcontract.ToolDefinition{
				Name:        "schedule_update",
				Description: "Update an active scheduled task created by the current requester. Provide scheduleID and only the scalar fields that should change. Keep only the work to perform at run time in taskInstruction; represent cadence and stop conditions only with kind, runAt, intervalSecond, cronExpression, expiresAt, maxRunCount, and repeatPolicy.",
				InputSchema: scheduleUpdateInputSchema,
			},
			Handler: func(toolContext context.Context, input scheduleUpdateToolInput) (toolcontract.ToolResult, error) {
				return toolCatalogBuilder.updateScheduleTool(toolContext, input, handlerContext)
			},
			Result: toolcontract.IdentityToolResult,
		})
	}
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[scheduleCancelToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "schedule_cancel",
			Description: "Cancel active scheduled tasks and pending approval or user-input waits. Use scope mine for schedules created by the current requester. Use scope currentConversation when the user wants messages or reminders delivered to this conversation to stop, even if another person created that delivery schedule. Use scope scheduleIDs for explicit schedule IDs visible from prior tool results. Cancellation expires records instead of deleting audit history.",
			InputSchema: scheduleCancelInputSchema,
		},
		Handler: func(toolContext context.Context, input scheduleCancelToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.cancelScheduleTool(toolContext, input, handlerContext)
		},
		Result: toolcontract.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) listScheduleTool(input scheduleListToolInput, handlerContext toolHandlerContext) (scheduleListToolOutput, error) {
	limit := normalizedScheduleListLimit(input.Limit)
	referenceTime := time.Now().UTC()
	result, errorValue := toolCatalogBuilder.taskScheduleRepository.ListTaskSchedules(task.TaskScheduleListRequest{
		CreatorPersonID: strings.TrimSpace(handlerContext.request.RequesterPersonID),
		Page:            1,
		PageSize:        20,
		ReferenceTime:   referenceTime,
	})
	if errorValue != nil {
		return scheduleListToolOutput{}, errorValue
	}
	return scheduleListToolOutput{
		Schedules: filteredScheduleListItems(result.TaskSchedules, input.Status, limit, referenceTime),
	}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) createScheduleTool(toolContext context.Context, input scheduleCreateToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	if toolCatalogBuilder.taskScheduleRepository == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, "schedule_create", "task schedule repository is unavailable"), nil
	}
	taskSchedule, errorValue := toolCatalogBuilder.buildTaskSchedule(input, handlerContext)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "schedule_create", errorValue.Error()), nil
	}
	initializedTaskSchedule, errorValue := (task.TaskScheduler{}).InitializeTaskSchedule(taskSchedule, time.Now().UTC())
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "schedule_create", "invalid task schedule"), nil
	}
	if initializedTaskSchedule.NextRunAt == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "schedule_create", "task schedule has no future run"), nil
	}
	if errorValue := toolCatalogBuilder.taskScheduleRepository.UpsertTaskSchedule(initializedTaskSchedule); errorValue != nil {
		return toolcontract.ToolResult{}, errorValue
	}
	resultDocument := scheduleCreateResultDocument(initializedTaskSchedule)
	if taskRunID := toolcontract.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "schedule.created", string(resultDocument))
	}
	return toolcontract.ToolSuccessData(string(resultDocument), resultDocument), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) updateScheduleTool(toolContext context.Context, input scheduleUpdateToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	if toolCatalogBuilder.taskScheduleRepository == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, "schedule_update", "task schedule repository is unavailable"), nil
	}
	if errorValue := validateScheduleID(input.ScheduleID); errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "schedule_update", errorValue.Error()), nil
	}
	if errorValue := validateScheduleUpdate(input); errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "schedule_update", errorValue.Error()), nil
	}
	updateRequest := task.TaskScheduleUpdateRequest{
		TaskScheduleID:    input.ScheduleID,
		RequesterPersonID: strings.TrimSpace(handlerContext.request.RequesterPersonID),
		UpdateTaskSchedule: func(existingTaskSchedule task.TaskSchedule) (task.TaskSchedule, error) {
			return toolCatalogBuilder.buildUpdatedTaskSchedule(existingTaskSchedule, input)
		},
	}
	result, errorValue := toolCatalogBuilder.taskScheduleRepository.UpdateTaskSchedule(updateRequest)
	if errorValue != nil {
		if isScheduleToolValidationError(errorValue) {
			return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "schedule_update", errorValue.Error()), nil
		}
		return toolcontract.ToolResult{}, errorValue
	}
	if !result.IsFound {
		return toolcontract.ToolFailureResult(toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound, "schedule_update", "active schedule was not found for the current requester"), nil
	}
	resultDocument := scheduleCreateResultDocument(result.TaskSchedule)
	if taskRunID := toolcontract.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "schedule.updated", string(resultDocument))
	}
	return toolcontract.ToolSuccessData(string(resultDocument), resultDocument), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) cancelScheduleTool(toolContext context.Context, input scheduleCancelToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	if toolCatalogBuilder.taskScheduleRepository == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, "schedule_cancel", "task schedule repository is unavailable"), nil
	}
	scope, errorValue := parseScheduleCancelScope(input.Scope)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "schedule_cancel", errorValue.Error()), nil
	}
	if errorValue := validateScheduleCancelIDs(scope, input.ScheduleIDs); errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "schedule_cancel", errorValue.Error()), nil
	}
	cancelledAt := time.Now().UTC()
	cancelRequest := task.TaskScheduleCancelRequest{
		Scope:             scope,
		RequesterPersonID: strings.TrimSpace(handlerContext.request.RequesterPersonID),
		ConversationID:    strings.TrimSpace(handlerContext.request.ConversationID),
		TaskScheduleIDs:   append([]string{}, input.ScheduleIDs...),
		CancelledAt:       cancelledAt,
	}
	result, errorValue := toolCatalogBuilder.cancelMatchingSchedules(cancelRequest, cancelledAt)
	if errorValue != nil {
		return toolcontract.ToolResult{}, errorValue
	}
	resultDocument := json.RawMessage(marshalToolResult(result))
	if result.EffectiveCancellationCount == 0 {
		return toolcontract.ToolFailureData(toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound, "schedule_cancel", "no active schedules or pending scheduled work matched the cancellation request", resultDocument), nil
	}
	if taskRunID := toolcontract.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, "schedule.cancelled", string(resultDocument))
	}
	return toolcontract.ToolSuccessData(string(resultDocument), resultDocument), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) cancelMatchingSchedules(cancelRequest task.TaskScheduleCancelRequest, cancelledAt time.Time) (scheduleCancelOperationResult, error) {
	result, errorValue := toolCatalogBuilder.taskScheduleRepository.CancelTaskSchedules(cancelRequest)
	if errorValue != nil {
		return scheduleCancelOperationResult{}, errorValue
	}
	cancelledTaskRunCount := toolCatalogBuilder.cancelScheduledTaskRuns(cancelRequest, result)
	cancelledWaitCount := toolCatalogBuilder.cancelPendingWaits(cancelRequest, cancelledAt)
	effectiveCancellationCount := len(result.TaskSchedules) + cancelledTaskRunCount + cancelledWaitCount
	return scheduleCancelOperationResult{
		CancelledScheduleIDs:       taskScheduleIDs(result.TaskSchedules),
		CancelledScheduleCount:     len(result.TaskSchedules),
		CancelledTaskRunCount:      cancelledTaskRunCount,
		CancelledWaitCount:         cancelledWaitCount,
		EffectiveCancellationCount: effectiveCancellationCount,
		Cancelled:                  effectiveCancellationCount > 0,
	}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) cancelScheduledTaskRuns(cancelRequest task.TaskScheduleCancelRequest, result task.TaskScheduleCancelResult) int {
	if toolCatalogBuilder.taskRunService == nil {
		return 0
	}
	taskRunCancelRequest := task.TaskRunCancelRequest{
		ScheduleOnly: true,
		Reason:       "schedule_cancel",
	}
	if cancelRequest.Scope == task.TaskScheduleCancelScopeMine {
		taskRunCancelRequest.RequesterPersonID = strings.TrimSpace(cancelRequest.RequesterPersonID)
		taskRunCancelRequest.OriginConversationIDPrefix = "schedule:"
	} else {
		taskRunCancelRequest.OriginConversationIDs = scheduleOriginConversationIDs(result.TaskSchedules)
	}
	return len(toolCatalogBuilder.taskRunService.CancelActiveTaskRuns(taskRunCancelRequest))
}

func (toolCatalogBuilder *ToolCatalogBuilder) cancelPendingWaits(cancelRequest task.TaskScheduleCancelRequest, cancelledAt time.Time) int {
	if toolCatalogBuilder.taskRunService == nil {
		return 0
	}
	if toolCatalogBuilder.taskWaitTokenRepository != nil && cancelRequest.Scope == task.TaskScheduleCancelScopeMine {
		_, _ = toolCatalogBuilder.taskWaitTokenRepository.ExpireTaskWaitTokensForPerson(cancelRequest.RequesterPersonID, cancelledAt)
	}
	originConversationID := ""
	if cancelRequest.Scope == task.TaskScheduleCancelScopeCurrentConversation {
		originConversationID = cancelRequest.ConversationID
	}
	cancelledTaskRuns := toolCatalogBuilder.taskRunService.CancelWaitingTaskRuns(cancelRequest.RequesterPersonID, originConversationID, "schedule_cancel")
	return len(cancelledTaskRuns)
}

func scheduleOriginConversationIDs(taskSchedules []task.TaskSchedule) []string {
	originConversationIDs := []string{}
	for _, taskSchedule := range taskSchedules {
		if strings.TrimSpace(taskSchedule.TaskScheduleID) == "" {
			continue
		}
		originConversationIDs = append(originConversationIDs, "schedule:"+taskSchedule.TaskScheduleID)
	}
	return originConversationIDs
}

func (toolCatalogBuilder *ToolCatalogBuilder) buildTaskSchedule(input scheduleCreateToolInput, handlerContext toolHandlerContext) (task.TaskSchedule, error) {
	if errorValue := validateScheduleCreateContext(handlerContext.request); errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	taskInstruction := strings.TrimSpace(input.TaskInstruction)
	if taskInstruction == "" {
		return task.TaskSchedule{}, errScheduleTaskInstructionRequired
	}
	kind, errorValue := parseTaskScheduleKind(input.Kind)
	if errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	timeZone, errorValue := normalizeScheduleTimeZone(input.TimeZone, toolCatalogBuilder.companyTimeZone())
	if errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	now := time.Now().UTC()
	taskSchedule := task.TaskSchedule{
		TaskScheduleID:   task.NewIdentifier(),
		CreatorPersonID:  strings.TrimSpace(handlerContext.request.RequesterPersonID),
		Name:             firstNonEmptyString(input.Name, taskInstruction),
		Prompt:           taskInstruction,
		ExecutionMode:    task.TaskScheduleExecutionModeAgent,
		AgentProfileName: firstNonEmptyString(input.AgentProfileName, handlerContext.request.ProfileName, "default"),
		Platform:         strings.TrimSpace(handlerContext.request.Platform),
		ConversationID:   strings.TrimSpace(handlerContext.request.ConversationID),
		ReplyTargetID:    strings.TrimSpace(handlerContext.request.ReplyTargetID),
		TimeZone:         timeZone,
		Kind:             kind,
		IntervalSecond:   input.IntervalSecond,
		CronExpression:   strings.TrimSpace(input.CronExpression),
		MaxRunCount:      input.MaxRunCount,
		CreatedAt:        now,
		UpdatedAt:        now,
		NextAttemptAt:    &now,
	}
	if errorValue := applyScheduleRunAt(&taskSchedule, input.RunAt); errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	if errorValue := applyScheduleExpiresAt(&taskSchedule, input.ExpiresAt, now); errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	if errorValue := validateScheduleRepeatPolicy(input, taskSchedule); errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	return taskSchedule, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) buildUpdatedTaskSchedule(taskSchedule task.TaskSchedule, input scheduleUpdateToolInput) (task.TaskSchedule, error) {
	now := time.Now().UTC()
	if input.Name != nil {
		taskSchedule.Name = strings.TrimSpace(*input.Name)
	}
	if taskInstruction := scheduleUpdateTaskInstruction(input); taskInstruction != nil {
		if strings.TrimSpace(*taskInstruction) == "" {
			return task.TaskSchedule{}, errScheduleTaskInstructionRequired
		}
		taskSchedule.Prompt = strings.TrimSpace(*taskInstruction)
	}
	if input.AgentProfileName != nil {
		taskSchedule.AgentProfileName = strings.TrimSpace(*input.AgentProfileName)
	}
	if input.TimeZone != nil {
		timeZone, errorValue := normalizeScheduleTimeZone(*input.TimeZone, toolCatalogBuilder.companyTimeZone())
		if errorValue != nil {
			return task.TaskSchedule{}, errorValue
		}
		taskSchedule.TimeZone = timeZone
	}
	updatedTaskSchedule, errorValue := applyScheduleUpdateTiming(taskSchedule, input, now)
	if errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	updatedTaskSchedule.UpdatedAt = now
	updatedTaskSchedule.NextAttemptAt = &now
	initializedTaskSchedule, errorValue := (task.TaskScheduler{}).InitializeTaskSchedule(updatedTaskSchedule, now)
	if errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	if initializedTaskSchedule.NextRunAt == nil {
		return task.TaskSchedule{}, errScheduleNoFutureRun
	}
	return initializedTaskSchedule, nil
}

func normalizedScheduleListLimit(limit int) int {
	if limit <= 0 {
		return 10
	}
	if limit > 20 {
		return 20
	}
	return limit
}

func filteredScheduleListItems(taskSchedules []task.TaskSchedule, statusFilter string, limit int, referenceTime time.Time) []scheduleListToolItem {
	filter := strings.TrimSpace(statusFilter)
	items := []scheduleListToolItem{}
	for _, taskSchedule := range taskSchedules {
		item := scheduleListToolItemFromSchedule(taskSchedule, referenceTime)
		if filter != "" && item.Status != filter {
			continue
		}
		items = append(items, item)
		if len(items) == limit {
			break
		}
	}
	return items
}

func scheduleListToolItemFromSchedule(taskSchedule task.TaskSchedule, referenceTime time.Time) scheduleListToolItem {
	return scheduleListToolItem{
		ScheduleID:      taskSchedule.TaskScheduleID,
		TaskInstruction: taskSchedule.Prompt,
		Description:     taskSchedule.Name,
		Cadence:         taskScheduleCadence(taskSchedule),
		CronExpression:  taskSchedule.CronExpression,
		RunAt:           taskSchedule.RunAt,
		Status:          taskScheduleStatus(taskSchedule, referenceTime),
		NextRunAt:       taskSchedule.NextRunAt,
		LastRunAt:       taskSchedule.LastRunAt,
	}
}

func taskScheduleCadence(taskSchedule task.TaskSchedule) string {
	switch taskSchedule.Kind {
	case task.TaskScheduleKindInterval:
		return "every " + strconv.Itoa(taskSchedule.IntervalSecond) + " seconds"
	case task.TaskScheduleKindCron:
		return "cron"
	default:
		return "once"
	}
}

func taskScheduleStatus(taskSchedule task.TaskSchedule, referenceTime time.Time) string {
	if taskSchedule.NextRunAt == nil || taskSchedule.ExpiresAt != nil && !taskSchedule.ExpiresAt.After(referenceTime) {
		return "expired"
	}
	if strings.TrimSpace(taskSchedule.LastError) != "" {
		return "failed"
	}
	return "active"
}

func scheduleCreateResultDocument(taskSchedule task.TaskSchedule) json.RawMessage {
	return json.RawMessage(marshalToolResult(scheduleCreateToolResult{
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
	}))
}

func parseScheduleCancelScope(value string) (task.TaskScheduleCancelScope, error) {
	switch strings.TrimSpace(value) {
	case string(task.TaskScheduleCancelScopeCurrentConversation):
		return task.TaskScheduleCancelScopeCurrentConversation, nil
	case string(task.TaskScheduleCancelScopeMine):
		return task.TaskScheduleCancelScopeMine, nil
	case string(task.TaskScheduleCancelScopeScheduleIDs):
		return task.TaskScheduleCancelScopeScheduleIDs, nil
	default:
		return "", errScheduleCancelScopeInvalid
	}
}

func applyScheduleUpdateTiming(taskSchedule task.TaskSchedule, input scheduleUpdateToolInput, now time.Time) (task.TaskSchedule, error) {
	if input.Kind != nil {
		kind, errorValue := parseTaskScheduleKind(*input.Kind)
		if errorValue != nil {
			return task.TaskSchedule{}, errorValue
		}
		taskSchedule.Kind = kind
	}
	if input.RunAt != nil {
		if errorValue := applyScheduleRunAtPointer(&taskSchedule, *input.RunAt); errorValue != nil {
			return task.TaskSchedule{}, errorValue
		}
	}
	if input.ExpiresAt != nil {
		if errorValue := applyScheduleExpiresAtPointer(&taskSchedule, *input.ExpiresAt, now); errorValue != nil {
			return task.TaskSchedule{}, errorValue
		}
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
	normalizeUpdatedTaskScheduleKindFields(&taskSchedule)
	if errorValue := validateScheduleRepeatPolicy(scheduleUpdateAsCreateInput(input), taskSchedule); errorValue != nil {
		return task.TaskSchedule{}, errorValue
	}
	return taskSchedule, nil
}

func normalizeUpdatedTaskScheduleKindFields(taskSchedule *task.TaskSchedule) {
	switch taskSchedule.Kind {
	case task.TaskScheduleKindOnce:
		taskSchedule.IntervalSecond = 0
		taskSchedule.CronExpression = ""
		taskSchedule.MaxRunCount = 0
	case task.TaskScheduleKindInterval:
		taskSchedule.CronExpression = ""
	case task.TaskScheduleKindCron:
		taskSchedule.IntervalSecond = 0
	}
}

func scheduleUpdateTaskInstruction(input scheduleUpdateToolInput) *string {
	return input.TaskInstruction
}

func scheduleUpdateAsCreateInput(input scheduleUpdateToolInput) scheduleCreateToolInput {
	createInput := scheduleCreateToolInput{}
	if input.RepeatPolicy != nil {
		createInput.RepeatPolicy = *input.RepeatPolicy
	}
	return createInput
}

func applyScheduleExpiresAt(taskSchedule *task.TaskSchedule, value string, referenceTime time.Time) error {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil
	}
	expiresAt, errorValue := time.Parse(time.RFC3339, trimmedValue)
	if errorValue != nil {
		return errScheduleInvalidExpiresAt
	}
	expiresAt = expiresAt.UTC()
	if !expiresAt.After(referenceTime) {
		return errScheduleInvalidExpiresAt
	}
	taskSchedule.ExpiresAt = &expiresAt
	return nil
}

func applyScheduleExpiresAtPointer(taskSchedule *task.TaskSchedule, value string, referenceTime time.Time) error {
	if strings.TrimSpace(value) == "" {
		taskSchedule.ExpiresAt = nil
		return nil
	}
	return applyScheduleExpiresAt(taskSchedule, value, referenceTime)
}

func validateScheduleRepeatPolicy(input scheduleCreateToolInput, taskSchedule task.TaskSchedule) error {
	if taskSchedule.Kind != task.TaskScheduleKindInterval && taskSchedule.Kind != task.TaskScheduleKindCron {
		return nil
	}
	if taskSchedule.MaxRunCount > 0 || taskSchedule.ExpiresAt != nil {
		return nil
	}
	switch strings.TrimSpace(input.RepeatPolicy) {
	case "unbounded":
		return nil
	case "finite":
		return errScheduleFiniteBoundRequired
	default:
		return errScheduleRepeatPolicyRequired
	}
}

func validateScheduleCreateContext(request ToolCatalogRequest) error {
	if request.IsScheduledRun {
		return errScheduleCreateInScheduledRun
	}
	if strings.TrimSpace(request.RequesterPersonID) == "" {
		return errScheduleRequesterRequired
	}
	if strings.TrimSpace(request.Platform) == "" || strings.TrimSpace(request.ConversationID) == "" {
		return errScheduleConversationRequired
	}
	if strings.TrimSpace(request.ReplyTargetID) == "" {
		return errScheduleReplyTargetRequired
	}
	return nil
}

func parseTaskScheduleKind(value string) (task.TaskScheduleKind, error) {
	switch value {
	case string(task.TaskScheduleKindOnce):
		return task.TaskScheduleKindOnce, nil
	case string(task.TaskScheduleKindInterval):
		return task.TaskScheduleKindInterval, nil
	case string(task.TaskScheduleKindCron):
		return task.TaskScheduleKindCron, nil
	default:
		return "", errScheduleKindInvalid
	}
}

func normalizeScheduleTimeZone(value string, companyTimeZone string) (string, error) {
	timeZone := task.ScheduleTimeZoneName(firstNonEmptyString(value, companyTimeZone))
	if _, errorValue := time.LoadLocation(timeZone); errorValue != nil {
		return "", errScheduleTimeZoneInvalid
	}
	return timeZone, nil
}

func applyScheduleRunAt(taskSchedule *task.TaskSchedule, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	runAt, errorValue := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if errorValue != nil {
		return errScheduleRunAtInvalid
	}
	taskSchedule.RunAt = &runAt
	return nil
}

func applyScheduleRunAtPointer(taskSchedule *task.TaskSchedule, value string) error {
	if strings.TrimSpace(value) == "" {
		taskSchedule.RunAt = nil
		return nil
	}
	return applyScheduleRunAt(taskSchedule, value)
}

func isScheduleToolValidationError(errorValue error) bool {
	return errors.Is(errorValue, errScheduleCancelScopeInvalid) ||
		errors.Is(errorValue, errScheduleCancelIDsRequired) ||
		errors.Is(errorValue, errScheduleCancelIDsInvalid) ||
		errors.Is(errorValue, errScheduleKindInvalid) ||
		errors.Is(errorValue, errScheduleIDRequired) ||
		errors.Is(errorValue, errScheduleUpdateRequired) ||
		errors.Is(errorValue, errScheduleTaskInstructionRequired) ||
		errors.Is(errorValue, errScheduleTimeZoneInvalid) ||
		errors.Is(errorValue, errScheduleRunAtInvalid) ||
		errors.Is(errorValue, errScheduleInvalidExpiresAt) ||
		errors.Is(errorValue, errScheduleRepeatPolicyRequired) ||
		errors.Is(errorValue, errScheduleFiniteBoundRequired) ||
		errors.Is(errorValue, errScheduleNoFutureRun)
}
