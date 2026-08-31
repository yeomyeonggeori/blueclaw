package agentruntime

import (
	"path/filepath"
	"strings"
)

type ConversationResourceScope struct {
	Kind                 string `json:"kind"`
	PersonID             string `json:"personID,omitempty"`
	CircleID             string `json:"circleID,omitempty"`
	Resource             string `json:"resource,omitempty"`
	DefaultDirectoryPath string `json:"defaultDirectoryPath"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) conversationScope(request ToolCatalogRequest) ConversationResourceScope {
	return ConversationScopeForRequest(toolCatalogBuilder.workspaceRootPath, request)
}

func ConversationScopeForRequest(workspaceRootPath string, request ToolCatalogRequest) ConversationResourceScope {
	defaultScope := ConversationResourceScope{
		Kind:                 "workspace",
		Resource:             "file:workspace",
		DefaultDirectoryPath: workspaceRootPath,
	}
	if !hasConversationScopeInput(request) {
		return defaultScope
	}
	if isPrivateConversation(request) && strings.TrimSpace(request.RequesterPersonID) != "" {
		personID := strings.TrimSpace(request.RequesterPersonID)
		return ConversationResourceScope{
			Kind:                 "private",
			PersonID:             personID,
			Resource:             "file:private:" + personID,
			DefaultDirectoryPath: filepath.Join(workspaceRootPath, "private", "people", personID),
		}
	}
	circleID := circleIDFromConversation(request)
	if circleID == "" {
		circleID = "member"
	}
	return ConversationResourceScope{
		Kind:                 "circle",
		CircleID:             circleID,
		Resource:             "file:circle:" + circleID,
		DefaultDirectoryPath: filepath.Join(workspaceRootPath, "circles", circleID),
	}
}

func hasConversationScopeInput(request ToolCatalogRequest) bool {
	return strings.TrimSpace(request.ConversationID) != "" ||
		strings.TrimSpace(request.ConversationType) != "" ||
		strings.TrimSpace(request.ConversationChannelID) != "" ||
		strings.TrimSpace(request.ConversationChannelName) != ""
}

func isPrivateConversation(request ToolCatalogRequest) bool {
	conversationType := strings.ToLower(strings.TrimSpace(request.ConversationType))
	if conversationType == "d" || conversationType == "dm" || conversationType == "direct" || conversationType == "im" {
		return true
	}
	conversationID := strings.ToLower(strings.TrimSpace(request.ConversationID))
	if strings.HasPrefix(conversationID, "dm:") {
		return true
	}
	if strings.HasPrefix(conversationID, "thread:") {
		return conversationType == "d" || conversationType == "dm" || conversationType == "direct" || conversationType == "im"
	}
	return false
}

func circleIDFromConversation(request ToolCatalogRequest) string {
	channelName := strings.ToLower(strings.TrimSpace(request.ConversationChannelName))
	if strings.HasPrefix(channelName, "circle-") {
		return strings.TrimSpace(strings.TrimPrefix(channelName, "circle-"))
	}
	return ""
}
