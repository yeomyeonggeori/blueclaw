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

type TaskScheduleSummaryRepository interface {
	SummarizeActiveTaskSchedules(time.Time) (task.TaskScheduleSummary, error)
}

type TaskScheduleListRepository interface {
	ListTaskSchedules(task.TaskScheduleListRequest) (task.TaskScheduleListResult, error)
	UpdateTaskSchedule(task.TaskScheduleUpdateRequest) (task.TaskScheduleUpdateResult, error)
	DeleteTaskSchedule(task.TaskScheduleDeleteRequest) (task.TaskScheduleDeleteResult, error)
	CancelTaskSchedules(task.TaskScheduleCancelRequest) (task.TaskScheduleCancelResult, error)
}

type TaskScheduleCreatorRepairRepository interface {
	RepairTaskScheduleCreatorPersonID(task.TaskScheduleCreatorRepairRequest) (task.TaskScheduleCreatorRepairResult, error)
}

type TaskScheduleHandler struct {
	SummaryRepository TaskScheduleSummaryRepository
	ListRepository    TaskScheduleListRepository
	RepairRepository  TaskScheduleCreatorRepairRepository
	CompanyProvider   func() agentcontract.CompanyContext
	ReaderPersonID    func(*http.Request) string
}

func (taskScheduleHandler TaskScheduleHandler) companyTimeZone() string {
	if taskScheduleHandler.CompanyProvider == nil {
		return ""
	}
	return taskScheduleHandler.CompanyProvider().TimeZone
}

type taskScheduleCreatorRepairRequest struct {
	FromCreatorPersonID string `json:"fromCreatorPersonID"`
	ToCreatorPersonID   string `json:"toCreatorPersonID"`
}

type taskScheduleCancelRequest struct {
	TaskScheduleID  string `json:"taskScheduleID"`
	CreatorPersonID string `json:"creatorPersonID"`
}

type taskScheduleDeleteRequest struct {
	TaskScheduleID  string `json:"taskScheduleID"`
	CreatorPersonID string `json:"creatorPersonID"`
}

type taskScheduleUpdateRequest struct {
	TaskScheduleID  string  `json:"taskScheduleID"`
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

type taskScheduleToolListRequest struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

func (taskScheduleHandler TaskScheduleHandler) HandleSummary(responseWriter http.ResponseWriter, request *http.Request) {
	if taskScheduleHandler.SummaryRepository == nil {
		http.Error(responseWriter, "task schedule summary repository is not configured", http.StatusServiceUnavailable)
		return
	}
	summary, errorValue := taskScheduleHandler.SummaryRepository.SummarizeActiveTaskSchedules(time.Now().UTC())
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, summary)
}

func (taskScheduleHandler TaskScheduleHandler) HandleList(responseWriter http.ResponseWriter, request *http.Request) {
	if taskScheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule list repository is not configured", http.StatusServiceUnavailable)
		return
	}
	result, errorValue := taskScheduleHandler.ListRepository.ListTaskSchedules(taskScheduleListRequestFromHTTP(request))
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"schedules":  taskScheduleListItems(result.TaskSchedules),
		"count":      len(result.TaskSchedules),
		"totalCount": result.TotalCount,
		"page":       result.Page,
		"pageSize":   result.PageSize,
		"checkedAt":  time.Now().UTC(),
	})
}

func (taskScheduleHandler TaskScheduleHandler) HandleToolList(responseWriter http.ResponseWriter, request *http.Request) {
	if taskScheduleHandler.ReaderPersonID == nil {
		http.Error(responseWriter, "schedule list authorization required", http.StatusForbidden)
		return
	}
	creatorPersonID := strings.TrimSpace(taskScheduleHandler.ReaderPersonID(request))
	if creatorPersonID == "" {
		http.Error(responseWriter, "schedule list authorization required", http.StatusForbidden)
		return
	}
	if taskScheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule list repository is not configured", http.StatusServiceUnavailable)
		return
	}
	var input taskScheduleToolListRequest
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
	result, errorValue := taskScheduleHandler.ListRepository.ListTaskSchedules(task.ScheduleListQuery(creatorPersonID, referenceTime))
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, task.ProjectScheduleList(result.TaskSchedules, task.ScheduleListInput{
		Status: input.Status,
		Limit:  input.Limit,
	}, referenceTime))
}

