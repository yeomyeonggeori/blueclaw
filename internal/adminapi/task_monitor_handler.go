package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type TaskMonitorHandler struct {
	TaskRunService   *task.TaskRunService
	TaskStepService  *task.TaskStepService
	TaskEventService *task.TaskEventService
	IdentityService  *identity.IdentityService
}

const defaultDailyCostTaskRunLimit = 500
const maxDailyCostTaskRunLimit = 1000

type taskRunListItem struct {
	task.TaskRun
	RequesterDisplayName   string  `json:"requesterDisplayName,omitempty"`
	LLMCostUSD             float64 `json:"llmCostUSD,omitempty"`
	LLMCallCount           int     `json:"llmCallCount,omitempty"`
	HasUndeliveredQuestion bool    `json:"hasUndeliveredQuestion,omitempty"`
}

type taskRunCostSummary struct {
	CostUSD      float64
	LLMCallCount int
}

type taskDailyCostSummary struct {
	Date         string  `json:"date"`
	CostUSD      float64 `json:"costUSD"`
	TaskRunCount int     `json:"taskRunCount"`
	LLMCallCount int     `json:"llmCallCount"`
}

type taskDailyCostScope struct {
	TaskRunLimit      int  `json:"taskRunLimit"`
	TaskRunCount      int  `json:"taskRunCount"`
	TotalTaskRunCount int  `json:"totalTaskRunCount"`
	IsTruncated       bool `json:"isTruncated"`
}

type taskRunDeleteRequest struct {
	TaskRunID     string `json:"taskRunID"`
	ViewerEmail   string `json:"viewerEmail"`
	ViewerIsAdmin bool   `json:"viewerIsAdmin"`
}

func (taskMonitorHandler TaskMonitorHandler) HandleListTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	requesterPersonID, isViewerAllowed := taskMonitorHandler.requesterScope(request)
	if !isViewerAllowed {
		writeJSON(responseWriter, http.StatusOK, []taskRunListItem{})
		return
	}
	query := request.URL.Query()
	filteredTaskRuns := selectTaskRunList(
		taskMonitorHandler.TaskRunService.ListTaskRun(),
		query.Get("status"),
		requesterPersonID,
	)
	pagedTaskRuns := pageTaskRunList(filteredTaskRuns, query.Get("offset"), query.Get("limit"))
	costSummaries := map[string]taskRunCostSummary{}
	if isRequested(query.Get("includeCost")) {
		costSummaries = taskMonitorHandler.taskRunCostSummaries(pagedTaskRuns)
	}
	decoratedTaskRuns := taskMonitorHandler.decorateTaskRunList(pagedTaskRuns, costSummaries)

	if !isRequested(query.Get("includeTotal")) {
		writeJSON(responseWriter, http.StatusOK, decoratedTaskRuns)
		return
	}
	responseBody := map[string]any{
		"taskRuns":   decoratedTaskRuns,
		"totalCount": len(filteredTaskRuns),
	}
	if isRequested(query.Get("includeCost")) {
		costTaskRunLimit := dailyCostTaskRunLimit(query.Get("dailyCostTaskRunLimit"))
		dailyCostTaskRuns := limitTaskRunList(filteredTaskRuns, costTaskRunLimit)
		responseBody["dailyCostSummaries"] = dailyCostSummaries(dailyCostTaskRuns, taskMonitorHandler.taskRunCostSummaries(dailyCostTaskRuns))
		responseBody["dailyCostScope"] = taskDailyCostScope{
			TaskRunLimit:      costTaskRunLimit,
			TaskRunCount:      len(dailyCostTaskRuns),
			TotalTaskRunCount: len(filteredTaskRuns),
			IsTruncated:       len(dailyCostTaskRuns) < len(filteredTaskRuns),
		}
	}
	writeJSON(responseWriter, http.StatusOK, responseBody)
}

