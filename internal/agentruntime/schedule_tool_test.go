package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestScheduleCreateToolStoresCurrentReplyTarget(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_create",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"name":            "daily research",
			"taskInstruction": "research the important industry news and tell me.",
			"kind":            "cron",
			"cronExpression":  "0 7 * * *",
			"repeatPolicy":    "unbounded",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule_create success, got %s", result.ContentText())
	}
	if len(result.Effects) != 1 || result.Effects[0].ObjectType != "schedule" || result.Effects[0].Effect != "created" || result.Effects[0].ID == "" {
		t.Fatalf("expected exact schedule create effect, got %+v", result.Effects)
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one schedule, got %+v", repository.taskSchedules)
	}
	taskSchedule := repository.taskSchedules[0]
	if taskSchedule.Platform != "mattermost" || taskSchedule.ConversationID != "channel-1" || taskSchedule.ReplyTargetID != "reply-target-1" {
		t.Fatalf("expected current reply target to be stored, got %+v", taskSchedule)
	}
	if taskSchedule.Prompt != "research the important industry news and tell me." {
		t.Fatalf("expected stored task instruction without cadence, got %q", taskSchedule.Prompt)
	}
	if taskSchedule.ExecutionMode != task.TaskScheduleExecutionModeAgent {
		t.Fatalf("expected agent execution mode, got %+v", taskSchedule)
	}
	if taskSchedule.TimeZone != "Asia/Seoul" || taskSchedule.NextRunAt == nil {
		t.Fatalf("expected default timezone and next run, got %+v", taskSchedule)
	}
}

func TestScheduleListToolListsRequesterSchedulesOnly(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{
		{TaskScheduleID: "schedule-a", CreatorPersonID: "person-a", Name: "A schedule", Prompt: "Check A", Kind: task.TaskScheduleKindOnce, RunAt: &nextRunAt, NextRunAt: &nextRunAt},
		{TaskScheduleID: "schedule-b", CreatorPersonID: "person-b", Name: "B schedule", Prompt: "Check B", Kind: task.TaskScheduleKindOnce, RunAt: &nextRunAt, NextRunAt: &nextRunAt},
	}}
	toolRegistry := newScheduleListTestRegistry(repository, "person-a")

	output := invokeScheduleList(t, toolRegistry, map[string]any{})

	if len(output.Schedules) != 1 || output.Schedules[0].ScheduleID != "schedule-a" {
		t.Fatalf("expected only requester schedules, got %+v", output.Schedules)
	}
	if output.Schedules[0].TaskInstruction != "Check A" || output.Schedules[0].Cadence != "once" || output.Schedules[0].Status != "active" {
		t.Fatalf("expected schedule details, got %+v", output.Schedules[0])
	}
}

func TestScheduleListToolFiltersByStatus(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{
		{TaskScheduleID: "schedule-active", CreatorPersonID: "person-1", Prompt: "Active", Kind: task.TaskScheduleKindInterval, IntervalSecond: 60, NextRunAt: &nextRunAt},
		{TaskScheduleID: "schedule-failed", CreatorPersonID: "person-1", Prompt: "Failed", Kind: task.TaskScheduleKindCron, CronExpression: "0 9 * * *", NextRunAt: &nextRunAt, LastError: "provider unavailable"},
	}}
	toolRegistry := newScheduleListTestRegistry(repository, "person-1")

	output := invokeScheduleList(t, toolRegistry, map[string]any{"status": "failed"})

	if len(output.Schedules) != 1 || output.Schedules[0].ScheduleID != "schedule-failed" {
		t.Fatalf("expected failed schedule only, got %+v", output.Schedules)
	}
	if output.Schedules[0].Cadence != "cron" || output.Schedules[0].CronExpression != "0 9 * * *" {
		t.Fatalf("expected cron details, got %+v", output.Schedules[0])
	}
}

