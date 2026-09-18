package adminapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

const (
	scheduleToolRequestByteLimit = 16384
	scheduleHintPageSize         = 200
)

var (
	errScheduleToolRequestInvalid    = errors.New("invalid schedule request")
	errScheduleCreateRunUnknown      = errors.New("taskRunID names no task run the requester started")
	errScheduleCreateFromScheduleRun = errors.New("a task run started by a schedule cannot create another schedule")
)

type scheduleToolCreateRequest struct {
	TaskRunID       string `json:"taskRunID"`
	TaskInstruction string `json:"taskInstruction"`
	Description     string `json:"description"`
	Kind            string `json:"kind"`
	RunAt           string `json:"runAt"`
	ExpiresAt       string `json:"expiresAt"`
	IntervalSecond  int    `json:"intervalSecond"`
	CronExpression  string `json:"cronExpression"`
	TimeZone        string `json:"timeZone"`
	MaxRunCount     int    `json:"maxRunCount"`
	RepeatPolicy    string `json:"repeatPolicy"`
	Platform        string `json:"platform"`
	ConversationID  string `json:"conversationID"`
	ReplyTargetID   string `json:"replyTargetID"`
}

type scheduleToolUpdateRequest struct {
	ScheduleHint    string  `json:"scheduleHint"`
	TaskInstruction *string `json:"taskInstruction"`
	Description     *string `json:"description"`
	Kind            *string `json:"kind"`
	RunAt           *string `json:"runAt"`
	ExpiresAt       *string `json:"expiresAt"`
	IntervalSecond  *int    `json:"intervalSecond"`
	CronExpression  *string `json:"cronExpression"`
	TimeZone        *string `json:"timeZone"`
	MaxRunCount     *int    `json:"maxRunCount"`
	RepeatPolicy    *string `json:"repeatPolicy"`
}

type scheduleToolCancelRequest struct {
	ScheduleHints []string `json:"scheduleHints"`
}

type cancelledScheduleItem struct {
	ScheduleID  string `json:"scheduleID"`
	Description string `json:"description"`
}

type scheduleToolCancelResult struct {
	Cancelled []cancelledScheduleItem `json:"cancelled"`
}

type scheduleHintConflict struct {
	Error      string                   `json:"error"`
	Hint       string                   `json:"hint"`
	Candidates []task.ScheduleCandidate `json:"candidates"`
}

func (taskScheduleHandler TaskScheduleHandler) HandleToolCreate(responseWriter http.ResponseWriter, request *http.Request) {
	creatorPersonID, isReady := taskScheduleHandler.readyForScheduleWrite(responseWriter, request)
	if !isReady {
		return
	}
	var input scheduleToolCreateRequest
	if errorValue := decodeScheduleToolRequest(request, scheduleToolCreateInputSchema, &input); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if !taskScheduleHandler.runMayCreateSchedules(responseWriter, input.TaskRunID, creatorPersonID) {
		return
	}
	referenceTime := time.Now().UTC()
	taskSchedule, errorValue := task.InitializeScheduleCreate(task.ScheduleCreateInput{
		Description:     input.Description,
		TaskInstruction: input.TaskInstruction,
		Kind:            input.Kind,
		RunAt:           input.RunAt,
		ExpiresAt:       input.ExpiresAt,
		IntervalSecond:  input.IntervalSecond,
		CronExpression:  input.CronExpression,
		TimeZone:        input.TimeZone,
		MaxRunCount:     input.MaxRunCount,
		RepeatPolicy:    input.RepeatPolicy,
	}, task.ScheduleCreateContext{
		CreatorPersonID: creatorPersonID,
		Delivery: task.ScheduleDeliveryBinding{
			Platform:       input.Platform,
			ConversationID: input.ConversationID,
			ReplyTargetID:  input.ReplyTargetID,
		},
		CompanyTimeZone: taskScheduleHandler.companyTimeZone(),
		ReferenceTime:   referenceTime,
	})
	if errorValue != nil {
		writeScheduleWriteError(responseWriter, errorValue)
		return
	}
	if errorValue := taskScheduleHandler.ListRepository.UpsertTaskSchedule(taskSchedule); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, task.ProjectScheduleMutation(taskSchedule))
}