func (taskMonitorHandler TaskMonitorHandler) HandleGetTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	taskRunID := request.URL.Query().Get("taskRunID")
	taskRun, isFound := taskMonitorHandler.TaskRunService.FindTaskRun(taskRunID)
	if !isFound {
		http.Error(responseWriter, "task run not found", http.StatusNotFound)
		return
	}
	requesterPersonID, isViewerAllowed := taskMonitorHandler.requesterScope(request)
	if !isViewerAllowed || (requesterPersonID != "" && taskRun.RequesterPersonID != requesterPersonID) {
		http.Error(responseWriter, "task run not found", http.StatusNotFound)
		return
	}

	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"taskRun":    taskMonitorHandler.decorateTaskRun(taskRun),
		"taskSteps":  taskMonitorHandler.TaskStepService.ListTaskStep(taskRunID),
		"taskEvents": taskMonitorHandler.TaskEventService.ListTaskEvent(taskRunID),
	})
}

func (taskMonitorHandler TaskMonitorHandler) HandleDeleteTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	var deleteRequest taskRunDeleteRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&deleteRequest); errorValue != nil {
		http.Error(responseWriter, "invalid task run delete request", http.StatusBadRequest)
		return
	}
	taskRunID := strings.TrimSpace(deleteRequest.TaskRunID)
	if taskRunID == "" {
		http.Error(responseWriter, "taskRunID is required", http.StatusBadRequest)
		return
	}
	requesterPersonID, isViewerAllowed := taskMonitorHandler.requesterScopeForViewer(deleteRequest.ViewerEmail, deleteRequest.ViewerIsAdmin)
	if !isViewerAllowed {
		http.Error(responseWriter, "task run not found", http.StatusNotFound)
		return
	}
	deletedTaskRun, errorValue := taskMonitorHandler.TaskRunService.DeleteTerminalTaskRun(taskRunID, requesterPersonID)
	if errorValue != nil {
		taskMonitorHandler.writeDeleteTaskRunError(responseWriter, errorValue)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"status":    "deleted",
		"taskRunID": deletedTaskRun.TaskRunID,
	})
}

func (taskMonitorHandler TaskMonitorHandler) requesterScope(request *http.Request) (string, bool) {
	return taskMonitorHandler.requesterScopeForViewer(
		request.URL.Query().Get("viewerEmail"),
		strings.EqualFold(strings.TrimSpace(request.URL.Query().Get("viewerIsAdmin")), "true"),
	)
}

func (taskMonitorHandler TaskMonitorHandler) requesterScopeForViewer(viewerEmail string, isViewerAdmin bool) (string, bool) {
	if isViewerAdmin {
		return "", true
	}
	viewerEmail = strings.TrimSpace(viewerEmail)
	if viewerEmail == "" {
		return "", true
	}
	if taskMonitorHandler.IdentityService == nil {
		return "", false
	}
	personID, isFound := taskMonitorHandler.IdentityService.ResolvePersonIDByEmail(viewerEmail)
	if !isFound || personID == "" {
		return "", false
	}
	return personID, true
}

func (taskMonitorHandler TaskMonitorHandler) writeDeleteTaskRunError(responseWriter http.ResponseWriter, errorValue error) {
	switch {
	case errors.Is(errorValue, task.ErrTaskRunNotFound), errors.Is(errorValue, task.ErrTaskRunAccessDenied):
		http.Error(responseWriter, "task run not found", http.StatusNotFound)
	case errors.Is(errorValue, task.ErrTaskRunNotDeletable):
		http.Error(responseWriter, "task run is not deletable", http.StatusConflict)
	default:
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
	}
}

func (taskMonitorHandler TaskMonitorHandler) decorateTaskRunList(taskRuns []task.TaskRun, costSummaries map[string]taskRunCostSummary) []taskRunListItem {
	decoratedTaskRuns := make([]taskRunListItem, 0, len(taskRuns))
	for _, taskRun := range taskRuns {
		decoratedTaskRuns = append(decoratedTaskRuns, taskMonitorHandler.decorateTaskRunWithCost(taskRun, costSummaries[taskRun.TaskRunID]))
	}
	return decoratedTaskRuns
}