func (taskScheduleHandler TaskScheduleHandler) HandleCancel(responseWriter http.ResponseWriter, request *http.Request) {
	if taskScheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule repository is not configured", http.StatusServiceUnavailable)
		return
	}
	var cancelRequest taskScheduleCancelRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&cancelRequest); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	taskScheduleID := strings.TrimSpace(cancelRequest.TaskScheduleID)
	creatorPersonID := strings.TrimSpace(cancelRequest.CreatorPersonID)
	if taskScheduleID == "" || creatorPersonID == "" {
		http.Error(responseWriter, "taskScheduleID and creatorPersonID are required", http.StatusBadRequest)
		return
	}
	taskSchedule, found, errorValue := taskScheduleHandler.findTaskSchedule(taskScheduleID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(responseWriter, "task schedule not found", http.StatusNotFound)
		return
	}
	if taskSchedule.CreatorPersonID != creatorPersonID {
		http.Error(responseWriter, "task schedule creator mismatch", http.StatusForbidden)
		return
	}
	result, errorValue := taskScheduleHandler.ListRepository.CancelTaskSchedules(task.TaskScheduleCancelRequest{
		Scope:             task.TaskScheduleCancelScopeScheduleIDs,
		RequesterPersonID: creatorPersonID,
		TaskScheduleIDs:   []string{taskScheduleID},
		CancelledAt:       time.Now().UTC(),
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

func (taskScheduleHandler TaskScheduleHandler) HandleDelete(responseWriter http.ResponseWriter, request *http.Request) {
	if taskScheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule repository is not configured", http.StatusServiceUnavailable)
		return
	}
	var deleteRequest taskScheduleDeleteRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&deleteRequest); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	taskScheduleID := strings.TrimSpace(deleteRequest.TaskScheduleID)
	creatorPersonID := strings.TrimSpace(deleteRequest.CreatorPersonID)
	if taskScheduleID == "" || creatorPersonID == "" {
		http.Error(responseWriter, "taskScheduleID and creatorPersonID are required", http.StatusBadRequest)
		return
	}
	taskSchedule, found, errorValue := taskScheduleHandler.findTaskSchedule(taskScheduleID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(responseWriter, "task schedule not found", http.StatusNotFound)
		return
	}
	if taskSchedule.CreatorPersonID != creatorPersonID {
		http.Error(responseWriter, "task schedule creator mismatch", http.StatusForbidden)
		return
	}
	result, errorValue := taskScheduleHandler.ListRepository.DeleteTaskSchedule(task.TaskScheduleDeleteRequest{
		TaskScheduleID:    taskScheduleID,
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

func (taskScheduleHandler TaskScheduleHandler) HandleUpdate(responseWriter http.ResponseWriter, request *http.Request) {
	if taskScheduleHandler.ListRepository == nil {
		http.Error(responseWriter, "task schedule repository is not configured", http.StatusServiceUnavailable)
		return
	}
	var updateRequest taskScheduleUpdateRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&updateRequest); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	taskScheduleID := strings.TrimSpace(updateRequest.TaskScheduleID)
	creatorPersonID := strings.TrimSpace(updateRequest.CreatorPersonID)
	if taskScheduleID == "" || creatorPersonID == "" {
		http.Error(responseWriter, "taskScheduleID and creatorPersonID are required", http.StatusBadRequest)
		return
	}
	taskSchedule, found, errorValue := taskScheduleHandler.findTaskSchedule(taskScheduleID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(responseWriter, "task schedule not found", http.StatusNotFound)
		return
	}
	if taskSchedule.CreatorPersonID != creatorPersonID {
		http.Error(responseWriter, "task schedule creator mismatch", http.StatusForbidden)
		return
	}
	result, errorValue := taskScheduleHandler.ListRepository.UpdateTaskSchedule(task.TaskScheduleUpdateRequest{
		TaskScheduleID:    taskScheduleID,
		RequesterPersonID: creatorPersonID,
		UpdateTaskSchedule: func(existingTaskSchedule task.TaskSchedule) (task.TaskSchedule, error) {
			return applyTaskScheduleUpdateRequest(existingTaskSchedule, updateRequest, taskScheduleHandler.companyTimeZone())
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

func (taskScheduleHandler TaskScheduleHandler) HandleRepairCreator(responseWriter http.ResponseWriter, request *http.Request) {
	if taskScheduleHandler.RepairRepository == nil {
		http.Error(responseWriter, "task schedule repair repository is not configured", http.StatusServiceUnavailable)
		return
	}
	var repairRequest taskScheduleCreatorRepairRequest
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
		writeJSON(responseWriter, http.StatusOK, task.TaskScheduleCreatorRepairResult{})
		return
	}
	result, errorValue := taskScheduleHandler.RepairRepository.RepairTaskScheduleCreatorPersonID(task.TaskScheduleCreatorRepairRequest{
		FromCreatorPersonID: fromCreatorPersonID,
		ToCreatorPersonID:   toCreatorPersonID,
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

type taskScheduleListItem struct {
	TaskScheduleID    string     `json:"taskScheduleID"`
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

func taskScheduleListRequestFromHTTP(request *http.Request) task.TaskScheduleListRequest {
	queryValues := request.URL.Query()
	return task.TaskScheduleListRequest{
		ConversationID:  strings.TrimSpace(queryValues.Get("deliveryConversationID")),
		CreatorPersonID: strings.TrimSpace(queryValues.Get("creatorPersonID")),
		UnboundedOnly:   parseBoolQuery(queryValues.Get("unboundedOnly")),
		IncludeExpired:  parseBoolQuery(queryValues.Get("includeExpired")),
		Page:            parsePositiveQuery(queryValues.Get("page"), 1),
		PageSize:        parsePageSizeQuery(queryValues.Get("pageSize")),
		ReferenceTime:   time.Now().UTC(),
	}
}

func (taskScheduleHandler TaskScheduleHandler) findTaskSchedule(taskScheduleID string) (task.TaskSchedule, bool, error) {
	page := 1
	for {
		result, errorValue := taskScheduleHandler.ListRepository.ListTaskSchedules(task.TaskScheduleListRequest{
			IncludeExpired: true,
			Page:           page,
			PageSize:       200,
			ReferenceTime:  time.Now().UTC(),
		})
		if errorValue != nil {
			return task.TaskSchedule{}, false, errorValue
		}
		for _, taskSchedule := range result.TaskSchedules {
			if taskSchedule.TaskScheduleID == taskScheduleID {
				return taskSchedule, true, nil
			}
		}
		if len(result.TaskSchedules) == 0 || page*result.PageSize >= result.TotalCount {
			return task.TaskSchedule{}, false, nil
		}
		page++
	}
}

func applyTaskScheduleUpdateRequest(taskSchedule task.TaskSchedule, request taskScheduleUpdateRequest, companyTimeZone string) (task.TaskSchedule, error) {
	return task.ApplyScheduleUpdate(taskSchedule, task.ScheduleUpdateInput{
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

func taskScheduleListItems(taskSchedules []task.TaskSchedule) []taskScheduleListItem {
	items := []taskScheduleListItem{}
	for _, taskSchedule := range taskSchedules {
		items = append(items, taskScheduleListItem{
			TaskScheduleID:    taskSchedule.TaskScheduleID,
			CreatorPersonID:   taskSchedule.CreatorPersonID,
			Name:              taskSchedule.Name,
			ExecutionMode:     string(taskSchedule.ExecutionMode),
			Kind:              string(taskSchedule.Kind),
			IntervalSecond:    taskSchedule.IntervalSecond,
			CronExpression:    taskSchedule.CronExpression,
			MaxRunCount:       taskSchedule.MaxRunCount,
			CompletedRunCount: taskSchedule.CompletedRunCount,
			CreatedAt:         taskSchedule.CreatedAt,
			UpdatedAt:         taskSchedule.UpdatedAt,
			NextRunAt:         taskSchedule.NextRunAt,
			LastRunAt:         taskSchedule.LastRunAt,
			ExpiresAt:         taskSchedule.ExpiresAt,
			LastTaskRunID:     taskSchedule.LastTaskRunID,
			FailureCount:      taskSchedule.FailureCount,
			DeliveryChannelID: taskSchedule.ConversationID,
			ReplyTargetID:     taskSchedule.ReplyTargetID,
			PromptPreview:     compactPromptPreview(taskSchedule.Prompt, 160),
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
