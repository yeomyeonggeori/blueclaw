package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

const connectorEventsClosedNotice = "this daemon was started with -inbound acp, so a turn arrives on the acp session and not here; post this event to the relay instead"

type ConnectorEventHandler struct {
	ConnectorRuntime *connectors.ConnectorRuntime
	AdmitsHTTPEvent  bool
}

func NewConnectorEventHandler(connectorRuntime *connectors.ConnectorRuntime, admitsHTTPEvent bool) *ConnectorEventHandler {
	return &ConnectorEventHandler{
		ConnectorRuntime: connectorRuntime,
		AdmitsHTTPEvent:  admitsHTTPEvent,
	}
}

func (connectorEventHandler *ConnectorEventHandler) HandleConnectorEvent() http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(responseWriter, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !connectorEventHandler.AdmitsHTTPEvent {
			http.Error(responseWriter, connectorEventsClosedNotice, http.StatusConflict)
			return
		}

		platform := request.PathValue("platform")
		result, immediateResponse, errorValue := connectorEventHandler.ConnectorRuntime.HandleHTTPEvent(request.Context(), platform, request)
		if errorValue != nil {
			http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
			return
		}
		if immediateResponse != nil {
			writeRawResponse(responseWriter, immediateResponse)
			return
		}

		writeJSONResponse(responseWriter, http.StatusOK, result)
	}
}

func writeRawResponse(responseWriter http.ResponseWriter, response *connectors.HTTPResponse) {
	statusCode := response.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	if response.ContentType != "" {
		responseWriter.Header().Set("Content-Type", response.ContentType)
	}
	responseWriter.WriteHeader(statusCode)
	_, _ = responseWriter.Write(response.Body)
}

func writeJSONResponse(responseWriter http.ResponseWriter, statusCode int, responseDocument interface{}) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	_ = json.NewEncoder(responseWriter).Encode(responseDocument)
}
