package agentruntime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

const (
	liveCapabilityRegistryTimeout   = 10 * time.Second
	liveCapabilityRegistryFreshness = 5 * time.Minute
)

var errCapabilityRegistryUnreachable = errors.New("no capability registry is configured")

// What the runtime believes capabilityd offers: the stamp when the runtime
// document carries one, and the running host when it does not.
type CapabilityRegistry struct {
	client  capability.Client
	stamped []CapabilityToolDescriptor

	mutex           sync.Mutex
	liveDescriptors []CapabilityToolDescriptor
	liveHash        string
	readAt          time.Time
	companionStatus string
}

func NewCapabilityRegistry(client capability.Client, stampedDescriptors []CapabilityToolDescriptor) *CapabilityRegistry {
	return &CapabilityRegistry{
		client:  client,
		stamped: copyCapabilityToolDescriptors(stampedDescriptors),
	}
}

func (registry *CapabilityRegistry) StampedToolDescriptors() []CapabilityToolDescriptor {
	if registry == nil {
		return nil
	}
	return copyCapabilityToolDescriptors(registry.stamped)
}

func (registry *CapabilityRegistry) ToolDescriptors() []CapabilityToolDescriptor {
	if registry == nil {
		return nil
	}
	if len(registry.stamped) > 0 {
		return copyCapabilityToolDescriptors(registry.stamped)
	}
	if descriptors, isFresh := registry.freshLiveDescriptors(); isFresh {
		return descriptors
	}
	registryContext, cancelRegistryRead := context.WithTimeout(context.Background(), liveCapabilityRegistryTimeout)
	defer cancelRegistryRead()
	descriptors, _, errorValue := registry.ReadLive(registryContext)
	if errorValue == nil {
		return descriptors
	}
	cachedDescriptors, _, hasCachedSnapshot := registry.CachedLive()
	if !hasCachedSnapshot {
		return nil
	}
	return cachedDescriptors
}

func (registry *CapabilityRegistry) Warm(ctx context.Context) {
	if registry == nil {
		return
	}
	if _, isFresh := registry.freshLiveDescriptors(); isFresh {
		return
	}
	_, _, _ = registry.ReadLive(ctx)
}

func (registry *CapabilityRegistry) ReadLive(ctx context.Context) ([]CapabilityToolDescriptor, string, error) {
	if registry == nil || registry.client.HTTPClient == nil {
		return nil, "", errCapabilityRegistryUnreachable
	}
	served, errorValue := registry.client.ReadRegistry(ctx)
	if errorValue != nil {
		return nil, "", errorValue
	}
	hash := hashCapabilityDescriptors(served.DeviceCapabilities)
	registry.keepLive(served.DeviceCapabilities, hash, served.CompanionStatus)
	return served.DeviceCapabilities, hash, nil
}

func (registry *CapabilityRegistry) CachedLive() ([]CapabilityToolDescriptor, string, bool) {
	if registry == nil {
		return nil, "", false
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.liveHash == "" {
		return nil, "", false
	}
	return append([]CapabilityToolDescriptor{}, registry.liveDescriptors...), registry.liveHash, true
}

func (registry *CapabilityRegistry) CompanionStatus() string {
	if registry == nil {
		return ""
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	return registry.companionStatus
}

func (registry *CapabilityRegistry) UseCompanionStatus(companionStatus string) {
	if registry == nil {
		return
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	registry.companionStatus = strings.TrimSpace(companionStatus)
}

func (registry *CapabilityRegistry) freshLiveDescriptors() ([]CapabilityToolDescriptor, bool) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.liveHash == "" {
		return nil, false
	}
	if time.Since(registry.readAt) > liveCapabilityRegistryFreshness {
		return nil, false
	}
	return append([]CapabilityToolDescriptor{}, registry.liveDescriptors...), true
}

func (registry *CapabilityRegistry) keepLive(descriptors []CapabilityToolDescriptor, hash string, companionStatus string) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	registry.liveDescriptors = append([]CapabilityToolDescriptor{}, descriptors...)
	registry.liveHash = hash
	registry.readAt = time.Now()
	registry.companionStatus = strings.TrimSpace(companionStatus)
}

func (toolCatalogBuilder *ToolCatalogBuilder) capabilityToolDefinitions() []CapabilityToolDescriptor {
	return toolCatalogBuilder.capabilityRegistry.ToolDescriptors()
}

func (toolCatalogBuilder *ToolCatalogBuilder) stampedCapabilityToolDefinitions() []CapabilityToolDescriptor {
	return toolCatalogBuilder.capabilityRegistry.StampedToolDescriptors()
}

func (toolCatalogBuilder *ToolCatalogBuilder) liveCapabilityToolDescriptors(ctx context.Context) ([]CapabilityToolDescriptor, string, error) {
	return toolCatalogBuilder.capabilityRegistry.ReadLive(ctx)
}

func (toolCatalogBuilder *ToolCatalogBuilder) cachedLiveCapabilitySnapshot() ([]CapabilityToolDescriptor, string, bool) {
	return toolCatalogBuilder.capabilityRegistry.CachedLive()
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseCompanionStatus(companionStatus string) {
	toolCatalogBuilder.capabilityRegistry.UseCompanionStatus(companionStatus)
}

func (toolCatalogBuilder *ToolCatalogBuilder) companionBrowserAvailable() bool {
	companionStatus := toolCatalogBuilder.capabilityRegistry.CompanionStatus()
	if companionStatus == "" {
		return true
	}
	return companionStatus == "available"
}
