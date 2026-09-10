package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type TaskRunRetryer interface {
	RetryTaskRun(context.Context, string) (task.TaskRun, error)
}

type taskRunRetryRequest struct {
	TaskRunID     string `json:"taskRunID"`
	ViewerEmail   string `json:"viewerEmail"`
	ViewerIsAdmin bool   `json:"viewerIsAdmin"`
}

func (taskMonitorHandler TaskMonitorHandler) HandleRetryTaskRun(responseWriter http.ResponseWriter, request *http.Request) {
	var retryRequest taskRunRetryRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&retryRequest); errorValue != nil {
		http.Error(responseWriter, "invalid task run retry request", http.StatusBadRequest)
		return
	}
	taskRunID := strings.TrimSpace(retryRequest.TaskRunID)
	if taskRunID == "" {
		http.Error(responseWriter, "taskRunID is required", http.StatusBadRequest)
		return
	}
	if !retryRequest.ViewerIsAdmin && strings.TrimSpace(retryRequest.ViewerEmail) == "" {
		http.Error(responseWriter, "viewer identity is required", http.StatusBadRequest)
		return
	}
	taskRun, isFound := taskMonitorHandler.TaskRunService.FindTaskRun(taskRunID)
	if !isFound {
		http.Error(responseWriter, "task run not found", http.StatusNotFound)
		return
	}
	requesterPersonID, isViewerAllowed := taskMonitorHandler.requesterScopeForViewer(retryRequest.ViewerEmail, retryRequest.ViewerIsAdmin)
	if !retryRequest.ViewerIsAdmin && strings.TrimSpace(retryRequest.ViewerEmail) != "" && taskMonitorHandler.IdentityService == nil {
		http.Error(responseWriter, "task retry is unavailable", http.StatusServiceUnavailable)
		return
	}
	if !isViewerAllowed || (requesterPersonID != "" && taskRun.RequesterPersonID != requesterPersonID) {
		http.Error(responseWriter, "task run not found", http.StatusNotFound)
		return
	}
	if taskRun.Status != task.TaskStatusFailed {
		http.Error(responseWriter, "task run is not failed", http.StatusConflict)
		return
	}
	if taskMonitorHandler.RetryTaskRun == nil {
		http.Error(responseWriter, "task retry is unavailable", http.StatusServiceUnavailable)
		return
	}
	retriedTaskRun, errorValue := taskMonitorHandler.RetryTaskRun.RetryTaskRun(request.Context(), taskRunID)
	if errorValue != nil {
		taskMonitorHandler.writeRetryTaskRunError(responseWriter, errorValue)
		return
	}
	writeJSON(responseWriter, http.StatusAccepted, map[string]string{"taskRunID": retriedTaskRun.TaskRunID, "status": string(retriedTaskRun.Status)})
}

func (taskMonitorHandler TaskMonitorHandler) writeRetryTaskRunError(responseWriter http.ResponseWriter, errorValue error) {
	switch {
	case errors.Is(errorValue, task.ErrTaskRunNotFound):
		http.Error(responseWriter, "task run not found", http.StatusNotFound)
	case errors.Is(errorValue, connectors.ErrTaskRetryConflict):
		http.Error(responseWriter, errorValue.Error(), http.StatusConflict)
	case errors.Is(errorValue, connectors.ErrTaskRetryUnavailable):
		http.Error(responseWriter, errorValue.Error(), http.StatusServiceUnavailable)
	default:
		http.Error(responseWriter, "task retry is unavailable", http.StatusServiceUnavailable)
	}
}
