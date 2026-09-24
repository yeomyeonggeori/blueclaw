package adminapi

import (
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/llmexchange"
)

type LLMCallExchangeRepository interface {
	FindLLMCallExchange(llmCallID string) (llmexchange.Exchange, bool, error)
}

type LLMCallExchangeHandler struct {
	Repository LLMCallExchangeRepository
}

func (handler LLMCallExchangeHandler) HandleGet(responseWriter http.ResponseWriter, request *http.Request) {
	if handler.Repository == nil {
		http.Error(responseWriter, "llm call exchanges are unavailable without a database", http.StatusServiceUnavailable)
		return
	}
	llmCallID := strings.TrimSpace(request.URL.Query().Get("id"))
	if llmCallID == "" {
		http.Error(responseWriter, "id is required", http.StatusBadRequest)
		return
	}
	exchange, isFound, errorValue := handler.Repository.FindLLMCallExchange(llmCallID)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	if !isFound {
		http.Error(responseWriter, "no exchange is recorded for llm call "+llmCallID, http.StatusNotFound)
		return
	}
	writeJSON(responseWriter, http.StatusOK, exchange)
}