func TestScheduleListToolCapsLimitAtTwenty(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	taskSchedules := []task.TaskSchedule{}
	for index := 0; index < 25; index++ {
		taskSchedules = append(taskSchedules, task.TaskSchedule{
			TaskScheduleID:  "schedule-" + strconv.Itoa(index),
			CreatorPersonID: "person-1",
			Prompt:          "Task",
			Kind:            task.TaskScheduleKindOnce,
			RunAt:           &nextRunAt,
			NextRunAt:       &nextRunAt,
		})
	}
	repository := &memoryTaskScheduleRepository{taskSchedules: taskSchedules}
	toolRegistry := newScheduleListTestRegistry(repository, "person-1")

	output := invokeScheduleList(t, toolRegistry, map[string]any{"limit": 99})

	if len(output.Schedules) != 20 {
		t.Fatalf("expected capped schedule list, got %d", len(output.Schedules))
	}
}

func TestScheduleListToolRequiresRequesterPersonID(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolRegistry := newScheduleListTestRegistry(repository, "")

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_list",
		Input:    toolcontract.MarshalToolInput(map[string]any{}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected schedule_list to be unregistered without requester, got %s", result.ContentText())
	}
}

func TestScheduleCreateSchemaUsesTaskInstruction(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	toolDefinition, isFound := findToolDefinition(toolRegistry.ListToolDefinitions(), "schedule_create")
	if !isFound {
		t.Fatal("expected schedule_create definition")
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if errorValue := json.Unmarshal(toolDefinition.InputSchema, &schema); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !containsString(schema.Required, "taskInstruction") {
		t.Fatalf("expected taskInstruction to be required, got %+v", schema.Required)
	}
	if !strings.Contains(string(toolDefinition.InputSchema), `"additionalProperties":false`) {
		t.Fatalf("expected schedule_create schema to reject unknown fields, got %s", toolDefinition.InputSchema)
	}
	for _, hiddenField := range []string{"prompt", "message", "schedule", "executionMode"} {
		if _, isFound := schema.Properties[hiddenField]; isFound {
			t.Fatalf("expected %s to stay out of schedule_create model-facing schema, got %+v", hiddenField, schema.Properties)
		}
	}
}

func TestScheduleCreateRejectsLegacyPromptAndUnknownFields(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_create",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"prompt":         "legacy prompt",
			"kind":           "interval",
			"intervalSecond": 60,
			"repeatPolicy":   "unbounded",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || len(repository.taskSchedules) != 0 {
		t.Fatalf("expected legacy prompt input to fail without mutation, result=%+v schedules=%+v", result, repository.taskSchedules)
	}
}

func TestScheduleCreateRejectsUnknownKindWithoutInference(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_create",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"taskInstruction": "inspect the status",
			"kind":            "unexpected",
			"cronExpression":  "0 9 * * *",
			"repeatPolicy":    "unbounded",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || len(repository.taskSchedules) != 0 {
		t.Fatalf("expected unknown kind to fail without inference, result=%+v schedules=%+v", result, repository.taskSchedules)
	}
}

func TestScheduleUpdateRejectsNonExactScheduleID(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:  "schedule-owned",
		CreatorPersonID: "person-1",
		Prompt:          "inspect the status",
		Kind:            task.TaskScheduleKindOnce,
		NextRunAt:       &nextRunAt,
		TimeZone:        "Asia/Seoul",
	}}}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_update"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", RequesterPersonID: "person-1"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_update",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"scheduleID": " schedule-owned ",
			"name":       "changed",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || repository.taskSchedules[0].Name == "changed" {
		t.Fatalf("expected non-exact schedule ID to fail without mutation, result=%+v schedule=%+v", result, repository.taskSchedules[0])
	}
}

