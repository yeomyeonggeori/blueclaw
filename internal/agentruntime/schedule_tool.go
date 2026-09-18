package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
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

type scheduleListToolInput = task.ScheduleListInput

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

type scheduleListToolOutput = task.ScheduleListOutput

var (
	errScheduleCancelScopeInvalid = errors.New("schedule cancellation scope is invalid")
	errScheduleCancelIDsRequired  = errors.New("scheduleIDs are required for scheduleIDs scope")
	errScheduleCancelIDsInvalid   = errors.New("scheduleIDs must be exact nonblank identifiers")
	errScheduleIDRequired         = errors.New("scheduleID must be an exact nonblank identifier")
	errScheduleUpdateRequired     = errors.New("schedule_update requires at least one field to change")
)

func validateScheduleID(value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return errScheduleIDRequired
	}
	return nil
}

func validateScheduleCancelIDs(scope task.ScheduleCancelScope, scheduleIDs []string) error {
	if scope != task.ScheduleCancelScopeScheduleIDs && len(scheduleIDs) > 0 {
		return errScheduleCancelIDsInvalid
	}
	if scope == task.ScheduleCancelScopeScheduleIDs && len(scheduleIDs) == 0 {
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
	if task.ScheduleUpdateChangesNothing(scheduleUpdateInputOf(input)) {
		return errScheduleUpdateRequired
	}
	return nil
}

func scheduleUpdateInputOf(input scheduleUpdateToolInput) task.ScheduleUpdateInput {
	return task.ScheduleUpdateInput{
		Description:      input.Name,
		TaskInstruction:  input.TaskInstruction,
		AgentProfileName: input.AgentProfileName,
		Kind:             input.Kind,
		RunAt:            input.RunAt,
		ExpiresAt:        input.ExpiresAt,
		IntervalSecond:   input.IntervalSecond,
		CronExpression:   input.CronExpression,
		TimeZone:         input.TimeZone,
		MaxRunCount:      input.MaxRunCount,
		RepeatPolicy:     input.RepeatPolicy,
	}
}

func scheduleIDsOf(schedules []task.Schedule) []string {
	scheduleIDs := make([]string, 0, len(schedules))
	for _, schedule := range schedules {
		scheduleIDs = append(scheduleIDs, schedule.ScheduleID)
	}
	return scheduleIDs
}

func scheduleListToolResult(output scheduleListToolOutput) toolcontract.ToolResult {
	document := json.RawMessage(MarshalBody(output))
	return toolcontract.ToolSuccessData(string(document), document)
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerScheduleTools(toolRegistry *toolcontract.ToolSet, handlerContext toolHandlerContext) {
	if toolCatalogBuilder.scheduleRepository != nil && strings.TrimSpace(handlerContext.request.RequesterPersonID) != "" {
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
	referenceTime := time.Now().UTC()
	result, errorValue := toolCatalogBuilder.scheduleRepository.ListSchedules(
		task.ScheduleListQuery(handlerContext.request.RequesterPersonID, referenceTime))
	if errorValue != nil {
		return scheduleListToolOutput{}, errorValue
	}
	return task.ProjectScheduleList(result.Schedules, input, referenceTime), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) createScheduleTool(toolContext context.Context, input scheduleCreateToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	if toolCatalogBuilder.scheduleRepository == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, "schedule_create", "task schedule repository is unavailable"), nil
	}
	initializedSchedule, errorValue := toolCatalogBuilder.createSchedule(input, handlerContext, time.Now().UTC())
	if task.IsScheduleWriteInputError(errorValue) {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "schedule_create", errorValue.Error()), nil
	}
	if errorValue != nil {
		return toolcontract.ToolResult{}, errorValue
	}
	resultDocument := scheduleCreateResultDocument(initializedSchedule)
	if taskRunID := toolcontract.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventScheduleCreated, string(resultDocument))
	}
	return toolcontract.ToolSuccessData(string(resultDocument), resultDocument), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) updateScheduleTool(toolContext context.Context, input scheduleUpdateToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	if toolCatalogBuilder.scheduleRepository == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, "schedule_update", "task schedule repository is unavailable"), nil
	}
	if errorValue := validateScheduleID(input.ScheduleID); errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "schedule_update", errorValue.Error()), nil
	}
	if errorValue := validateScheduleUpdate(input); errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "schedule_update", errorValue.Error()), nil
	}
	updateRequest := task.ScheduleUpdateRequest{
		ScheduleID:        input.ScheduleID,
		RequesterPersonID: strings.TrimSpace(handlerContext.request.RequesterPersonID),
		UpdateSchedule: func(existingSchedule task.Schedule) (task.Schedule, error) {
			return toolCatalogBuilder.buildUpdatedSchedule(existingSchedule, input)
		},
	}
	result, errorValue := toolCatalogBuilder.scheduleRepository.UpdateSchedule(updateRequest)
	if errorValue != nil {
		if isScheduleToolValidationError(errorValue) {
			return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "schedule_update", errorValue.Error()), nil
		}
		return toolcontract.ToolResult{}, errorValue
	}
	if !result.IsFound {
		return toolcontract.ToolFailureResult(toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound, "schedule_update", "active schedule was not found for the current requester"), nil
	}
	resultDocument := scheduleCreateResultDocument(result.Schedule)
	if taskRunID := toolcontract.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventScheduleUpdated, string(resultDocument))
	}
	return toolcontract.ToolSuccessData(string(resultDocument), resultDocument), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) cancelScheduleTool(toolContext context.Context, input scheduleCancelToolInput, handlerContext toolHandlerContext) (toolcontract.ToolResult, error) {
	if toolCatalogBuilder.scheduleRepository == nil {
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
	cancelRequest := task.ScheduleCancelRequest{
		Scope:             scope,
		RequesterPersonID: strings.TrimSpace(handlerContext.request.RequesterPersonID),
		ConversationID:    strings.TrimSpace(handlerContext.request.ConversationID),
		ScheduleIDs:       append([]string{}, input.ScheduleIDs...),
		CancelledAt:       cancelledAt,
	}
	result, errorValue := toolCatalogBuilder.cancelMatchingSchedules(cancelRequest, cancelledAt)
	if errorValue != nil {
		return toolcontract.ToolResult{}, errorValue
	}
	resultDocument := json.RawMessage(MarshalBody(result))
	if result.EffectiveCancellationCount == 0 {
		return toolcontract.ToolFailureData(toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound, "schedule_cancel", "no active schedules or pending scheduled work matched the cancellation request", resultDocument), nil
	}
	if taskRunID := toolcontract.TaskRunIDFromContext(toolContext); taskRunID != "" && toolCatalogBuilder.taskRunService != nil {
		toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventScheduleCancelled, string(resultDocument))
	}
	return toolcontract.ToolSuccessData(string(resultDocument), resultDocument), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) cancelMatchingSchedules(cancelRequest task.ScheduleCancelRequest, cancelledAt time.Time) (scheduleCancelOperationResult, error) {
	result, errorValue := toolCatalogBuilder.scheduleRepository.CancelSchedules(cancelRequest)
	if errorValue != nil {
		return scheduleCancelOperationResult{}, errorValue
	}
	cancelledTaskRunCount := toolCatalogBuilder.cancelScheduledTaskRuns(cancelRequest, result)
	cancelledWaitCount := toolCatalogBuilder.cancelPendingWaits(cancelRequest, cancelledAt)
	effectiveCancellationCount := len(result.Schedules) + cancelledTaskRunCount + cancelledWaitCount
	return scheduleCancelOperationResult{
		CancelledScheduleIDs:       scheduleIDsOf(result.Schedules),
		CancelledScheduleCount:     len(result.Schedules),
		CancelledTaskRunCount:      cancelledTaskRunCount,
		CancelledWaitCount:         cancelledWaitCount,
		EffectiveCancellationCount: effectiveCancellationCount,
		Cancelled:                  effectiveCancellationCount > 0,
	}, nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) cancelScheduledTaskRuns(cancelRequest task.ScheduleCancelRequest, result task.ScheduleCancelResult) int {
	if toolCatalogBuilder.taskRunService == nil {
		return 0
	}
	originConversationIDs := scheduleOriginConversationIDs(result.Schedules)
	if len(originConversationIDs) == 0 {
		return 0
	}
	taskRunCancelRequest := task.TaskRunCancelRequest{
		OriginConversationIDs: originConversationIDs,
		Reason:                "schedule_cancel",
	}
	if cancelRequest.Scope == task.ScheduleCancelScopeMine {
		taskRunCancelRequest.RequesterPersonID = strings.TrimSpace(cancelRequest.RequesterPersonID)
	}
	return len(toolCatalogBuilder.taskRunService.CancelActiveTaskRuns(taskRunCancelRequest))
}

