package capability

import (
	"context"
	"strings"
)

const registryPath = "/v1/capabilities"

type Registry struct {
	CompanionStatus    string           `json:"companionStatus"`
	DeviceCapabilities []ToolDescriptor `json:"deviceCapabilities"`
}

func (client Client) ReadRegistry(ctx context.Context) (Registry, error) {
	var registry Registry
	if errorValue := client.GetJSON(ctx, registryPath, &registry); errorValue != nil {
		return Registry{}, errorValue
	}
	namedDescriptors := make([]ToolDescriptor, 0, len(registry.DeviceCapabilities))
	for _, descriptor := range registry.DeviceCapabilities {
		descriptor.Name = strings.TrimSpace(descriptor.Name)
		if descriptor.Name == "" {
			continue
		}
		namedDescriptors = append(namedDescriptors, descriptor)
	}
	registry.DeviceCapabilities = namedDescriptors
	return registry, nil
}