func TestScheduleUpdateRequiresAChange(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:  "schedule-owned",
		CreatorPersonID: "person-1",
		Prompt:          "inspect the status",
		Kind:            task.TaskScheduleKindOnce,
		NextRunAt:       &nextRunAt,
		TimeZone:        "Asia/Seoul",
	}}}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_update"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", RequesterPersonID: "person-1"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_update",
		Input:    toolcontract.MarshalToolInput(map[string]any{"scheduleID": "schedule-owned"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || repository.taskSchedules[0].Prompt != "inspect the status" {
		t.Fatalf("expected empty update to fail without mutation, result=%+v schedule=%+v", result, repository.taskSchedules[0])
	}
}

func TestScheduleCancelRejectsUnknownScopeWithoutMutation(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_cancel"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", RequesterPersonID: "person-1"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_cancel",
		Input:    toolcontract.MarshalToolInput(map[string]any{"scope": "unknown"}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected unknown cancellation scope to fail, got %+v", result)
	}
}

func TestScheduleCreateToolStoresTaskInstructionAsAgentTask(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_create",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"taskInstruction": "say sorry.",
			"kind":            "interval",
			"intervalSecond":  60,
			"maxRunCount":     10,
			"repeatPolicy":    "finite",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule_create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one schedule, got %+v", repository.taskSchedules)
	}
	if repository.taskSchedules[0].ExecutionMode != task.TaskScheduleExecutionModeAgent {
		t.Fatalf("expected agent execution mode, got %+v", repository.taskSchedules[0])
	}
	if repository.taskSchedules[0].Prompt != "say sorry." {
		t.Fatalf("expected task instruction to be stored, got %+v", repository.taskSchedules[0])
	}
	if !strings.Contains(result.ContentText(), `"taskInstruction":"say sorry."`) || strings.Contains(result.ContentText(), `"prompt"`) {
		t.Fatalf("expected tool result to expose taskInstruction without prompt, got %s", result.ContentText())
	}
	if repository.taskSchedules[0].ExpiresAt != nil {
		t.Fatalf("expected schedule expiration to default to nil, got %+v", repository.taskSchedules[0])
	}
}

func TestScheduleCreateToolRejectsBoundedRepeatWithoutFiniteBound(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		Prompt:            "send one line of song lyrics every hour, only until 18:00 today",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_create",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"taskInstruction": "write one line of song lyrics and send it as a DM.",
			"kind":            "interval",
			"intervalSecond":  3600,
			"repeatPolicy":    "finite",
			"timeZone":        "Asia/Seoul",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "expiresAt or maxRunCount") {
		t.Fatalf("expected finite bound failure, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 0 {
		t.Fatalf("expected no schedule to be created, got %+v", repository.taskSchedules)
	}
}

func TestScheduleCreateToolStoresExpiresAtForBoundedRepeat(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	expiresAt := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		Prompt:            "send one line of song lyrics every hour, only until 18:00 today",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_create",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"taskInstruction": "write one line of song lyrics and send it as a DM.",
			"kind":            "interval",
			"intervalSecond":  3600,
			"expiresAt":       expiresAt,
			"repeatPolicy":    "finite",
			"timeZone":        "Asia/Seoul",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule_create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 || repository.taskSchedules[0].ExpiresAt == nil {
		t.Fatalf("expected one expiring schedule, got %+v", repository.taskSchedules)
	}
	if !strings.Contains(result.ContentText(), `"expiresAt"`) || !strings.Contains(result.ContentText(), `"nextRunAt"`) {
		t.Fatalf("expected schedule result to include timing fields, got %s", result.ContentText())
	}
}

func TestScheduledToolSetKeepsOnlyAskInputAvailable(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capability.Client{}, []CapabilityToolDescriptor{{
		Name:            "user_confirm",
		Description:     "Ask the user to confirm",
		ModelVisibility: toolcontract.ToolVisibilityInternal,
	}})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"ask_input", "ask_confirm", "user_confirm", "schedule_create"})

	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default", IsScheduledRun: true})

	if !toolRegistry.IsRegistered("ask_input") || !toolRegistry.IsAllowed("ask_input") {
		t.Fatalf("expected scheduled run to keep ask_input, got %+v", toolRegistry.ListToolNames())
	}
	if toolRegistry.IsRegistered("ask_confirm") || toolRegistry.IsAllowed("ask_confirm") {
		t.Fatalf("expected runtime-owned confirmation to stay hidden, got %+v", toolRegistry.ListToolNames())
	}
	if toolRegistry.CanExpose("user_confirm") || toolRegistry.IsAllowed("user_confirm") {
		t.Fatalf("expected legacy user_confirm to stay hidden, got %+v", toolRegistry.ListToolNames())
	}
}

