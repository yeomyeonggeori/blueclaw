package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const toolCatalogServerName = "blueclaw-tool-catalog"

type RequesterToolSet struct {
	RequesterPersonID string
	TaskRunID         string
	ToolSet           *toolcontract.ToolSet
	HarnessSession    HarnessSession
	ToolAudience      ToolAudience
	ResponseLanguage  string
	Prompt            string

	ObserveToolInvocation func(toolName string, isSucceeded bool)
}

func NewToolCatalogServer(requesterToolSet RequesterToolSet, version string) (*mcp.Server, error) {
	if strings.TrimSpace(requesterToolSet.RequesterPersonID) == "" {
		return nil, errors.New("tool catalog server refuses to serve a tool set with no requester")
	}
	if requesterToolSet.ToolSet == nil {
		return nil, errors.New("tool catalog server requires a tool set")
	}
	server := mcp.NewServer(&mcp.Implementation{Name: toolCatalogServerName, Version: version}, nil)
	for _, toolDescriptor := range requesterToolSet.ToolSet.ListDescribedToolDefinitions() {
		if !isPublishedToAudience(toolDescriptor, requesterToolSet.ToolAudience) {
			continue
		}
		tool, isServable := servableTool(toolDescriptor)
		if !isServable {
			continue
		}
		server.AddTool(tool, invokeThroughToolSet(requesterToolSet, toolDescriptor, tool.OutputSchema != nil))
	}
	return server, nil
}

func servableTool(toolDescriptor toolcontract.ToolDescriptor) (*mcp.Tool, bool) {
	inputSchema := toolDescriptor.InputSchema
	if len(inputSchema) == 0 {
		return nil, false
	}
	var decodedSchema map[string]any
	if json.Unmarshal(inputSchema, &decodedSchema) != nil {
		return nil, false
	}
	tool := &mcp.Tool{
		Name:        toolDescriptor.Name,
		Description: toolDescriptor.Description,
		InputSchema: decodedSchema,
		Annotations: toolAnnotations(toolDescriptor),
		Meta: mcp.Meta{
			"blueclaw/sideEffectClass":         toolDescriptor.SideEffectClass,
			"blueclaw/approvalScope":           toolDescriptor.ApprovalScope,
			"blueclaw/requiresApproval":        toolDescriptor.RequiresApproval,
			"blueclaw/requiresRequesterDevice": toolDescriptor.RequiresRequesterDevice,
		},
	}
	var decodedOutputSchema map[string]any
	if len(toolDescriptor.OutputSchema) > 0 && json.Unmarshal(toolDescriptor.OutputSchema, &decodedOutputSchema) == nil {
		tool.OutputSchema = decodedOutputSchema
	}
	return tool, true
}

func toolAnnotations(toolDescriptor toolcontract.ToolDescriptor) *mcp.ToolAnnotations {
	isReadOnly := leavesEnvironmentUnchanged(toolDescriptor.SideEffectClass)
	isDestructive := toolDescriptor.SideEffectClass == toolcontract.ToolSideEffectDestructive
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    isReadOnly,
		DestructiveHint: &isDestructive,
	}
}

func leavesEnvironmentUnchanged(sideEffectClass string) bool {
	switch sideEffectClass {
	case toolcontract.ToolSideEffectRead, toolcontract.ToolSideEffectNone, toolcontract.ToolSideEffectComputation:
		return true
	}
	return false
}

func invokeThroughToolSet(requesterToolSet RequesterToolSet, toolDescriptor toolcontract.ToolDescriptor, hasOutputSchema bool) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		invocationContext := toolcontract.WithTaskRunID(ctx, strings.TrimSpace(requesterToolSet.TaskRunID))
		toolResult, errorValue := requesterToolSet.ToolSet.Invoke(invocationContext, toolcontract.ToolInvocation{
			ToolName: toolDescriptor.Name,
			Input:    request.Params.Arguments,
		})
		if requesterToolSet.ObserveToolInvocation != nil {
			requesterToolSet.ObserveToolInvocation(toolDescriptor.Name, errorValue == nil && toolResult.Failure == nil)
		}
		if errorValue != nil {
			return nil, errorValue
		}
		return callToolResult(toolResult, hasOutputSchema, toolDescriptor.Name), nil
	}
}

func callToolResult(toolResult toolcontract.ToolResult, hasOutputSchema bool, toolName string) *mcp.CallToolResult {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: resultText(toolResult)}},
		IsError: toolResult.Failed(),
	}
	if !hasOutputSchema || toolResult.Failed() {
		return result
	}
	var structuredContent any
	if len(toolResult.Output.Data) == 0 || json.Unmarshal(toolResult.Output.Data, &structuredContent) != nil {
		return missingStructuredContentResult(toolName)
	}
	result.StructuredContent = structuredContent
	return result
}

func missingStructuredContentResult(toolName string) *mcp.CallToolResult {
	notice := toolName + " publishes an output schema but returned no structured result, so the runtime cannot hand you one that conforms to it. This is a defect in the tool, not in your call."
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: notice}}, IsError: true}
}

func resultText(toolResult toolcontract.ToolResult) string {
	if toolResult.Failure != nil {
		return fmt.Sprintf("%s: %s", toolResult.Failure.Code, toolResult.Failure.UserSafeSummary)
	}
	return toolResult.Output.Content
}
