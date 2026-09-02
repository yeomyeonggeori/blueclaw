package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"sort"
	"strconv"
	"strings"
	"time"
)

const toolRegistryVersion = "platform-message-v1"

var newPlatformMessageToolNames = []string{
	"message_context",
	"message_search",
	"message_send",
	"message_update",
	"message_delete",
}

var oldPlatformMessageToolNames = []string{
	"platform.dm.inspect",
	"platform.dm.send",
	"mattermost_context_inspect",
	"mattermost_post_search",
	"mattermost_channel_posts_list",
	"mattermost_channel_post",
	"mattermost_post_update",
	"mattermost_post_delete",
}

type ToolRegistryAudit struct {
	ToolRegistryVersion               string `json:"toolRegistryVersion"`
	CapabilityDescriptorHash          string `json:"capabilityDescriptorHash"`
	LiveCapabilityHash                string `json:"liveCapabilityHash,omitempty"`
	PlatformMessageDescriptorHash     string `json:"platformMessageDescriptorHash"`
	LivePlatformMessageDescriptorHash string `json:"livePlatformMessageDescriptorHash,omitempty"`
	AllowedToolHash                   string `json:"allowedToolHash"`
	HasScheduleUpdate                 bool   `json:"hasScheduleUpdate"`
	HasPlatformMessageDelete          bool   `json:"hasPlatformMessageDelete"`
	HasOldMattermostPostDelete        bool   `json:"hasOldMattermostPostDelete"`
	HasOldPlatformDMInspect           bool   `json:"hasOldPlatformDMInspect"`
	LiveHasPlatformMessageDelete      bool   `json:"liveHasPlatformMessageDelete,omitempty"`
	LiveHasOldMattermostPostDelete    bool   `json:"liveHasOldMattermostPostDelete,omitempty"`
	LiveHasOldPlatformDMInspect       bool   `json:"liveHasOldPlatformDMInspect,omitempty"`
	LiveRegistryUnavailable           bool   `json:"liveRegistryUnavailable,omitempty"`
	LiveRegistryServedFromCache       bool   `json:"liveRegistryServedFromCache,omitempty"`
}

type capabilityRegistryResponse struct {
	CompanionStatus    string                     `json:"companionStatus"`
	DeviceCapabilities []CapabilityToolDescriptor `json:"deviceCapabilities"`
}

type toolRegistryMismatchError struct {
	audit ToolRegistryAudit
}

func (errorValue toolRegistryMismatchError) Error() string {
	return fmt.Sprintf("runtime_registry_mismatch: configuredHash=%s liveHash=%s platformMessageDescriptorHash=%s livePlatformMessageDescriptorHash=%s platformMessageDelete=%t livePlatformMessageDelete=%t oldMattermostPostDelete=%t liveOldMattermostPostDelete=%t oldPlatformDMInspect=%t liveOldPlatformDMInspect=%t",
		errorValue.audit.CapabilityDescriptorHash,
		errorValue.audit.LiveCapabilityHash,
		errorValue.audit.PlatformMessageDescriptorHash,
		errorValue.audit.LivePlatformMessageDescriptorHash,
		errorValue.audit.HasPlatformMessageDelete,
		errorValue.audit.LiveHasPlatformMessageDelete,
		errorValue.audit.HasOldMattermostPostDelete,
		errorValue.audit.LiveHasOldMattermostPostDelete,
		errorValue.audit.HasOldPlatformDMInspect,
		errorValue.audit.LiveHasOldPlatformDMInspect,
	)
}