func (taskScheduleHandler TaskScheduleHandler) HandleToolUpdate(responseWriter http.ResponseWriter, request *http.Request) {
	creatorPersonID, isReady := taskScheduleHandler.readyForScheduleWrite(responseWriter, request)
	if !isReady {
		return
	}
	var input scheduleToolUpdateRequest
	if errorValue := decodeScheduleToolRequest(request, scheduleToolUpdateInputSchema, &input); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	updateInput := task.ScheduleUpdateInput{
		Description:     input.Description,
		TaskInstruction: input.TaskInstruction,
		Kind:            input.Kind,
		RunAt:           input.RunAt,
		ExpiresAt:       input.ExpiresAt,
		IntervalSecond:  input.IntervalSecond,
		CronExpression:  input.CronExpression,
		TimeZone:        input.TimeZone,
		MaxRunCount:     input.MaxRunCount,
		RepeatPolicy:    input.RepeatPolicy,
	}
	if task.ScheduleUpdateChangesNothing(updateInput) {
		http.Error(responseWriter, task.ErrScheduleUpdateFieldRequired.Error(), http.StatusBadRequest)
		return
	}
	referenceTime := time.Now().UTC()
	ownTaskSchedules, errorValue := taskScheduleHandler.ownOpenTaskSchedules(creatorPersonID, referenceTime)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	resolution := task.ResolveScheduleHint(input.ScheduleHint, ownTaskSchedules)
	if resolution.Outcome != task.ScheduleHintResolved {
		writeScheduleHintConflict(responseWriter, "scheduleHint", input.ScheduleHint, resolution)
		return
	}
	result, errorValue := taskScheduleHandler.ListRepository.UpdateTaskSchedule(task.TaskScheduleUpdateRequest{
		TaskScheduleID:    resolution.Match.TaskScheduleID,
		RequesterPersonID: creatorPersonID,
		UpdateTaskSchedule: func(existingTaskSchedule task.TaskSchedule) (task.TaskSchedule, error) {
			return task.ApplyScheduleUpdate(existingTaskSchedule, updateInput, taskScheduleHandler.companyTimeZone(), referenceTime)
		},
	})
	if errorValue != nil {
		writeScheduleWriteError(responseWriter, errorValue)
		return
	}
	if !result.IsFound {
		http.Error(responseWriter, "task schedule not found", http.StatusNotFound)
		return
	}
	writeJSON(responseWriter, http.StatusOK, task.ProjectScheduleMutation(result.TaskSchedule))
}

func (taskScheduleHandler TaskScheduleHandler) HandleToolCancel(responseWriter http.ResponseWriter, request *http.Request) {
	creatorPersonID, isReady := taskScheduleHandler.readyForScheduleWrite(responseWriter, request)
	if !isReady {
		return
	}
	var input scheduleToolCancelRequest
	if errorValue := decodeScheduleToolRequest(request, scheduleToolCancelInputSchema, &input); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	referenceTime := time.Now().UTC()
	ownTaskSchedules, errorValue := taskScheduleHandler.ownOpenTaskSchedules(creatorPersonID, referenceTime)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	taskScheduleIDs, unresolvedHint, resolution := resolveEveryScheduleHint(input.ScheduleHints, ownTaskSchedules)
	if unresolvedHint != "" {
		writeScheduleHintConflict(responseWriter, "scheduleHints", unresolvedHint, resolution)
		return
	}
	result, errorValue := taskScheduleHandler.ListRepository.CancelTaskSchedules(task.TaskScheduleCancelRequest{
		Scope:             task.TaskScheduleCancelScopeScheduleIDs,
		RequesterPersonID: creatorPersonID,
		TaskScheduleIDs:   taskScheduleIDs,
		CancelledAt:       referenceTime,
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, scheduleToolCancelResult{Cancelled: cancelledScheduleItems(result.TaskSchedules)})
}

func resolveEveryScheduleHint(hints []string, ownTaskSchedules []task.TaskSchedule) ([]string, string, task.ScheduleHintResolution) {
	taskScheduleIDs := []string{}
	seenTaskScheduleIDs := map[string]bool{}
	for _, hint := range hints {
		resolution := task.ResolveScheduleHint(hint, ownTaskSchedules)
		if resolution.Outcome != task.ScheduleHintResolved {
			return nil, hint, resolution
		}
		if seenTaskScheduleIDs[resolution.Match.TaskScheduleID] {
			continue
		}
		seenTaskScheduleIDs[resolution.Match.TaskScheduleID] = true
		taskScheduleIDs = append(taskScheduleIDs, resolution.Match.TaskScheduleID)
	}
	return taskScheduleIDs, "", task.ScheduleHintResolution{}
}

func cancelledScheduleItems(taskSchedules []task.TaskSchedule) []cancelledScheduleItem {
	items := make([]cancelledScheduleItem, 0, len(taskSchedules))
	for _, taskSchedule := range taskSchedules {
		items = append(items, cancelledScheduleItem{
			ScheduleID:  taskSchedule.TaskScheduleID,
			Description: taskSchedule.Name,
		})
	}
	return items
}

func (taskScheduleHandler TaskScheduleHandler) readyForScheduleWrite(responseWriter http.ResponseWriter, request *http.Request) (string, bool) {
	creatorPersonID := taskScheduleHandler.signedPrincipal(request)
	if creatorPersonID == "" {
		http.Error(responseWriter, "schedule write authorization required", http.StatusForbidden)
		return "", false
	}
	if taskScheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule repository is not configured", http.StatusServiceUnavailable)
		return "", false
	}
	return creatorPersonID, true
}