func TestScheduleCancelToolCancelsRequesterSchedules(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Minute)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:   "schedule-owned",
		CreatorPersonID:  "person-1",
		ConversationID:   "channel-1",
		Prompt:           "owned",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   60,
		NextRunAt:        &nextRunAt,
		ExpiresAt:        timePointer(nextRunAt.Add(time.Hour)),
		AgentProfileName: "default",
	}, {
		TaskScheduleID:   "schedule-other",
		CreatorPersonID:  "person-2",
		ConversationID:   "channel-1",
		Prompt:           "other",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   60,
		NextRunAt:        &nextRunAt,
		ExpiresAt:        timePointer(nextRunAt.Add(time.Hour)),
		AgentProfileName: "default",
	}}}
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	waitingTaskRun := taskRunService.CreateTaskRun("person-1", "channel-1", "approval required")
	if _, errorValue := taskRunService.PauseTaskRun(waitingTaskRun.TaskRunID, task.TaskStatusWaitingApproval, "approval"); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_cancel"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "channel-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_cancel",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"scope": "mine",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule_cancel success, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), `"cancelledScheduleIDs":["schedule-owned"]`) || !strings.Contains(result.ContentText(), `"cancelledScheduleCount":1`) || !strings.Contains(result.ContentText(), `"cancelledWaitCount":1`) || strings.Contains(result.ContentText(), `"taskSchedules"`) {
		t.Fatalf("expected one schedule and one wait cancelled, got %s", result.ContentText())
	}
	ownedSchedule := repository.taskSchedules[1]
	if ownedSchedule.TaskScheduleID != "schedule-owned" || ownedSchedule.NextRunAt != nil || ownedSchedule.ExpiresAt == nil || ownedSchedule.ExpiresAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("expected owned schedule to expire, got %+v", ownedSchedule)
	}
	otherSchedule := repository.taskSchedules[0]
	if otherSchedule.TaskScheduleID != "schedule-other" || otherSchedule.NextRunAt == nil {
		t.Fatalf("expected other schedule to remain active, got %+v", otherSchedule)
	}
	cancelledTaskRun, isFound := taskRunService.FindTaskRun(waitingTaskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected waiting task run to be cancelled, got found=%v task=%+v", isFound, cancelledTaskRun)
	}
}

func TestScheduleCancelToolCancelsCurrentConversationDeliverySchedules(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Minute)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:   "schedule-delivered",
		CreatorPersonID:  "person-creator",
		ConversationID:   "dm-recipient",
		Prompt:           "spam",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   60,
		NextRunAt:        &nextRunAt,
		ExpiresAt:        timePointer(nextRunAt.Add(time.Hour)),
		AgentProfileName: "default",
	}, {
		TaskScheduleID:   "schedule-other-conversation",
		CreatorPersonID:  "person-creator",
		ConversationID:   "dm-other",
		Prompt:           "other",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   60,
		NextRunAt:        &nextRunAt,
		ExpiresAt:        timePointer(nextRunAt.Add(time.Hour)),
		AgentProfileName: "default",
	}}}
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-creator", "schedule:schedule-delivered", "spam")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_cancel"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-recipient",
		ConversationID:    "dm-recipient",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_cancel",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"scope": "currentConversation",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule_cancel success, got %s", result.ContentText())
	}
	if !strings.Contains(result.ContentText(), `"cancelledScheduleCount":1`) || !strings.Contains(result.ContentText(), `"cancelledTaskRunCount":1`) || !strings.Contains(result.ContentText(), `"effectiveCancellationCount":2`) {
		t.Fatalf("expected delivered schedule and active run cancelled, got %s", result.ContentText())
	}
	deliveredSchedule := repository.taskSchedules[1]
	if deliveredSchedule.TaskScheduleID != "schedule-delivered" || deliveredSchedule.NextRunAt != nil {
		t.Fatalf("expected delivered schedule to expire, got %+v", deliveredSchedule)
	}
	otherSchedule := repository.taskSchedules[0]
	if otherSchedule.TaskScheduleID != "schedule-other-conversation" || otherSchedule.NextRunAt == nil {
		t.Fatalf("expected other conversation schedule to remain active, got %+v", otherSchedule)
	}
	cancelledTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected active scheduled task run to be cancelled, got found=%v task=%+v", isFound, cancelledTaskRun)
	}
}