func (toolCatalogBuilder *ToolCatalogBuilder) cancelPendingWaits(cancelRequest task.ScheduleCancelRequest, cancelledAt time.Time) int {
	if toolCatalogBuilder.taskRunService == nil {
		return 0
	}
	if toolCatalogBuilder.taskWaitTokenRepository != nil && cancelRequest.Scope == task.ScheduleCancelScopeMine {
		_, _ = toolCatalogBuilder.taskWaitTokenRepository.ExpireTaskWaitTokensForPerson(cancelRequest.RequesterPersonID, cancelledAt)
	}
	originConversationID := ""
	if cancelRequest.Scope == task.ScheduleCancelScopeCurrentConversation {
		originConversationID = cancelRequest.ConversationID
	}
	cancelledTaskRuns := toolCatalogBuilder.taskRunService.CancelWaitingTaskRuns(cancelRequest.RequesterPersonID, originConversationID, "schedule_cancel")
	return len(cancelledTaskRuns)
}

func scheduleOriginConversationIDs(schedules []task.Schedule) []string {
	originConversationIDs := []string{}
	for _, schedule := range schedules {
		if strings.TrimSpace(schedule.ScheduleID) == "" {
			continue
		}
		originConversationIDs = append(originConversationIDs, task.ScheduleSessionID(schedule.ScheduleID))
	}
	return originConversationIDs
}

