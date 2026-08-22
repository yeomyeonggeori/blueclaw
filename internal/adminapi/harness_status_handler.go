package adminapi

import (
	"encoding/json"
	"net/http"

	"github.com/yeomyeonggeori/blueclaw/internal/buildrevision"
)

type HarnessStatus struct {
	Name                    string `json:"name"`
	Revision                string `json:"revision,omitempty"`
	AgentCommandPath        string `json:"agentCommandPath,omitempty"`
	RunsAsRequesterIdentity bool   `json:"runsAsRequesterIdentity"`
	ToolCatalogURL          string `json:"toolCatalogURL,omitempty"`
}

type HarnessStatusHandler struct {
	Status HarnessStatus
}

func (handler HarnessStatusHandler) HandleGetHarnessStatus(responseWriter http.ResponseWriter, _ *http.Request) {
	status := handler.Status
	status.Revision = buildrevision.Revision()
	responseWriter.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(responseWriter).Encode(status)
}
