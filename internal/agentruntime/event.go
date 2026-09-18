package agentruntime

import (
	"encoding/json"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type taskLaunchEvent struct {
	Source                            TaskLaunchSource                   `json:"source"`
	SourceReference                   string                             `json:"sourceReference"`
	Platform                          string                             `json:"platform,omitempty"`
	ProfileName                       string                             `json:"profileName"`
	RequesterPersonID                 string                             `json:"requesterPersonID"`
	ConversationID                    string                             `json:"conversationID"`
	ReplyTargetID                     string                             `json:"replyTargetID,omitempty"`
	IsThread                          bool                               `json:"isThread,omitempty"`
	ConversationType                  string                             `json:"conversationType,omitempty"`
	ChannelID                         string                             `json:"channelID,omitempty"`
	ChannelName                       string                             `json:"channelName,omitempty"`
	ToolNames                         []string                           `json:"toolNames"`
	ToolRegistryVersion               string                             `json:"toolRegistryVersion,omitempty"`
	CapabilityDescriptorHash          string                             `json:"capabilityDescriptorHash,omitempty"`
	LiveCapabilityHash                string                             `json:"liveCapabilityHash,omitempty"`
	PlatformMessageDescriptorHash     string                             `json:"platformMessageDescriptorHash,omitempty"`
	LivePlatformMessageDescriptorHash string                             `json:"livePlatformMessageDescriptorHash,omitempty"`
	AllowedToolHash                   string                             `json:"allowedToolHash,omitempty"`
	HasPlatformMessageDelete          bool                               `json:"hasPlatformMessageDelete"`
	HasOldMattermostPostDelete        bool                               `json:"hasOldMattermostPostDelete"`
	HasOldPlatformDMInspect           bool                               `json:"hasOldPlatformDMInspect"`
	LiveHasPlatformMessageDelete      bool                               `json:"liveHasPlatformMessageDelete"`
	LiveHasOldMattermostPostDelete    bool                               `json:"liveHasOldMattermostPostDelete"`
	LiveHasOldPlatformDMInspect       bool                               `json:"liveHasOldPlatformDMInspect"`
	MemoryFactCount                   int                                `json:"memoryFactCount"`
	IsIntakePrecomputed               bool                               `json:"isIntakePrecomputed,omitempty"`
	ScheduledRun                      *agentcontract.ScheduledRunContext `json:"scheduledRun,omitempty"`
}

func marshalTaskLaunchEvent(request TaskLaunchRequest, profileName string, toolNames []string, registryAudit ToolRegistryAudit, memoryFactCount int) string {
	document, errorValue := json.Marshal(taskLaunchEvent{
		Source:                            request.Source,
		SourceReference:                   request.SourceReference,
		Platform:                          request.Platform,
		ProfileName:                       profileName,
		RequesterPersonID:                 request.RequesterPersonID,
		ConversationID:                    request.ConversationID,
		ReplyTargetID:                     request.OriginReplyTargetID,
		IsThread:                          request.OriginIsThread,
		ConversationType:                  request.ConversationType,
		ChannelID:                         request.ConversationChannelID,
		ChannelName:                       request.ConversationChannelName,
		ToolNames:                         append([]string{}, toolNames...),
		ToolRegistryVersion:               registryAudit.ToolRegistryVersion,
		CapabilityDescriptorHash:          registryAudit.CapabilityDescriptorHash,
		LiveCapabilityHash:                registryAudit.LiveCapabilityHash,
		PlatformMessageDescriptorHash:     registryAudit.PlatformMessageDescriptorHash,
		LivePlatformMessageDescriptorHash: registryAudit.LivePlatformMessageDescriptorHash,
		AllowedToolHash:                   registryAudit.AllowedToolHash,
		HasPlatformMessageDelete:          registryAudit.HasPlatformMessageDelete,
		HasOldMattermostPostDelete:        registryAudit.HasOldMattermostPostDelete,
		HasOldPlatformDMInspect:           registryAudit.HasOldPlatformDMInspect,
		LiveHasPlatformMessageDelete:      registryAudit.LiveHasPlatformMessageDelete,
		LiveHasOldMattermostPostDelete:    registryAudit.LiveHasOldMattermostPostDelete,
		LiveHasOldPlatformDMInspect:       registryAudit.LiveHasOldPlatformDMInspect,
		MemoryFactCount:                   memoryFactCount,
		IsIntakePrecomputed:               request.PrecomputedTurnDecision != nil,
		ScheduledRun:                      taskLaunchScheduledRunEvent(request),
	})
	if errorValue != nil {
		return "{}"
	}
	return string(document)
}

func taskLaunchScheduledRunEvent(request TaskLaunchRequest) *agentcontract.ScheduledRunContext {
	if request.ScheduledRun.IsEmpty() {
		return nil
	}
	scheduledRun := request.ScheduledRun
	return &scheduledRun
}
