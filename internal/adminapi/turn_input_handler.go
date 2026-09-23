package adminapi

import (
	"net/http"
	"strings"
)

type TurnInputRepository interface {
	FindPartedTaskEventDocument(taskEventID string) (string, bool, error)
}

type TurnInputHandler struct {
	Repository TurnInputRepository
}

func (handler TurnInputHandler) HandleGet(responseWriter http.ResponseWriter, request *http.Request) {
	if handler.Repository == nil {
		http.Error(responseWriter, "turn inputs are unavailable without a database", http.StatusServiceUnavailable)
		return
	}
	taskEventID := strings.TrimSpace(request.URL.Query().Get("id"))
	if taskEventID == "" {
		http.Error(responseWriter, "id is required", http.StatusBadRequest)
		return
	}
	document, isFound, errorValue := handler.Repository.FindPartedTaskEventDocument(taskEventID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	if !isFound {
		http.Error(responseWriter, "no turn input is recorded as task event "+taskEventID, http.StatusNotFound)
		return
	}
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.Write([]byte(document))
}
