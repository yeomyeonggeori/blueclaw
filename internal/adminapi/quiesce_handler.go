package adminapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/runtimecontrol"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type QuiesceController interface {
	TaskIntakeGate
	SetQuiesced(bool)
}

type TaskIntakeGate interface {
	IsQuiesced() bool
}

type MemoryQueueDrainer interface {
	Drain(ctx context.Context) memory.MemoryQueueDrainResult
}

type QuiesceHandler struct {
	Controller        QuiesceController
	TaskRunService    *task.TaskRunService
	MemoryUpdateQueue MemoryQueueDrainer
	Logger            *slog.Logger
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
	MemoryQueueDrained   bool `json:"memoryQueueDrained"`
	MemoryJobsDropped    int  `json:"memoryJobsDropped,omitempty"`
}

func (quiesceHandler QuiesceHandler) HandlePrepareShutdown(responseWriter http.ResponseWriter, _ *http.Request) {
	if quiesceHandler.Controller != nil {
		quiesceHandler.Controller.SetQuiesced(true)
	}
	interruptedTaskCount := 0
	if quiesceHandler.TaskRunService != nil {
		interruptedTaskCount = len(quiesceHandler.TaskRunService.InterruptRuntimeTaskRunsForPlannedShutdown())
	}
	memoryDrainResult := quiesceHandler.drainMemoryUpdateQueue()
	writeJSON(responseWriter, http.StatusOK, prepareShutdownResponse{
		Quiesced:             quiesceHandler.isQuiesced(),
		InterruptedTaskCount: interruptedTaskCount,
		MemoryQueueDrained:   memoryDrainResult.Drained,
		MemoryJobsDropped:    len(memoryDrainResult.DroppedJobs),
	})
}

func (quiesceHandler QuiesceHandler) drainMemoryUpdateQueue() memory.MemoryQueueDrainResult {
	if quiesceHandler.MemoryUpdateQueue == nil {
		return memory.MemoryQueueDrainResult{Drained: true}
	}
	drainContext, cancel := context.WithTimeout(context.Background(), runtimecontrol.MemoryDrainDeadline)
	defer cancel()
	result := quiesceHandler.MemoryUpdateQueue.Drain(drainContext)
	if !result.Drained {
		quiesceHandler.logDroppedMemoryJobs(result.DroppedJobs)
	}
	return result
}

func (quiesceHandler QuiesceHandler) logDroppedMemoryJobs(droppedJobs []memory.MemoryUpdateJob) {
	conversationIDs := make([]string, 0, len(droppedJobs))
	for _, droppedJob := range droppedJobs {
		conversationIDs = append(conversationIDs, droppedJob.ConversationID)
	}
	quiesceHandler.loggerOrDefault().Warn("memory.queue_drain_incomplete",
		slog.Int("droppedJobCount", len(droppedJobs)),
		slog.Any("conversationIDs", conversationIDs))
}

func (quiesceHandler QuiesceHandler) loggerOrDefault() *slog.Logger {
	if quiesceHandler.Logger != nil {
		return quiesceHandler.Logger
	}
	return slog.Default()
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
