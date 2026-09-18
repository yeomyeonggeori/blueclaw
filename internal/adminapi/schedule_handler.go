package adminapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type ScheduleSummaryRepository interface {
	SummarizeActiveSchedules(time.Time) (task.ScheduleSummary, error)
}

type ScheduleListRepository interface {
	ListSchedules(task.ScheduleListRequest) (task.ScheduleListResult, error)
	UpsertSchedule(task.Schedule) error
	UpdateSchedule(task.ScheduleUpdateRequest) (task.ScheduleUpdateResult, error)
	DeleteSchedule(task.ScheduleDeleteRequest) (task.ScheduleDeleteResult, error)
	CancelSchedules(task.ScheduleCancelRequest) (task.ScheduleCancelResult, error)
}

type ScheduleCreatorRepairRepository interface {
	RepairScheduleCreatorPersonID(task.ScheduleCreatorRepairRequest) (task.ScheduleCreatorRepairResult, error)
}

type ScheduleHandler struct {
	SummaryRepository ScheduleSummaryRepository
	ListRepository    ScheduleListRepository
	RepairRepository  ScheduleCreatorRepairRepository
	CompanyProvider   func() agentcontract.CompanyContext
	ReaderPersonID    func(*http.Request) string
}

func (scheduleHandler ScheduleHandler) companyTimeZone() string {
	if scheduleHandler.CompanyProvider == nil {
		return ""
	}
	return scheduleHandler.CompanyProvider().TimeZone
}

type scheduleCreatorRepairRequest struct {
	FromCreatorPersonID string `json:"fromCreatorPersonID"`
	ToCreatorPersonID   string `json:"toCreatorPersonID"`
}

type scheduleCancelRequest struct {
	ScheduleID      string `json:"taskScheduleID"`
	CreatorPersonID string `json:"creatorPersonID"`
}

type scheduleDeleteRequest struct {
	ScheduleID      string `json:"taskScheduleID"`
	CreatorPersonID string `json:"creatorPersonID"`
}

type scheduleUpdateRequest struct {
	ScheduleID      string  `json:"taskScheduleID"`
	CreatorPersonID string  `json:"creatorPersonID"`
	Name            *string `json:"name"`
	Kind            *string `json:"kind"`
	RunAt           *string `json:"runAt"`
	IntervalSecond  *int    `json:"intervalSecond"`
	CronExpression  *string `json:"cronExpression"`
	TimeZone        *string `json:"timeZone"`
	ExpiresAt       *string `json:"expiresAt"`
	MaxRunCount     *int    `json:"maxRunCount"`
	RepeatPolicy    *string `json:"repeatPolicy"`
}

type scheduleToolListRequest struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

func (scheduleHandler ScheduleHandler) HandleSummary(responseWriter http.ResponseWriter, request *http.Request) {
	if scheduleHandler.SummaryRepository == nil {
		http.Error(responseWriter, "task schedule summary repository is not configured", http.StatusServiceUnavailable)
		return
	}
	summary, errorValue := scheduleHandler.SummaryRepository.SummarizeActiveSchedules(time.Now().UTC())
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, summary)
}

func (scheduleHandler ScheduleHandler) HandleList(responseWriter http.ResponseWriter, request *http.Request) {
	if scheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule list repository is not configured", http.StatusServiceUnavailable)
		return
	}
	result, errorValue := scheduleHandler.ListRepository.ListSchedules(scheduleListRequestFromHTTP(request))
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"schedules":  scheduleListItems(result.Schedules),
		"count":      len(result.Schedules),
		"totalCount": result.TotalCount,
		"page":       result.Page,
		"pageSize":   result.PageSize,
		"checkedAt":  time.Now().UTC(),
	})
}

func (scheduleHandler ScheduleHandler) HandleToolList(responseWriter http.ResponseWriter, request *http.Request) {
	creatorPersonID := scheduleHandler.signedPrincipal(request)
	if creatorPersonID == "" {
		http.Error(responseWriter, "schedule list authorization required", http.StatusForbidden)
		return
	}
	if scheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule list repository is not configured", http.StatusServiceUnavailable)
		return
	}
	var input scheduleToolListRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if errorValue := decoder.Decode(&input); errorValue != nil {
		http.Error(responseWriter, "invalid schedule list request", http.StatusBadRequest)
		return
	}
	if errorValue := decoder.Decode(&struct{}{}); errorValue != io.EOF {
		http.Error(responseWriter, "invalid schedule list request", http.StatusBadRequest)
		return
	}
	referenceTime := time.Now().UTC()
	result, errorValue := scheduleHandler.ListRepository.ListSchedules(task.ScheduleListQuery(creatorPersonID, referenceTime))
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, task.ProjectScheduleList(result.Schedules, task.ScheduleListInput{
		Status: input.Status,
		Limit:  input.Limit,
	}, referenceTime))
}

