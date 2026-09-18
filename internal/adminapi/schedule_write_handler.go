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

var errScheduleToolRequestInvalid = errors.New("invalid schedule request")

type scheduleToolCreateRequest struct {
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

func (scheduleHandler ScheduleHandler) HandleToolCreate(responseWriter http.ResponseWriter, request *http.Request) {
	creatorPersonID, isReady := scheduleHandler.readyForScheduleWrite(responseWriter, request)
	if !isReady {
		return
	}
	var input scheduleToolCreateRequest
	if errorValue := decodeScheduleToolRequest(request, scheduleToolCreateInputSchema, &input); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	referenceTime := time.Now().UTC()
	schedule, errorValue := task.CreateSchedule(scheduleHandler.ListRepository, task.ScheduleCreateInput{
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
		CompanyTimeZone: scheduleHandler.companyTimeZone(),
		ReferenceTime:   referenceTime,
	})
	if errorValue != nil {
		writeScheduleWriteError(responseWriter, errorValue)
		return
	}
	writeJSON(responseWriter, http.StatusOK, task.ProjectScheduleMutation(schedule))
}

func (scheduleHandler ScheduleHandler) HandleToolUpdate(responseWriter http.ResponseWriter, request *http.Request) {
	creatorPersonID, isReady := scheduleHandler.readyForScheduleWrite(responseWriter, request)
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
	ownSchedules, errorValue := scheduleHandler.ownOpenSchedules(creatorPersonID, referenceTime)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	resolution := task.ResolveScheduleHint(input.ScheduleHint, ownSchedules)
	if resolution.Outcome != task.ScheduleHintResolved {
		writeScheduleHintConflict(responseWriter, "scheduleHint", input.ScheduleHint, resolution)
		return
	}
	result, errorValue := scheduleHandler.ListRepository.UpdateSchedule(task.ScheduleUpdateRequest{
		ScheduleID:        resolution.Match.ScheduleID,
		RequesterPersonID: creatorPersonID,
		UpdateSchedule: func(existingSchedule task.Schedule) (task.Schedule, error) {
			return task.ApplyScheduleUpdate(existingSchedule, updateInput, scheduleHandler.companyTimeZone(), referenceTime)
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
	writeJSON(responseWriter, http.StatusOK, task.ProjectScheduleMutation(result.Schedule))
}

func (scheduleHandler ScheduleHandler) HandleToolCancel(responseWriter http.ResponseWriter, request *http.Request) {
	creatorPersonID, isReady := scheduleHandler.readyForScheduleWrite(responseWriter, request)
	if !isReady {
		return
	}
	var input scheduleToolCancelRequest
	if errorValue := decodeScheduleToolRequest(request, scheduleToolCancelInputSchema, &input); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	referenceTime := time.Now().UTC()
	ownSchedules, errorValue := scheduleHandler.ownOpenSchedules(creatorPersonID, referenceTime)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	scheduleIDs, unresolvedHint, resolution := resolveEveryScheduleHint(input.ScheduleHints, ownSchedules)
	if unresolvedHint != "" {
		writeScheduleHintConflict(responseWriter, "scheduleHints", unresolvedHint, resolution)
		return
	}
	result, errorValue := scheduleHandler.ListRepository.CancelSchedules(task.ScheduleCancelRequest{
		Scope:             task.ScheduleCancelScopeScheduleIDs,
		RequesterPersonID: creatorPersonID,
		ScheduleIDs:       scheduleIDs,
		CancelledAt:       referenceTime,
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, scheduleToolCancelResult{Cancelled: cancelledScheduleItems(result.Schedules)})
}

func resolveEveryScheduleHint(hints []string, ownSchedules []task.Schedule) ([]string, string, task.ScheduleHintResolution) {
	scheduleIDs := []string{}
	seenScheduleIDs := map[string]bool{}
	for _, hint := range hints {
		resolution := task.ResolveScheduleHint(hint, ownSchedules)
		if resolution.Outcome != task.ScheduleHintResolved {
			return nil, hint, resolution
		}
		if seenScheduleIDs[resolution.Match.ScheduleID] {
			continue
		}
		seenScheduleIDs[resolution.Match.ScheduleID] = true
		scheduleIDs = append(scheduleIDs, resolution.Match.ScheduleID)
	}
	return scheduleIDs, "", task.ScheduleHintResolution{}
}

func cancelledScheduleItems(schedules []task.Schedule) []cancelledScheduleItem {
	items := make([]cancelledScheduleItem, 0, len(schedules))
	for _, schedule := range schedules {
		items = append(items, cancelledScheduleItem{
			ScheduleID:  schedule.ScheduleID,
			Description: schedule.Name,
		})
	}
	return items
}

func (scheduleHandler ScheduleHandler) readyForScheduleWrite(responseWriter http.ResponseWriter, request *http.Request) (string, bool) {
	creatorPersonID := scheduleHandler.signedPrincipal(request)
	if creatorPersonID == "" {
		http.Error(responseWriter, "schedule write authorization required", http.StatusForbidden)
		return "", false
	}
	if scheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule repository is not configured", http.StatusServiceUnavailable)
		return "", false
	}
	return creatorPersonID, true
}

func (scheduleHandler ScheduleHandler) signedPrincipal(request *http.Request) string {
	if scheduleHandler.ReaderPersonID == nil {
		return ""
	}
	return strings.TrimSpace(scheduleHandler.ReaderPersonID(request))
}

func (scheduleHandler ScheduleHandler) ownOpenSchedules(creatorPersonID string, referenceTime time.Time) ([]task.Schedule, error) {
	result, errorValue := scheduleHandler.ListRepository.ListSchedules(task.ScheduleListRequest{
		CreatorPersonID: creatorPersonID,
		Page:            1,
		PageSize:        scheduleHintPageSize,
		ReferenceTime:   referenceTime,
	})
	if errorValue != nil {
		return nil, errorValue
	}
	return task.OpenSchedulesCreatedBy(result.Schedules, creatorPersonID, referenceTime), nil
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