func (toolCatalogBuilder *ToolCatalogBuilder) BuildToolRegistryAudit(ctx context.Context, toolSet *toolcontract.ToolSet) (ToolRegistryAudit, error) {
	configuredDescriptors := toolCatalogBuilder.capabilityToolDefinitions()
	configuredNames := capabilityDescriptorNames(configuredDescriptors)
	configuredPlatformMessageDescriptors := platformMessageCapabilityDescriptors(configuredDescriptors)
	allowedToolNames := []string{}
	if toolSet != nil {
		allowedToolNames = toolSet.ListToolNames()
	}

	audit := ToolRegistryAudit{
		ToolRegistryVersion:           toolRegistryVersion,
		CapabilityDescriptorHash:      hashCapabilityDescriptors(configuredDescriptors),
		PlatformMessageDescriptorHash: hashCapabilityDescriptors(configuredPlatformMessageDescriptors),
		AllowedToolHash:               hashStrings(allowedToolNames),
		HasScheduleUpdate:             registryContainsString(allowedToolNames, "schedule_update"),
		HasPlatformMessageDelete:      registryContainsString(configuredNames, "message_delete"),
		HasOldMattermostPostDelete:    registryContainsString(configuredNames, "mattermost_post_delete"),
		HasOldPlatformDMInspect:       registryContainsString(configuredNames, "platform.dm.inspect"),
	}

	if !requiresLiveCapabilityRegistryCheck(configuredDescriptors) {
		return audit, nil
	}

	liveDescriptors, liveHash, errorValue := toolCatalogBuilder.liveCapabilityToolDescriptors(ctx)
	if errorValue != nil {
		cachedDescriptors, cachedHash, hasCachedSnapshot := toolCatalogBuilder.cachedLiveCapabilitySnapshot()
		if !hasCachedSnapshot {
			audit.LiveRegistryUnavailable = true
			return audit, nil
		}
		liveDescriptors, liveHash = cachedDescriptors, cachedHash
		audit.LiveRegistryServedFromCache = true
	} else {
		toolCatalogBuilder.storeLiveCapabilitySnapshot(liveDescriptors, liveHash)
	}
	liveNames := capabilityDescriptorNames(liveDescriptors)
	audit.LiveCapabilityHash = liveHash
	audit.LivePlatformMessageDescriptorHash = hashCapabilityDescriptors(platformMessageCapabilityDescriptors(liveDescriptors))
	audit.LiveHasPlatformMessageDelete = registryContainsString(liveNames, "message_delete")
	audit.LiveHasOldMattermostPostDelete = registryContainsString(liveNames, "mattermost_post_delete")
	audit.LiveHasOldPlatformDMInspect = registryContainsString(liveNames, "platform.dm.inspect")

	if hasMessageRegistryMismatch(audit) {
		return audit, toolRegistryMismatchError{audit: audit}
	}

	return audit, nil
}

func capabilityDescriptorNames(toolDescriptors []CapabilityToolDescriptor) []string {
	toolNames := []string{}
	for _, toolDescriptor := range toolDescriptors {
		toolName := strings.TrimSpace(toolDescriptor.Name)
		if toolName != "" {
			toolNames = append(toolNames, toolName)
		}
	}
	return sortedUniqueRegistryStrings(toolNames)
}

func requiresLiveCapabilityRegistryCheck(configuredDescriptors []CapabilityToolDescriptor) bool {
	return len(configuredDescriptors) > 0
}

func (toolCatalogBuilder *ToolCatalogBuilder) liveCapabilityToolDescriptors(ctx context.Context) ([]CapabilityToolDescriptor, string, error) {
	var response capabilityRegistryResponse
	if errorValue := toolCatalogBuilder.capabilityClient.GetJSON(ctx, "/v1/capabilities", &response); errorValue != nil {
		return nil, "", errorValue
	}
	toolCatalogBuilder.UseCompanionStatus(response.CompanionStatus)
	toolDescriptors := []CapabilityToolDescriptor{}
	for _, descriptor := range response.DeviceCapabilities {
		toolName := strings.TrimSpace(descriptor.Name)
		if toolName != "" {
			toolDescriptors = append(toolDescriptors, CapabilityToolDescriptor{
				Name:              toolName,
				InputSchema:       append(json.RawMessage{}, descriptor.InputSchema...),
				InputIntentSchema: append(json.RawMessage{}, descriptor.InputIntentSchema...),
				ResultContract:    descriptor.ResultContract,
				SideEffectClass:   strings.TrimSpace(descriptor.SideEffectClass),
				RequiresApproval:  descriptor.RequiresApproval,
				Idempotency:       descriptor.Idempotency,
			})
		}
	}
	return toolDescriptors, hashCapabilityDescriptors(toolDescriptors), nil
}

func hasMessageRegistryMismatch(audit ToolRegistryAudit) bool {
	if audit.HasOldMattermostPostDelete || audit.HasOldPlatformDMInspect {
		return true
	}
	if audit.LiveHasOldMattermostPostDelete || audit.LiveHasOldPlatformDMInspect {
		return true
	}
	if audit.HasPlatformMessageDelete != audit.LiveHasPlatformMessageDelete {
		return true
	}
	return false
}

func platformMessageCapabilityDescriptors(toolDescriptors []CapabilityToolDescriptor) []CapabilityToolDescriptor {
	result := []CapabilityToolDescriptor{}
	for _, toolDescriptor := range toolDescriptors {
		toolName := strings.TrimSpace(toolDescriptor.Name)
		if strings.TrimSpace(toolDescriptor.Namespace) == "message" || registryContainsString(oldPlatformMessageToolNames, toolName) {
			result = append(result, toolDescriptor)
		}
	}
	return result
}

func hashCapabilityDescriptors(toolDescriptors []CapabilityToolDescriptor) string {
	signatures := []string{}
	for _, toolDescriptor := range toolDescriptors {
		toolName := strings.TrimSpace(toolDescriptor.Name)
		if toolName == "" {
			continue
		}
		signatures = append(signatures, strings.Join([]string{
			toolName,
			normalizedJSONSchemaString(toolDescriptor.InputSchema),
			normalizedJSONSchemaString(toolDescriptor.InputIntentSchema),
			normalizedCapabilityResultContractString(toolDescriptor.ResultContract),
			strings.TrimSpace(toolDescriptor.SideEffectClass),
			strconv.FormatBool(toolDescriptor.RequiresApproval),
			capabilityToolIdempotency(toolDescriptor.Idempotency),
			strings.TrimSpace(toolDescriptor.Idempotency.Scope),
		}, "\t"))
	}
	return hashStrings(signatures)
}