func (scheduleHandler ScheduleHandler) HandleCancel(responseWriter http.ResponseWriter, request *http.Request) {
	if scheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule repository is not configured", http.StatusServiceUnavailable)
		return
	}
	var cancelRequest scheduleCancelRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&cancelRequest); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	scheduleID := strings.TrimSpace(cancelRequest.ScheduleID)
	creatorPersonID := strings.TrimSpace(cancelRequest.CreatorPersonID)
	if scheduleID == "" || creatorPersonID == "" {
		http.Error(responseWriter, "taskScheduleID and creatorPersonID are required", http.StatusBadRequest)
		return
	}
	schedule, found, errorValue := scheduleHandler.findSchedule(scheduleID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(responseWriter, "task schedule not found", http.StatusNotFound)
		return
	}
	if schedule.CreatorPersonID != creatorPersonID {
		http.Error(responseWriter, "task schedule creator mismatch", http.StatusForbidden)
		return
	}
	result, errorValue := scheduleHandler.ListRepository.CancelSchedules(task.ScheduleCancelRequest{
		Scope:             task.ScheduleCancelScopeScheduleIDs,
		RequesterPersonID: creatorPersonID,
		ScheduleIDs:       []string{scheduleID},
		CancelledAt:       time.Now().UTC(),
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

func (scheduleHandler ScheduleHandler) HandleDelete(responseWriter http.ResponseWriter, request *http.Request) {
	if scheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule repository is not configured", http.StatusServiceUnavailable)
		return
	}
	var deleteRequest scheduleDeleteRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&deleteRequest); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	scheduleID := strings.TrimSpace(deleteRequest.ScheduleID)
	creatorPersonID := strings.TrimSpace(deleteRequest.CreatorPersonID)
	if scheduleID == "" || creatorPersonID == "" {
		http.Error(responseWriter, "taskScheduleID and creatorPersonID are required", http.StatusBadRequest)
		return
	}
	schedule, found, errorValue := scheduleHandler.findSchedule(scheduleID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(responseWriter, "task schedule not found", http.StatusNotFound)
		return
	}
	if schedule.CreatorPersonID != creatorPersonID {
		http.Error(responseWriter, "task schedule creator mismatch", http.StatusForbidden)
		return
	}
	result, errorValue := scheduleHandler.ListRepository.DeleteSchedule(task.ScheduleDeleteRequest{
		ScheduleID:        scheduleID,
		RequesterPersonID: creatorPersonID,
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	if !result.IsFound {
		http.Error(responseWriter, "task schedule not found", http.StatusNotFound)
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

func (scheduleHandler ScheduleHandler) HandleUpdate(responseWriter http.ResponseWriter, request *http.Request) {
	if scheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule repository is not configured", http.StatusServiceUnavailable)
		return
	}
	var updateRequest scheduleUpdateRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&updateRequest); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	scheduleID := strings.TrimSpace(updateRequest.ScheduleID)
	creatorPersonID := strings.TrimSpace(updateRequest.CreatorPersonID)
	if scheduleID == "" || creatorPersonID == "" {
		http.Error(responseWriter, "taskScheduleID and creatorPersonID are required", http.StatusBadRequest)
		return
	}
	schedule, found, errorValue := scheduleHandler.findSchedule(scheduleID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(responseWriter, "task schedule not found", http.StatusNotFound)
		return
	}
	if schedule.CreatorPersonID != creatorPersonID {
		http.Error(responseWriter, "task schedule creator mismatch", http.StatusForbidden)
		return
	}
	result, errorValue := scheduleHandler.ListRepository.UpdateSchedule(task.ScheduleUpdateRequest{
		ScheduleID:        scheduleID,
		RequesterPersonID: creatorPersonID,
		UpdateSchedule: func(existingSchedule task.Schedule) (task.Schedule, error) {
			return applyScheduleUpdateRequest(existingSchedule, updateRequest, scheduleHandler.companyTimeZone())
		},
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if !result.IsFound {
		http.Error(responseWriter, "task schedule not found", http.StatusNotFound)
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

func (scheduleHandler ScheduleHandler) HandleRepairCreator(responseWriter http.ResponseWriter, request *http.Request) {
	if scheduleHandler.RepairRepository == nil {
		http.Error(responseWriter, "task schedule repair repository is not configured", http.StatusServiceUnavailable)
		return
	}
	var repairRequest scheduleCreatorRepairRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&repairRequest); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	fromCreatorPersonID := strings.TrimSpace(repairRequest.FromCreatorPersonID)
	toCreatorPersonID := strings.TrimSpace(repairRequest.ToCreatorPersonID)
	if fromCreatorPersonID == "" || toCreatorPersonID == "" {
		http.Error(responseWriter, "fromCreatorPersonID and toCreatorPersonID are required", http.StatusBadRequest)
		return
	}
	if fromCreatorPersonID == toCreatorPersonID {
		writeJSON(responseWriter, http.StatusOK, task.ScheduleCreatorRepairResult{})
		return
	}
	result, errorValue := scheduleHandler.RepairRepository.RepairScheduleCreatorPersonID(task.ScheduleCreatorRepairRequest{
		FromCreatorPersonID: fromCreatorPersonID,
		ToCreatorPersonID:   toCreatorPersonID,
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

type scheduleListItem struct {
	ScheduleID        string     `json:"taskScheduleID"`
	CreatorPersonID   string     `json:"creatorPersonID"`
	Name              string     `json:"name,omitempty"`
	ExecutionMode     string     `json:"executionMode"`
	Kind              string     `json:"kind"`
	IntervalSecond    int        `json:"intervalSecond,omitempty"`
	CronExpression    string     `json:"cronExpression,omitempty"`
	MaxRunCount       int        `json:"maxRunCount,omitempty"`
	CompletedRunCount int        `json:"completedRunCount"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	NextRunAt         *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt         *time.Time `json:"lastRunAt,omitempty"`
	ExpiresAt         *time.Time `json:"expiresAt,omitempty"`
	LastTaskRunID     string     `json:"lastTaskRunID,omitempty"`
	FailureCount      int        `json:"failureCount"`
	DeliveryChannelID string     `json:"deliveryChannelID"`
	ReplyTargetID     string     `json:"replyTargetID,omitempty"`
	PromptPreview     string     `json:"promptPreview"`
}

func scheduleListRequestFromHTTP(request *http.Request) task.ScheduleListRequest {
	queryValues := request.URL.Query()
	return task.ScheduleListRequest{
		ConversationID:  strings.TrimSpace(queryValues.Get("deliveryConversationID")),
		CreatorPersonID: strings.TrimSpace(queryValues.Get("creatorPersonID")),
		UnboundedOnly:   parseBoolQuery(queryValues.Get("unboundedOnly")),
		IncludeExpired:  parseBoolQuery(queryValues.Get("includeExpired")),
		Page:            parsePositiveQuery(queryValues.Get("page"), 1),
		PageSize:        parsePageSizeQuery(queryValues.Get("pageSize")),
		ReferenceTime:   time.Now().UTC(),
	}
}

func (scheduleHandler ScheduleHandler) findSchedule(scheduleID string) (task.Schedule, bool, error) {
	page := 1
	for {
		result, errorValue := scheduleHandler.ListRepository.ListSchedules(task.ScheduleListRequest{
			IncludeExpired: true,
			Page:           page,
			PageSize:       200,
			ReferenceTime:  time.Now().UTC(),
		})
		if errorValue != nil {
			return task.Schedule{}, false, errorValue
		}
		for _, schedule := range result.Schedules {
			if schedule.ScheduleID == scheduleID {
				return schedule, true, nil
			}
		}
		if len(result.Schedules) == 0 || page*result.PageSize >= result.TotalCount {
			return task.Schedule{}, false, nil
		}
		page++
	}
}

func applyScheduleUpdateRequest(schedule task.Schedule, request scheduleUpdateRequest, companyTimeZone string) (task.Schedule, error) {
	return task.ApplyScheduleUpdate(schedule, task.ScheduleUpdateInput{
		Description:    request.Name,
		Kind:           request.Kind,
		RunAt:          request.RunAt,
		ExpiresAt:      request.ExpiresAt,
		IntervalSecond: request.IntervalSecond,
		CronExpression: request.CronExpression,
		TimeZone:       request.TimeZone,
		MaxRunCount:    request.MaxRunCount,
		RepeatPolicy:   request.RepeatPolicy,
	}, companyTimeZone, time.Now().UTC())
}

func scheduleListItems(schedules []task.Schedule) []scheduleListItem {
	items := []scheduleListItem{}
	for _, schedule := range schedules {
		items = append(items, scheduleListItem{
			ScheduleID:        schedule.ScheduleID,
			CreatorPersonID:   schedule.CreatorPersonID,
			Name:              schedule.Name,
			ExecutionMode:     string(schedule.ExecutionMode),
			Kind:              string(schedule.Kind),
			IntervalSecond:    schedule.IntervalSecond,
			CronExpression:    schedule.CronExpression,
			MaxRunCount:       schedule.MaxRunCount,
			CompletedRunCount: schedule.CompletedRunCount,
			CreatedAt:         schedule.CreatedAt,
			UpdatedAt:         schedule.UpdatedAt,
			NextRunAt:         schedule.NextRunAt,
			LastRunAt:         schedule.LastRunAt,
			ExpiresAt:         schedule.ExpiresAt,
			LastTaskRunID:     schedule.LastTaskRunID,
			FailureCount:      schedule.FailureCount,
			DeliveryChannelID: schedule.ConversationID,
			ReplyTargetID:     schedule.ReplyTargetID,
			PromptPreview:     compactPromptPreview(schedule.Prompt, 160),
		})
	}
	return items
}

func parseBoolQuery(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func parsePositiveQuery(value string, fallback int) int {
	number, errorValue := strconv.Atoi(strings.TrimSpace(value))
	if errorValue != nil {
		return fallback
	}
	if number < 1 {
		return fallback
	}
	return number
}

func parsePageSizeQuery(value string) int {
	pageSize := parsePositiveQuery(value, 50)
	if pageSize > 200 {
		return 200
	}
	return pageSize
}

func compactPromptPreview(value string, limit int) string {
	words := strings.Fields(value)
	preview := strings.Join(words, " ")
	if limit <= 0 || len([]rune(preview)) <= limit {
		return preview
	}
	return string([]rune(preview)[:limit]) + "..."
}
