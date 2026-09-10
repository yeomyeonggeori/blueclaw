package adminapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestHandlePrepareShutdownQuiescesTheController(t *testing.T) {
	controller := &recordingQuiesceController{}
	handler := QuiesceHandler{Controller: controller}

	request := httptest.NewRequest(http.MethodPost, "/admin/api/runtime/prepare-shutdown", nil)
	responseRecorder := httptest.NewRecorder()
	handler.HandlePrepareShutdown(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !controller.quiesced {
		t.Fatal("expected prepare-shutdown to quiesce the controller")
	}
	if !strings.Contains(responseRecorder.Body.String(), `"quiesced":true`) {
		t.Fatalf("expected the response to report the quiesced state, got %s", responseRecorder.Body.String())
	}
}
