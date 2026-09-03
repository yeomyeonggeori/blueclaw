package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

type ConnectorEventHandler struct {
	ConnectorRuntime *connectors.ConnectorRuntime
}

func NewConnectorEventHandler(connectorRuntime *connectors.ConnectorRuntime) *ConnectorEventHandler {
	return &ConnectorEventHandler{
		ConnectorRuntime: connectorRuntime,
	}
}

func (connectorEventHandler *ConnectorEventHandler) HandleConnectorEvent() http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(responseWriter, "method not allowed", http.StatusMethodNotAllowed)
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