func (taskMonitorHandler TaskMonitorHandler) decorateTaskRun(taskRun task.TaskRun) taskRunListItem {
	return taskMonitorHandler.decorateTaskRunWithCost(taskRun, taskMonitorHandler.taskRunCostSummary(taskRun.TaskRunID))
}

func (taskMonitorHandler TaskMonitorHandler) decorateTaskRunWithCost(taskRun task.TaskRun, costSummary taskRunCostSummary) taskRunListItem {
	return taskRunListItem{
		TaskRun:                taskRun,
		RequesterDisplayName:   taskMonitorHandler.resolveRequesterDisplayName(taskRun.RequesterPersonID),
		LLMCostUSD:             costSummary.CostUSD,
		LLMCallCount:           costSummary.LLMCallCount,
		HasUndeliveredQuestion: taskMonitorHandler.hasUndeliveredQuestion(taskRun),
	}
}

func (taskMonitorHandler TaskMonitorHandler) hasUndeliveredQuestion(taskRun task.TaskRun) bool {
	if taskMonitorHandler.TaskEventService == nil || !isTaskRunWaitingForTheRequester(taskRun.Status) {
		return false
	}
	return taskRunHasUndeliveredQuestion(taskMonitorHandler.TaskEventService.ListTaskEvent(taskRun.TaskRunID))
}

func isTaskRunWaitingForTheRequester(status task.TaskStatus) bool {
	return status == task.TaskStatusWaitingApproval || status == task.TaskStatusWaitingUserInput
}

func taskRunHasUndeliveredQuestion(taskEvents []task.TaskEvent) bool {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		switch taskEvents[index].Name {
		case agentcontract.TaskEventConnectorReplySent:
			return false
		case agentcontract.TaskEventAskRequested:
			return true
		}
	}
	return false
}

func (taskMonitorHandler TaskMonitorHandler) taskRunCostSummaries(taskRuns []task.TaskRun) map[string]taskRunCostSummary {
	costSummaries := map[string]taskRunCostSummary{}
	if taskMonitorHandler.TaskEventService == nil {
		return costSummaries
	}
	for _, taskEvent := range taskMonitorHandler.TaskEventService.ListTaskEventByNameForTaskRuns(taskRunIDs(taskRuns), agentcontract.TaskEventLLMCall) {
		costUSD, isLLMCall := llmCallCostUSD(taskEvent)
		if !isLLMCall {
			continue
		}
		summary := costSummaries[taskEvent.TaskRunID]
		summary.LLMCallCount += 1
		summary.CostUSD += costUSD
		costSummaries[taskEvent.TaskRunID] = summary
	}
	return costSummaries
}

func (taskMonitorHandler TaskMonitorHandler) taskRunCostSummary(taskRunID string) taskRunCostSummary {
	costSummaries := taskMonitorHandler.taskRunCostSummaries([]task.TaskRun{{TaskRunID: taskRunID}})
	return costSummaries[taskRunID]
}

func (taskMonitorHandler TaskMonitorHandler) resolveRequesterDisplayName(requesterPersonID string) string {
	if taskMonitorHandler.IdentityService == nil {
		return ""
	}
	return taskMonitorHandler.IdentityService.ResolvePersonDisplayName(requesterPersonID)
}

func dailyCostSummaries(taskRuns []task.TaskRun, costSummaries map[string]taskRunCostSummary) []taskDailyCostSummary {
	summaryByDate := map[string]taskDailyCostSummary{}
	for _, taskRun := range taskRuns {
		costSummary := costSummaries[taskRun.TaskRunID]
		if costSummary.CostUSD <= 0 && costSummary.LLMCallCount == 0 {
			continue
		}
		date := taskRun.CreatedAt.Format("2006-01-02")
		dailySummary := summaryByDate[date]
		dailySummary.Date = date
		dailySummary.CostUSD += costSummary.CostUSD
		dailySummary.TaskRunCount += 1
		dailySummary.LLMCallCount += costSummary.LLMCallCount
		summaryByDate[date] = dailySummary
	}
	summaries := make([]taskDailyCostSummary, 0, len(summaryByDate))
	for _, dailySummary := range summaryByDate {
		summaries = append(summaries, dailySummary)
	}
	sort.Slice(summaries, func(leftIndex int, rightIndex int) bool {
		return summaries[leftIndex].Date > summaries[rightIndex].Date
	})
	return summaries
}