func normalizedCapabilityResultContractString(contract *CapabilityToolResultContract) string {
	if contract == nil {
		return ""
	}
	document, errorValue := json.Marshal(contract)
	if errorValue != nil {
		return ""
	}
	return normalizedJSONSchemaString(document)
}

func normalizedJSONSchemaString(schema json.RawMessage) string {
	if len(schema) == 0 {
		return ""
	}
	var document any
	if errorValue := json.Unmarshal(schema, &document); errorValue != nil {
		return strings.TrimSpace(string(schema))
	}
	normalizedDocument, errorValue := json.Marshal(document)
	if errorValue != nil {
		return strings.TrimSpace(string(schema))
	}
	return string(normalizedDocument)
}

func hashStrings(values []string) string {
	normalizedValues := sortedUniqueRegistryStrings(values)
	document := strings.Join(normalizedValues, "\n")
	sum := sha256.Sum256([]byte(document))
	return hex.EncodeToString(sum[:])
}

func sortedUniqueRegistryStrings(values []string) []string {
	seenValues := map[string]bool{}
	result := []string{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" || seenValues[trimmedValue] {
			continue
		}
		seenValues[trimmedValue] = true
		result = append(result, trimmedValue)
	}
	sort.Strings(result)
	return result
}

func registryContainsAnyString(values []string, candidates []string) bool {
	for _, candidate := range candidates {
		if registryContainsString(values, candidate) {
			return true
		}
	}
	return false
}

func registryContainsString(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func (toolCatalogBuilder *ToolCatalogBuilder) reachableCapabilityToolDefinitions() []CapabilityToolDescriptor {
	descriptors := toolCatalogBuilder.capabilityToolDefinitions()
	if !containsBrowserDescriptor(descriptors) || toolCatalogBuilder.companionBrowserAvailable() {
		return descriptors
	}
	reachableDescriptors := []CapabilityToolDescriptor{}
	for _, descriptor := range descriptors {
		if descriptorIsBrowserCapability(descriptor) {
			continue
		}
		reachableDescriptors = append(reachableDescriptors, descriptor)
	}
	return reachableDescriptors
}

func containsBrowserDescriptor(descriptors []CapabilityToolDescriptor) bool {
	for _, descriptor := range descriptors {
		if descriptorIsBrowserCapability(descriptor) {
			return true
		}
	}
	return false
}

// The descriptor says whether a tool reaches the requester's own machine, which
// is what the companion connection provides. Neither its name nor its namespace
// can answer that question.
func descriptorIsBrowserCapability(descriptor CapabilityToolDescriptor) bool {
	return descriptor.RequiresRequesterDevice
}

func (toolCatalogBuilder *ToolCatalogBuilder) companionBrowserAvailable() bool {
	toolCatalogBuilder.companionStatusMutex.Lock()
	defer toolCatalogBuilder.companionStatusMutex.Unlock()
	if toolCatalogBuilder.companionStatusValue == "" {
		return true
	}
	return toolCatalogBuilder.companionStatusValue == "available"
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseCompanionStatus(companionStatus string) {
	toolCatalogBuilder.companionStatusMutex.Lock()
	defer toolCatalogBuilder.companionStatusMutex.Unlock()
	toolCatalogBuilder.companionStatusValue = strings.TrimSpace(companionStatus)
	toolCatalogBuilder.companionStatusCheckedAt = time.Now()
}

func (toolCatalogBuilder *ToolCatalogBuilder) cachedLiveCapabilitySnapshot() ([]CapabilityToolDescriptor, string, bool) {
	toolCatalogBuilder.liveSnapshotMutex.Lock()
	defer toolCatalogBuilder.liveSnapshotMutex.Unlock()
	if toolCatalogBuilder.liveSnapshotHash == "" {
		return nil, "", false
	}
	return append([]CapabilityToolDescriptor{}, toolCatalogBuilder.liveSnapshotDescriptors...), toolCatalogBuilder.liveSnapshotHash, true
}

func (toolCatalogBuilder *ToolCatalogBuilder) storeLiveCapabilitySnapshot(descriptors []CapabilityToolDescriptor, hash string) {
	toolCatalogBuilder.liveSnapshotMutex.Lock()
	defer toolCatalogBuilder.liveSnapshotMutex.Unlock()
	toolCatalogBuilder.liveSnapshotDescriptors = append([]CapabilityToolDescriptor{}, descriptors...)
	toolCatalogBuilder.liveSnapshotHash = hash
}
