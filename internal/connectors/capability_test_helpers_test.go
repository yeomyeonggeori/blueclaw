package connectors

import (
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

var connectorTestCapabilityClosedSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

func connectorTestCapabilityInputSchemaForTool(toolName string) json.RawMessage {
	switch toolName {
	case "event_add":
		return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"startISO":{"type":"string"},"endISO":{"type":"string"},"isAllDay":{"type":"boolean"}},"additionalProperties":false}`)
	case "event_delete":
		return json.RawMessage(`{"type":"object","properties":{"eventHint":{"type":"string"},"userConfirmed":{"type":"boolean"}},"additionalProperties":false}`)
	case "message_send":
		return json.RawMessage(`{"type":"object","properties":{"targetType":{"type":"string"},"personHint":{"type":"string"},"message":{"type":"string"}},"additionalProperties":false}`)
	default:
		return connectorTestCapabilityClosedSchema
	}
}

func connectorTestCapabilityResultSchemaForTool(toolName string) json.RawMessage {
	switch toolName {
	case "event_add", "event_delete":
		return json.RawMessage(`{"type":"object","properties":{"eventID":{"type":"string"}},"additionalProperties":false}`)
	case "message_send":
		return json.RawMessage(`{"type":"object","properties":{"messageID":{"type":"string"}},"additionalProperties":false}`)
	case "browser_snapshot":
		return json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"},"snapshotText":{"type":"string"},"devicePath":{"type":"string"},"filename":{"type":"string"},"contentType":{"type":"string"},"sizeBytes":{"type":"number"}},"additionalProperties":false}`)
	default:
		return connectorTestCapabilityClosedSchema
	}
}

func (connectorRuntime *ConnectorRuntime) UseTestCapabilityTools(capabilityClient capability.Client, toolNames []string) {
	descriptors := make([]agentruntime.CapabilityToolDescriptor, 0, len(toolNames))
	for _, toolName := range toolNames {
		descriptors = append(descriptors, connectorTestCapabilityToolDescriptor(toolName))
	}
	connectorRuntime.UseCapabilityToolDescriptors(capabilityClient, descriptors)
}

func connectorTestCapabilityToolDescriptor(toolName string) agentruntime.CapabilityToolDescriptor {
	sideEffectClass := connectorTestCapabilitySideEffect(toolName)
	descriptor := agentruntime.CapabilityToolDescriptor{
		Name:            toolName,
		CanonicalName:   toolName,
		Namespace:       connectorTestCapabilityNamespace(toolName),
		ModelName:       toolName,
		ModelVisibility: toolcontract.ToolVisibilityModel,
		Description:     "Test capability " + toolName,
		PrivacyClass:    "test",
		InputSchema:     connectorTestCapabilityInputSchemaForTool(toolName),
		OutputSchema:    connectorTestCapabilityResultSchemaForTool(toolName),
		ResultContract:  &agentruntime.CapabilityToolResultContract{Schema: connectorTestCapabilityResultSchemaForTool(toolName)},
		PolicyResource:  "tool:" + toolName,
		SideEffectClass: sideEffectClass,
		Availability:    agentruntime.CapabilityAvailability{State: "ok"},
		Idempotency:     agentruntime.CapabilityIdempotency{Scope: "operation"},
	}
	if toolName == "browser_snapshot" {
		descriptor.PrivacyClass = "user_browser"
	}
	if sideEffectClass != toolcontract.ToolSideEffectRead {
		descriptor.CompletionEvidence = &agentruntime.CapabilityCompletionEvidence{Mode: "success", Action: toolName, TargetKind: descriptor.Namespace}
	}
	if sideEffectClass == toolcontract.ToolSideEffectDestructive || sideEffectClass == toolcontract.ToolSideEffectExternalSend {
		descriptor.RequiresApproval = true
	}
	if toolcontract.ToolDescriptorRequiresInputIntentSchema(toolcontract.ToolDescriptor{Visibility: descriptor.ModelVisibility, SideEffectClass: descriptor.SideEffectClass}) {
		descriptor.InputIntentSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	}
	return descriptor
}

func connectorTestCapabilitySideEffect(toolName string) string {
	switch toolName {
	case "browser_snapshot":
		return toolcontract.ToolSideEffectRead
	case "event_delete":
		return toolcontract.ToolSideEffectDestructive
	case "message_send":
		return toolcontract.ToolSideEffectExternalSend
	default:
		return toolcontract.ToolSideEffectWorkspaceWrite
	}
}

func connectorTestCapabilityNamespace(toolName string) string {
	if separator := strings.IndexByte(toolName, '.'); separator > 0 {
		return toolName[:separator]
	}
	return toolName
}
