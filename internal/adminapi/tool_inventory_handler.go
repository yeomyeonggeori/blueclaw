package adminapi

import (
	"net/http"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
)

type QuarantinedToolProvider struct {
	ProviderID string `json:"providerID"`
	Reason     string `json:"reason"`
}

type ToolInventoryDocument struct {
	Tools                []string                  `json:"tools"`
	QuarantinedProviders []QuarantinedToolProvider `json:"quarantinedProviders"`
}

type ToolInventoryHandler struct {
	ToolCatalogBuilder *agentruntime.ToolCatalogBuilder
}

func (handler ToolInventoryHandler) HandleListTools(responseWriter http.ResponseWriter, _ *http.Request) {
	if handler.ToolCatalogBuilder == nil {
		http.Error(responseWriter, "the tool catalog is unavailable", http.StatusServiceUnavailable)
		return
	}
	toolSet := handler.ToolCatalogBuilder.BuildToolSet(agentruntime.ToolCatalogRequest{ProfileName: "default"})
	document := ToolInventoryDocument{
		Tools:                toolSet.ListRegisteredToolNames(),
		QuarantinedProviders: []QuarantinedToolProvider{},
	}
	for _, quarantinedProvider := range toolSet.QuarantinedProviders() {
		document.QuarantinedProviders = append(document.QuarantinedProviders, QuarantinedToolProvider{
			ProviderID: quarantinedProvider.ProviderID,
			Reason:     quarantinedProvider.Reason,
		})
	}
	writeJSON(responseWriter, http.StatusOK, document)
}
