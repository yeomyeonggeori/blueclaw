package adminapi

import (
	"encoding/json"
	"net/http"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type QuiesceController interface {
	TaskIntakeGate
	SetQuiesced(bool)
}

type TaskIntakeGate interface {
	IsQuiesced() bool
}

type QuiesceHandler struct {
	Controller     QuiesceController
	TaskRunService *task.TaskRunService
}

type quiesceRequest struct {
	Enabled bool `json:"enabled"`
}

type quiesceResponse struct {
	Quiesced        bool `json:"quiesced"`
	ActiveTaskCount int  `json:"activeTaskCount"`
}

func (quiesceHandler QuiesceHandler) HandleGet(responseWriter http.ResponseWriter, _ *http.Request) {
	writeJSON(responseWriter, http.StatusOK, quiesceHandler.response())
}

func (quiesceHandler QuiesceHandler) HandlePost(responseWriter http.ResponseWriter, request *http.Request) {
	if quiesceHandler.Controller == nil {
		http.Error(responseWriter, "quiesce controller is not configured", http.StatusServiceUnavailable)
		return
	}
	var payload quiesceRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&payload); errorValue != nil {
		http.Error(responseWriter, "invalid quiesce request", http.StatusBadRequest)
		return
	}
	quiesceHandler.Controller.SetQuiesced(payload.Enabled)
	writeJSON(responseWriter, http.StatusOK, quiesceHandler.response())
}

type prepareShutdownResponse struct {
	Quiesced             bool `json:"quiesced"`
	InterruptedTaskCount int  `json:"interruptedTaskCount"`
}

func (quiesceHandler QuiesceHandler) HandlePrepareShutdown(responseWriter http.ResponseWriter, _ *http.Request) {
	if quiesceHandler.Controller != nil {
		quiesceHandler.Controller.SetQuiesced(true)
	}
	interruptedTaskCount := 0
	if quiesceHandler.TaskRunService != nil {
		interruptedTaskCount = len(quiesceHandler.TaskRunService.InterruptRuntimeTaskRunsForPlannedShutdown())
	}
	writeJSON(responseWriter, http.StatusOK, prepareShutdownResponse{
		Quiesced:             quiesceHandler.isQuiesced(),
		InterruptedTaskCount: interruptedTaskCount,
	})
}

func (quiesceHandler QuiesceHandler) response() quiesceResponse {
	return quiesceResponse{
		Quiesced:        quiesceHandler.isQuiesced(),
		ActiveTaskCount: quiesceHandler.activeTaskCount(),
	}
}

func (quiesceHandler QuiesceHandler) isQuiesced() bool {
	return quiesceHandler.Controller != nil && quiesceHandler.Controller.IsQuiesced()
}

func (quiesceHandler QuiesceHandler) activeTaskCount() int {
	if quiesceHandler.TaskRunService == nil {
		return 0
	}
	activeTaskCount := 0
	for _, taskRun := range quiesceHandler.TaskRunService.ListTaskRun() {
		if isActiveTaskRunStatus(taskRun.Status) {
			activeTaskCount++
		}
	}
	return activeTaskCount
}