func (toolCatalogBuilder *ToolCatalogBuilder) scheduleCreateContext(handlerContext toolHandlerContext, input scheduleCreateToolInput, referenceTime time.Time) task.ScheduleCreateContext {
	return task.ScheduleCreateContext{
		CreatorPersonID:  handlerContext.request.RequesterPersonID,
		AgentProfileName: firstNonEmptyString(input.AgentProfileName, handlerContext.request.ProfileName),
		Delivery: task.ScheduleDeliveryBinding{
			Platform:       handlerContext.request.Platform,
			ConversationID: handlerContext.request.DeliveryConversationID,
			ReplyTargetID:  handlerContext.request.ReplyTargetID,
		},
		CompanyTimeZone: toolCatalogBuilder.companyTimeZone(),
		ReferenceTime:   referenceTime,
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) createSchedule(input scheduleCreateToolInput, handlerContext toolHandlerContext, referenceTime time.Time) (task.Schedule, error) {
	return task.CreateSchedule(toolCatalogBuilder.scheduleRepository, task.ScheduleCreateInput{
		Description:     input.Name,
		TaskInstruction: input.TaskInstruction,
		Kind:            input.Kind,
		RunAt:           input.RunAt,
		ExpiresAt:       input.ExpiresAt,
		IntervalSecond:  input.IntervalSecond,
		CronExpression:  input.CronExpression,
		TimeZone:        input.TimeZone,
		MaxRunCount:     input.MaxRunCount,
		RepeatPolicy:    input.RepeatPolicy,
	}, toolCatalogBuilder.scheduleCreateContext(handlerContext, input, referenceTime))
}

func (toolCatalogBuilder *ToolCatalogBuilder) buildUpdatedSchedule(schedule task.Schedule, input scheduleUpdateToolInput) (task.Schedule, error) {
	return task.ApplyScheduleUpdate(schedule, scheduleUpdateInputOf(input), toolCatalogBuilder.companyTimeZone(), time.Now().UTC())
}

type nativeScheduleMutationResult struct {
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

func scheduleCreateResultDocument(schedule task.Schedule) json.RawMessage {
	mutation := task.ProjectScheduleMutation(schedule)
	return json.RawMessage(MarshalBody(nativeScheduleMutationResult{
		ScheduleID:       mutation.ScheduleID,
		Name:             mutation.Description,
		TaskInstruction:  mutation.TaskInstruction,
		TimeZone:         mutation.TimeZone,
		Kind:             mutation.Kind,
		RunAt:            mutation.RunAt,
		IntervalSecond:   mutation.IntervalSecond,
		CronExpression:   mutation.CronExpression,
		MaxRunCount:      mutation.MaxRunCount,
		ExpiresAt:        mutation.ExpiresAt,
		NextRunAt:        mutation.NextRunAt,
		ConversationID:   mutation.ConversationID,
		ReplyTargetID:    mutation.ReplyTargetID,
		AgentProfileName: mutation.AgentProfileName,
	}))
}

func parseScheduleCancelScope(value string) (task.ScheduleCancelScope, error) {
	switch strings.TrimSpace(value) {
	case string(task.ScheduleCancelScopeCurrentConversation):
		return task.ScheduleCancelScopeCurrentConversation, nil
	case string(task.ScheduleCancelScopeMine):
		return task.ScheduleCancelScopeMine, nil
	case string(task.ScheduleCancelScopeScheduleIDs):
		return task.ScheduleCancelScopeScheduleIDs, nil
	default:
		return "", errScheduleCancelScopeInvalid
	}
}

func isScheduleToolValidationError(errorValue error) bool {
	return task.IsScheduleWriteInputError(errorValue) ||
		errors.Is(errorValue, errScheduleCancelScopeInvalid) ||
		errors.Is(errorValue, errScheduleCancelIDsRequired) ||
		errors.Is(errorValue, errScheduleCancelIDsInvalid) ||
		errors.Is(errorValue, errScheduleIDRequired) ||
		errors.Is(errorValue, errScheduleUpdateRequired)
}