func TestScheduleCancelToolFailsWhenNothingMatched(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "dm-1", "cancel request")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(&memoryTaskScheduleRepository{})
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_cancel"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		ConversationID:    "dm-1",
	})

	result, errorValue := toolRegistry.Invoke(toolcontract.WithTaskRunID(context.Background(), taskRun.TaskRunID), toolcontract.ToolInvocation{
		ToolName: "schedule_cancel",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"scope": "currentConversation",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureCode() != toolcontract.FailureCodes.NotFound.String() {
		t.Fatalf("expected not found failure, got %s", result.ContentText())
	}
	if !strings.Contains(string(result.Output.Data), `"effectiveCancellationCount":0`) {
		t.Fatalf("expected failed result to expose zero effective cancellation count, got %s", string(result.Output.Data))
	}
	if containsTaskEvent(taskRunService.ListTaskEvent(taskRun.TaskRunID), "schedule.cancelled") {
		t.Fatalf("expected not found cancellation to avoid schedule.cancelled event")
	}
}

func TestScheduleUpdateToolUpdatesIntervalSchedule(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:   "schedule-owned",
		CreatorPersonID:  "person-1",
		Name:             "status reminder",
		Prompt:           "check the status.",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   1800,
		MaxRunCount:      3,
		NextRunAt:        &nextRunAt,
		TimeZone:         "Asia/Seoul",
		AgentProfileName: "default",
	}}}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_update"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_update",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"scheduleID":     "schedule-owned",
			"intervalSecond": 3600,
			"maxRunCount":    5,
			"repeatPolicy":   "finite",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule_update success, got %s", result.ContentText())
	}
	updatedSchedule := repository.taskSchedules[0]
	if updatedSchedule.IntervalSecond != 3600 || updatedSchedule.MaxRunCount != 5 || updatedSchedule.NextRunAt == nil {
		t.Fatalf("expected interval update, got %+v", updatedSchedule)
	}
	if len(result.Effects) != 1 || result.Effects[0].Effect != "updated" || result.Effects[0].ID != "schedule-owned" {
		t.Fatalf("expected exact schedule update effect, got %+v", result.Effects)
	}
	if !strings.Contains(result.ContentText(), `"scheduleID":"schedule-owned"`) || !strings.Contains(result.ContentText(), `"intervalSecond":3600`) || !strings.Contains(result.ContentText(), `"maxRunCount":5`) {
		t.Fatalf("expected updated interval result, got %s", result.ContentText())
	}
}

