package agentruntime

import (
	"context"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type workspaceFileCarryingGate struct {
	gate               toolcontract.ToolCallGate
	toolCatalogBuilder *ToolCatalogBuilder
	request            ToolCatalogRequest
}

func (toolCatalogBuilder *ToolCatalogBuilder) gateCarryingWorkspaceFiles(request ToolCatalogRequest) toolcontract.ToolCallGate {
	if request.ToolCallGate == nil {
		return nil
	}
	return workspaceFileCarryingGate{gate: request.ToolCallGate, toolCatalogBuilder: toolCatalogBuilder, request: request}
}

func (carryingGate workspaceFileCarryingGate) ReviewToolCall(ctx context.Context, toolInvocation toolcontract.ToolInvocation, toolDefinition toolcontract.ToolDefinition) (toolcontract.ToolCallReview, error) {
	if !carryingGate.toolCatalogBuilder.capabilityToolCarriesWorkspaceAttachments(toolInvocation.ToolName) {
		return carryingGate.gate.ReviewToolCall(ctx, toolInvocation, toolDefinition)
	}
	carrier := func(carrierContext context.Context) (map[string]any, *toolcontract.ToolResult) {
		prepared, failure, errorValue := carryingGate.toolCatalogBuilder.prepareWorkspaceAttachments(carrierContext, toolInvocation.ToolName, carryingGate.request, toolInvocation.Input)
		if errorValue != nil {
			invalid := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "capability_input", errorValue.Error())
			return nil, &invalid
		}
		return prepared.Transport, failure
	}
	return carryingGate.gate.ReviewToolCall(approvalgate.WithWorkspaceFileCarrier(ctx, carrier), toolInvocation, toolDefinition)
}
