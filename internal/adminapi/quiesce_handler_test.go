package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/runtimecontrol"
)

type recordingQuiesceController struct {
	quiesced bool
}

func (controller *recordingQuiesceController) SetQuiesced(isQuiesced bool) {
	controller.quiesced = isQuiesced
}

func (controller *recordingQuiesceController) IsQuiesced() bool {
	return controller.quiesced
}

type fakeMemoryQueueDrainer struct {
	result          memory.MemoryQueueDrainResult
	observedContext context.Context
}

func (drainer *fakeMemoryQueueDrainer) Drain(ctx context.Context) memory.MemoryQueueDrainResult {
	drainer.observedContext = ctx
	return drainer.result
}

func TestHandlePrepareShutdownDrainsTheMemoryQueue(t *testing.T) {
	controller := &recordingQuiesceController{}
	drainer := &fakeMemoryQueueDrainer{result: memory.MemoryQueueDrainResult{Drained: true}}
	handler := QuiesceHandler{Controller: controller, MemoryUpdateQueue: drainer}

	request := httptest.NewRequest(http.MethodPost, "/admin/api/runtime/prepare-shutdown", nil)
	responseRecorder := httptest.NewRecorder()
	handler.HandlePrepareShutdown(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !controller.quiesced {
		t.Fatal("expected prepare-shutdown to quiesce the controller")
	}
	if !strings.Contains(responseRecorder.Body.String(), `"memoryQueueDrained":true`) {
		t.Fatalf("expected the response to report a drained memory queue, got %s", responseRecorder.Body.String())
	}
	if drainer.observedContext == nil {
		t.Fatal("expected the handler to pass a context to the memory queue drain")
	}
	deadline, hasDeadline := drainer.observedContext.Deadline()
	if !hasDeadline {
		t.Fatal("expected the memory queue drain to run with a deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > runtimecontrol.MemoryDrainDeadline {
		t.Fatalf("expected the drain deadline to fit within %s, got %s remaining", runtimecontrol.MemoryDrainDeadline, remaining)
	}
}

func TestHandlePrepareShutdownReportsDroppedMemoryJobs(t *testing.T) {
	controller := &recordingQuiesceController{}
	drainer := &fakeMemoryQueueDrainer{result: memory.MemoryQueueDrainResult{
		Drained: false,
		DroppedJobs: []memory.MemoryUpdateJob{
			{JobID: "job-1", ConversationID: "conversation-1"},
			{JobID: "job-2", ConversationID: "conversation-2"},
		},
	}}
	handler := QuiesceHandler{Controller: controller, MemoryUpdateQueue: drainer}

	request := httptest.NewRequest(http.MethodPost, "/admin/api/runtime/prepare-shutdown", nil)
	responseRecorder := httptest.NewRecorder()
	handler.HandlePrepareShutdown(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"memoryQueueDrained":false`) {
		t.Fatalf("expected the response to report an undrained memory queue, got %s", responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"memoryJobsDropped":2`) {
		t.Fatalf("expected the response to report the dropped job count, got %s", responseRecorder.Body.String())
	}
}

func TestHandlePrepareShutdownWithoutAMemoryQueueReportsDrained(t *testing.T) {
	handler := QuiesceHandler{Controller: &recordingQuiesceController{}}

	request := httptest.NewRequest(http.MethodPost, "/admin/api/runtime/prepare-shutdown", nil)
	responseRecorder := httptest.NewRecorder()
	handler.HandlePrepareShutdown(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"memoryQueueDrained":true`) {
		t.Fatalf("expected no memory queue to report as trivially drained, got %s", responseRecorder.Body.String())
	}
}
