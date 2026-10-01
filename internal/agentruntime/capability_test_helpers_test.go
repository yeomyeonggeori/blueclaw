package agentruntime

import (
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

var testCapabilityInputSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

func (toolCatalogBuilder *ToolCatalogBuilder) UseTestCapabilityToolDescriptors(capabilityClient capability.Client, descriptors []CapabilityToolDescriptor) {
	toolCatalogBuilder.UseCapabilityToolDescriptors(capabilityClient, completeTestCapabilityToolDescriptors(descriptors))
}

func (toolCatalogBuilder *ToolCatalogBuilder) UseTestCapabilityTools(capabilityClient capability.Client, toolNames []string) {
	descriptors := make([]CapabilityToolDescriptor, 0, len(toolNames))
	for _, toolName := range toolNames {
		descriptor := CapabilityToolDescriptor{
			Name:        toolName,
			InputSchema: testCapabilityInputSchemaForTool(toolName),
		}
		if toolName == "browser_open" {
			descriptor.PrivacyClass = "user_browser"
			descriptor.RequiresUserPresence = true
		}
		descriptors = append(descriptors, descriptor)
	}
	toolCatalogBuilder.UseTestCapabilityToolDescriptors(capabilityClient, descriptors)
}

func testCapabilityInputSchemaForTool(toolName string) json.RawMessage {
	switch strings.TrimSpace(toolName) {
	case "browser_open":
		return json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`)
	case "company_broadcast_send":
		return json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`)
	case "task_add":
		return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`)
	default:
		return testCapabilityInputSchema
	}
}

func completeTestCapabilityToolDescriptors(descriptors []CapabilityToolDescriptor) []CapabilityToolDescriptor {
	completeDescriptors := make([]CapabilityToolDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		descriptor = completeTestCapabilityToolDescriptor(descriptor)
		completeDescriptors = append(completeDescriptors, descriptor)
	}
	return completeDescriptors
}

func completeTestCapabilityToolDescriptor(descriptor CapabilityToolDescriptor) CapabilityToolDescriptor {
	descriptor.Name = strings.TrimSpace(descriptor.Name)
	descriptor.CanonicalName = firstNonEmptyString(descriptor.CanonicalName, descriptor.Name)
	descriptor.Namespace = firstNonEmptyString(descriptor.Namespace, testCapabilityNamespace(descriptor.CanonicalName))
	descriptor.ModelName = firstNonEmptyString(descriptor.ModelName, descriptor.CanonicalName)
	descriptor.ModelVisibility = firstNonEmptyString(descriptor.ModelVisibility, "visible")
	descriptor.Description = firstNonEmptyString(descriptor.Description, "Test capability "+descriptor.Name)
	descriptor.PrivacyClass = firstNonEmptyString(descriptor.PrivacyClass, "test")
	descriptor.InputSchema = firstNonEmptySchema(descriptor.InputSchema, testCapabilityInputSchema)
	descriptor.OutputSchema = firstNonEmptySchema(descriptor.OutputSchema, json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`))
	if descriptor.ResultContract == nil {
		descriptor.ResultContract = &CapabilityToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		}
	}
	descriptor.PolicyResource = firstNonEmptyString(descriptor.PolicyResource, "tool:"+descriptor.CanonicalName)
	descriptor.SideEffectClass = firstNonEmptyString(descriptor.SideEffectClass, "read")
	if toolcontract.ToolDescriptorRequiresInputIntentSchema(toolcontract.ToolDescriptor{
		Visibility:      descriptor.ModelVisibility,
		SideEffectClass: descriptor.SideEffectClass,
	}) {
		descriptor.InputIntentSchema = firstNonEmptySchema(descriptor.InputIntentSchema, json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`))
	}
	descriptor.Availability.State = firstNonEmptyString(descriptor.Availability.State, "ok")
	descriptor.Idempotency.Scope = firstNonEmptyString(descriptor.Idempotency.Scope, "operation")
	return descriptor
}

func testCapabilityNamespace(name string) string {
	if separator := strings.IndexByte(name, '.'); separator > 0 {
		return name[:separator]
	}
	return name
}

func firstNonEmptySchema(schemas ...json.RawMessage) json.RawMessage {
	for _, schema := range schemas {
		if len(schema) > 0 {
			return schema
		}
	}
	return nil
}
