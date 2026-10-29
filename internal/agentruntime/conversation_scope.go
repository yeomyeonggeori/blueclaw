package agentruntime

import (
	"path/filepath"
	"strings"
)

type ConversationResourceScope struct {
	Kind                 string `json:"kind"`
	PersonID             string `json:"personID,omitempty"`
	Resource             string `json:"resource,omitempty"`
	DefaultDirectoryPath string `json:"defaultDirectoryPath"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) conversationScope(request ToolCatalogRequest) ConversationResourceScope {
	return ConversationScopeForRequest(toolCatalogBuilder.workspaceRootPath, request)
}

func ConversationScopeForRequest(workspaceRootPath string, request ToolCatalogRequest) ConversationResourceScope {
	personID := strings.TrimSpace(request.RequesterPersonID)
	if !hasConversationScopeInput(request) || personID == "" {
		return ConversationResourceScope{
			Kind:                 "workspace",
			Resource:             "file:workspace",
			DefaultDirectoryPath: workspaceRootPath,
		}
	}
	return ConversationResourceScope{
		Kind:                 "private",
		PersonID:             personID,
		Resource:             "file:private:" + personID,
		DefaultDirectoryPath: filepath.Join(workspaceRootPath, "private", "people", personID),
	}
}

func hasConversationScopeInput(request ToolCatalogRequest) bool {
	return strings.TrimSpace(request.ConversationID) != "" ||
		strings.TrimSpace(request.ConversationType) != "" ||
		strings.TrimSpace(request.ConversationChannelID) != "" ||
		strings.TrimSpace(request.ConversationChannelName) != ""
}