func taskRunIDs(taskRuns []task.TaskRun) []string {
	taskRunIDs := make([]string, 0, len(taskRuns))
	for _, taskRun := range taskRuns {
		taskRunIDs = append(taskRunIDs, taskRun.TaskRunID)
	}
	return taskRunIDs
}

func llmCallCostUSD(taskEvent task.TaskEvent) (float64, bool) {
	if taskEvent.Name != agentcontract.TaskEventLLMCall {
		return 0, false
	}
	var body struct {
		CostUSD               float64 `json:"costUSD"`
		UpstreamInferenceCost float64 `json:"upstreamInferenceCostUSD"`
	}
	if errorValue := json.Unmarshal([]byte(taskEvent.Body), &body); errorValue != nil {
		return 0, true
	}
	if body.CostUSD > 0 {
		return body.CostUSD, true
	}
	if body.UpstreamInferenceCost > 0 {
		return body.UpstreamInferenceCost, true
	}
	return 0, true
}

func selectTaskRunList(taskRuns []task.TaskRun, status string, requesterPersonID string) []task.TaskRun {
	selectedTaskRuns := filterTaskRunListByStatus(taskRuns, strings.TrimSpace(status))
	selectedTaskRuns = filterTaskRunListByRequester(selectedTaskRuns, strings.TrimSpace(requesterPersonID))
	sort.Slice(selectedTaskRuns, func(leftIndex int, rightIndex int) bool {
		return selectedTaskRuns[leftIndex].CreatedAt.After(selectedTaskRuns[rightIndex].CreatedAt)
	})
	return selectedTaskRuns
}

func filterTaskRunListByStatus(taskRuns []task.TaskRun, status string) []task.TaskRun {
	if status == "" {
		return taskRuns
	}
	filteredTaskRuns := []task.TaskRun{}
	for _, taskRun := range taskRuns {
		if string(taskRun.Status) == status {
			filteredTaskRuns = append(filteredTaskRuns, taskRun)
		}
	}
	return filteredTaskRuns
}

func filterTaskRunListByRequester(taskRuns []task.TaskRun, requesterPersonID string) []task.TaskRun {
	if requesterPersonID == "" {
		return taskRuns
	}
	filteredTaskRuns := []task.TaskRun{}
	for _, taskRun := range taskRuns {
		if taskRun.RequesterPersonID == requesterPersonID {
			filteredTaskRuns = append(filteredTaskRuns, taskRun)
		}
	}
	return filteredTaskRuns
}

func pageTaskRunList(taskRuns []task.TaskRun, offsetValue string, limitValue string) []task.TaskRun {
	offset := parseNonNegativeInteger(offsetValue)
	if offset >= len(taskRuns) {
		return []task.TaskRun{}
	}
	windowedTaskRuns := taskRuns[offset:]

	limit := parseNonNegativeInteger(limitValue)
	if limit <= 0 || len(windowedTaskRuns) <= limit {
		return windowedTaskRuns
	}
	return windowedTaskRuns[:limit]
}

func limitTaskRunList(taskRuns []task.TaskRun, limit int) []task.TaskRun {
	if limit <= 0 {
		return []task.TaskRun{}
	}
	if len(taskRuns) <= limit {
		return taskRuns
	}
	return taskRuns[:limit]
}

func dailyCostTaskRunLimit(value string) int {
	limit := parseNonNegativeInteger(value)
	if limit <= 0 {
		return defaultDailyCostTaskRunLimit
	}
	if limit > maxDailyCostTaskRunLimit {
		return maxDailyCostTaskRunLimit
	}
	return limit
}

func parseNonNegativeInteger(value string) int {
	parsed, errorValue := strconv.Atoi(strings.TrimSpace(value))
	if errorValue != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func isRequested(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}