func TestScheduleUpdateToolUpdatesOneOffRunAt(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	runAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:   "schedule-owned",
		CreatorPersonID:  "person-1",
		Name:             "status reminder",
		Prompt:           "check the status.",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   1800,
		MaxRunCount:      3,
		NextRunAt:        &nextRunAt,
		TimeZone:         "Asia/Seoul",
		AgentProfileName: "default",
	}}}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_update"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_update",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"scheduleID": "schedule-owned",
			"kind":       "once",
			"runAt":      runAt.Format(time.RFC3339),
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule_update success, got %s", result.ContentText())
	}
	updatedSchedule := repository.taskSchedules[0]
	if updatedSchedule.Kind != task.TaskScheduleKindOnce || updatedSchedule.RunAt == nil || !updatedSchedule.RunAt.Equal(runAt) {
		t.Fatalf("expected one-off runAt update, got %+v", updatedSchedule)
	}
	if updatedSchedule.IntervalSecond != 0 || updatedSchedule.MaxRunCount != 0 || updatedSchedule.NextRunAt == nil || !updatedSchedule.NextRunAt.Equal(runAt) {
		t.Fatalf("expected one-off cadence fields, got %+v", updatedSchedule)
	}
	if len(result.Effects) != 1 || result.Effects[0].Effect != "updated" || result.Effects[0].ID != "schedule-owned" {
		t.Fatalf("expected exact schedule update effect, got %+v", result.Effects)
	}
	if !strings.Contains(result.ContentText(), `"scheduleID":"schedule-owned"`) || !strings.Contains(result.ContentText(), `"kind":"once"`) || !strings.Contains(result.ContentText(), `"runAt"`) {
		t.Fatalf("expected one-off result, got %s", result.ContentText())
	}
}

func TestScheduleUpdateToolFailsForNonexistentID(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(&memoryTaskScheduleRepository{})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_update"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_update",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"scheduleID":     "schedule-missing",
			"intervalSecond": 3600,
			"maxRunCount":    5,
			"repeatPolicy":   "finite",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureCode() != toolcontract.FailureCodes.NotFound.String() {
		t.Fatalf("expected not found failure, got %s", result.ContentText())
	}
}

func TestScheduleUpdateToolFailsForWrongOwnerID(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:   "schedule-other",
		CreatorPersonID:  "person-2",
		Name:             "status reminder",
		Prompt:           "check the status.",
		Kind:             task.TaskScheduleKindInterval,
		IntervalSecond:   1800,
		MaxRunCount:      3,
		NextRunAt:        &nextRunAt,
		TimeZone:         "Asia/Seoul",
		AgentProfileName: "default",
	}}}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_update"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_update",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"scheduleID":     "schedule-other",
			"intervalSecond": 3600,
			"maxRunCount":    5,
			"repeatPolicy":   "finite",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureCode() != toolcontract.FailureCodes.NotFound.String() {
		t.Fatalf("expected not found failure, got %s", result.ContentText())
	}
	if repository.taskSchedules[0].IntervalSecond != 1800 || repository.taskSchedules[0].MaxRunCount != 3 {
		t.Fatalf("expected wrong-owner schedule to remain unchanged, got %+v", repository.taskSchedules[0])
	}
}

func TestScheduleCreateToolRejectsIntervalWithoutExplicitCadence(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		Prompt:            "every minute, send \"a minute has passed\"",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_create",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"taskInstruction": "1say that the minutes have passed.",
			"kind":            "interval",
			"timeZone":        "Asia/Seoul",
			"maxRunCount":     10,
			"repeatPolicy":    "finite",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() {
		t.Fatalf("expected schedule_create to reject missing intervalSecond, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 0 {
		t.Fatalf("expected no schedule to be created, got %+v", repository.taskSchedules)
	}
}

func TestScheduleCreateToolStoresMaxRunCount(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		Prompt:            `every minute, say "sorry" to me ten times`,
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_create",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"taskInstruction": "say sorry.",
			"kind":            "interval",
			"intervalSecond":  60,
			"maxRunCount":     10,
			"repeatPolicy":    "finite",
			"timeZone":        "Asia/Seoul",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule_create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one schedule, got %+v", repository.taskSchedules)
	}
	if repository.taskSchedules[0].MaxRunCount != 10 {
		t.Fatalf("expected max run count 10, got %+v", repository.taskSchedules[0])
	}
	if repository.taskSchedules[0].Prompt != "say sorry." {
		t.Fatalf("expected task instruction without cadence or run count, got %+v", repository.taskSchedules[0])
	}
}

