package adminapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/sessionquery"
)

type TaskSearchHandler struct {
	SessionQuery sessionquery.Service
}

func (handler TaskSearchHandler) HandleSearchTaskRuns(responseWriter http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	result, errorValue := handler.SessionQuery.Search(sessionquery.Request{
		RequesterPersonID: strings.TrimSpace(query.Get("personID")),
		Text:              strings.TrimSpace(query.Get("q")),
		ConversationID:    strings.TrimSpace(query.Get("conversationID")),
		Limit:             requestedLimit(query.Get("limit")),
	})
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	responseWriter.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(responseWriter).Encode(result)
}

func requestedLimit(value string) int {
	limit, errorValue := strconv.Atoi(strings.TrimSpace(value))
	if errorValue != nil {
		return 0
	}
	return limit
}