func (taskScheduleHandler TaskScheduleHandler) runMayCreateSchedules(responseWriter http.ResponseWriter, taskRunID string, creatorPersonID string) bool {
	trimmedTaskRunID := strings.TrimSpace(taskRunID)
	if trimmedTaskRunID == "" {
		return true
	}
	if taskScheduleHandler.TaskRunReader == nil {
		http.Error(responseWriter, "task run reader is not configured", http.StatusServiceUnavailable)
		return false
	}
	taskRun, isFound := taskScheduleHandler.TaskRunReader.FindTaskRun(trimmedTaskRunID)
	if !isFound || strings.TrimSpace(taskRun.RequesterPersonID) != creatorPersonID {
		http.Error(responseWriter, errScheduleCreateRunUnknown.Error(), http.StatusForbidden)
		return false
	}
	if task.IsScheduleStartedTaskRun(taskRun) {
		http.Error(responseWriter, errScheduleCreateFromScheduleRun.Error(), http.StatusForbidden)
		return false
	}
	return true
}

func (taskScheduleHandler TaskScheduleHandler) signedPrincipal(request *http.Request) string {
	if taskScheduleHandler.ReaderPersonID == nil {
		return ""
	}
	return strings.TrimSpace(taskScheduleHandler.ReaderPersonID(request))
}

func (taskScheduleHandler TaskScheduleHandler) ownOpenTaskSchedules(creatorPersonID string, referenceTime time.Time) ([]task.TaskSchedule, error) {
	result, errorValue := taskScheduleHandler.ListRepository.ListTaskSchedules(task.TaskScheduleListRequest{
		CreatorPersonID: creatorPersonID,
		Page:            1,
		PageSize:        scheduleHintPageSize,
		ReferenceTime:   referenceTime,
	})
	if errorValue != nil {
		return nil, errorValue
	}
	return task.OpenSchedulesCreatedBy(result.TaskSchedules, creatorPersonID, referenceTime), nil
}

func writeScheduleWriteError(responseWriter http.ResponseWriter, errorValue error) {
	if task.IsScheduleWriteInputError(errorValue) {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
}

func writeScheduleHintConflict(responseWriter http.ResponseWriter, hintField string, hint string, resolution task.ScheduleHintResolution) {
	candidates := resolution.Candidates
	if candidates == nil {
		candidates = []task.ScheduleCandidate{}
	}
	writeJSON(responseWriter, http.StatusConflict, scheduleHintConflict{
		Error:      unresolvedScheduleHintReason(hintField, resolution.Outcome),
		Hint:       hint,
		Candidates: candidates,
	})
}

func unresolvedScheduleHintReason(hintField string, outcome task.ScheduleHintOutcome) string {
	if outcome == task.ScheduleHintAmbiguous {
		return hintField + " matched more than one schedule the requester created"
	}
	return "no schedule the requester created matched " + hintField
}

func decodeScheduleToolRequest(request *http.Request, schema json.RawMessage, input any) error {
	body, errorValue := io.ReadAll(io.LimitReader(request.Body, scheduleToolRequestByteLimit+1))
	if errorValue != nil || len(body) > scheduleToolRequestByteLimit {
		return errScheduleToolRequestInvalid
	}
	if errorValue := validateAgainstScheduleSchema(schema, body); errorValue != nil {
		return errorValue
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if errorValue := decoder.Decode(input); errorValue != nil {
		return errScheduleToolRequestInvalid
	}
	if errorValue := decoder.Decode(&struct{}{}); errorValue != io.EOF {
		return errScheduleToolRequestInvalid
	}
	return nil
}

func validateAgainstScheduleSchema(schemaDocument json.RawMessage, body []byte) error {
	var schema jsonschema.Schema
	if errorValue := json.Unmarshal(schemaDocument, &schema); errorValue != nil {
		return errorValue
	}
	resolvedSchema, errorValue := schema.Resolve(nil)
	if errorValue != nil {
		return errorValue
	}
	var instance any
	if errorValue := json.Unmarshal(body, &instance); errorValue != nil {
		return errScheduleToolRequestInvalid
	}
	if errorValue := resolvedSchema.Validate(instance); errorValue != nil {
		return errorValue
	}
	return nil
}