func TestScheduleCreateToolSeparatesRepeatFieldsFromTaskInstruction(t *testing.T) {
	repository := &memoryTaskScheduleRepository{}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		Prompt:            `every minute, send hello to Wendy three times`,
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_create",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"taskInstruction": "send \"hello\" to Wendy.",
			"kind":            "interval",
			"intervalSecond":  60,
			"maxRunCount":     3,
			"repeatPolicy":    "finite",
			"timeZone":        "Asia/Seoul",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule_create success, got %s", result.ContentText())
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one schedule, got %+v", repository.taskSchedules)
	}
	taskSchedule := repository.taskSchedules[0]
	if taskSchedule.IntervalSecond != 60 || taskSchedule.MaxRunCount != 3 {
		t.Fatalf("expected structured repeat fields, got %+v", taskSchedule)
	}
	if taskSchedule.Prompt != "send \"hello\" to Wendy." {
		t.Fatalf("expected only executable action in task instruction, got %q", taskSchedule.Prompt)
	}
}

func TestScheduleCancelToolCancelsActiveScheduledTaskRuns(t *testing.T) {
	runAt := time.Now().UTC().Add(time.Hour)
	repository := &memoryTaskScheduleRepository{taskSchedules: []task.TaskSchedule{{
		TaskScheduleID:  "schedule-1",
		CreatorPersonID: "person-1",
		Prompt:          "test",
		Platform:        "mattermost",
		ConversationID:  "channel-1",
		ReplyTargetID:   "reply-target-1",
		TimeZone:        "Asia/Seoul",
		Kind:            task.TaskScheduleKindOnce,
		RunAt:           &runAt,
		NextRunAt:       &runAt,
		ExpiresAt:       timePointer(runAt.Add(time.Hour)),
	}}}
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "schedule:schedule-1", "test")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_cancel"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_cancel",
		Input:    toolcontract.MarshalToolInput(map[string]any{"scope": "mine"}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule_cancel success, got %s", result.ContentText())
	}
	cancelledTaskRun, isFound := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected scheduled task run to be cancelled, got found=%v run=%+v", isFound, cancelledTaskRun)
	}
}

func TestScheduleCreateToolRejectsMissingReplyTarget(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(&memoryTaskScheduleRepository{})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_create"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
	})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_create",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"taskInstruction": "check today's schedule and brief me.",
			"kind":            "cron",
			"cronExpression":  "0 7 * * *",
		}),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "reply target") {
		t.Fatalf("expected reply target error, got %+v", result)
	}
}

func TestScheduleCreateExecutorRejectsScheduledRunContext(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(&memoryTaskScheduleRepository{})

	result, errorValue := toolCatalogBuilder.createScheduleTool(context.Background(), scheduleCreateToolInput{
		TaskInstruction: "create a new schedule.",
		Kind:            "cron",
		CronExpression:  "* * * * *",
		RepeatPolicy:    "unbounded",
	}, toolHandlerContext{request: ToolCatalogRequest{
		IsScheduledRun:    true,
		RequesterPersonID: "person-1",
		Platform:          "mattermost",
		ConversationID:    "channel-1",
		ReplyTargetID:     "reply-target-1",
	}})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || !strings.Contains(result.ContentText(), "scheduled task executions cannot create new schedules") {
		t.Fatalf("expected scheduled run schedule_create failure, got %+v", result)
	}
}

func newScheduleListTestRegistry(repository *memoryTaskScheduleRepository, requesterPersonID string) *toolcontract.ToolSet {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskScheduleRepository(repository)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"schedule_list"})
	return toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:       "default",
		RequesterPersonID: requesterPersonID,
	})
}

func invokeScheduleList(t *testing.T, toolRegistry *toolcontract.ToolSet, input map[string]any) scheduleListToolOutput {
	t.Helper()
	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "schedule_list",
		Input:    toolcontract.MarshalToolInput(input),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected schedule_list success, got %s", result.ContentText())
	}
	var output scheduleListToolOutput
	if errorValue := json.Unmarshal([]byte(result.ContentText()), &output); errorValue != nil {
		t.Fatal(errorValue)
	}
	return output
}
