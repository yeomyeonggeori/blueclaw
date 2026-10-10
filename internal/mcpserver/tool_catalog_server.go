package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
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
	TurnContext       context.Context

	ObserveToolInvocation func(toolName string, toolResult toolcontract.ToolResult, isSucceeded bool)
}

func NewToolCatalogServer(requesterToolSet RequesterToolSet, version string) (*mcp.Server, error) {
	if strings.TrimSpace(requesterToolSet.RequesterPersonID) == "" {
		return nil, errors.New("tool catalog server refuses to serve a tool set with no requester")
	}
	if requesterToolSet.ToolSet == nil {
		return nil, errors.New("tool catalog server requires a tool set")
	}
	server := mcp.NewServer(&mcp.Implementation{Name: toolCatalogServerName, Version: version}, nil)
	publishedDescriptors := publishedToolDescriptors(requesterToolSet)
	publishedDescriptors = markedOfferedOnRequest(requesterToolSet, publishedDescriptors)
	requesterToolSet.ToolSet = toolSetAllowingEveryPublishedTool(requesterToolSet, publishedDescriptors)
	for _, toolDescriptor := range publishedDescriptors {
		tool, isServable := servableTool(markedHostGated(toolDescriptor, requesterToolSet.ToolSet))
		if !isServable {
			continue
		}
		server.AddTool(tool, invokeThroughToolSet(requesterToolSet, toolDescriptor, tool.OutputSchema != nil))
	}
	return server, nil
}

func publishedToolDescriptors(requesterToolSet RequesterToolSet) []toolcontract.ToolDescriptor {
	if requesterToolSet.ToolAudience == ToolAudienceBare {
		return callableAndSelectableToolDescriptors(requesterToolSet.ToolSet)
	}
	publishedDescriptors := []toolcontract.ToolDescriptor{}
	for _, toolDescriptor := range requesterToolSet.ToolSet.ListDescribedToolDefinitions() {
		if isPublishedToAudience(toolDescriptor, requesterToolSet.ToolAudience) {
			publishedDescriptors = append(publishedDescriptors, toolDescriptor)
		}
	}
	return publishedDescriptors
}

func markedOfferedOnRequest(requesterToolSet RequesterToolSet, publishedDescriptors []toolcontract.ToolDescriptor) []toolcontract.ToolDescriptor {
	if requesterToolSet.ToolAudience != ToolAudienceBare {
		return publishedDescriptors
	}
	marked := make([]toolcontract.ToolDescriptor, 0, len(publishedDescriptors))
	for _, toolDescriptor := range publishedDescriptors {
		toolDescriptor.IsOfferedOnRequest = !isCallableByTheLoop(requesterToolSet.ToolSet, toolDescriptor)
		marked = append(marked, toolDescriptor)
	}
	return marked
}

func callableAndSelectableToolDescriptors(toolSet *toolcontract.ToolSet) []toolcontract.ToolDescriptor {
	descriptors := []toolcontract.ToolDescriptor{}
	for _, toolDescriptor := range toolSet.ListRegisteredToolDefinitions() {
		if isCallableByTheLoop(toolSet, toolDescriptor) || toolSet.CanExpose(toolDescriptor.Name) {
			descriptors = append(descriptors, toolDescriptor)
		}
	}
	return descriptors
}

func isCallableByTheLoop(toolSet *toolcontract.ToolSet, toolDescriptor toolcontract.ToolDescriptor) bool {
	return toolSet.IsAllowed(toolDescriptor.Name) || toolSet.IsBuiltInTool(toolDescriptor.Name) || toolDescriptor.Visibility == toolcontract.ToolVisibilityInternal
}

func toolSetAllowingEveryPublishedTool(requesterToolSet RequesterToolSet, publishedDescriptors []toolcontract.ToolDescriptor) *toolcontract.ToolSet {
	if requesterToolSet.ToolAudience != ToolAudienceBare {
		return requesterToolSet.ToolSet
	}
	toolNames := make([]string, 0, len(publishedDescriptors))
	for _, toolDescriptor := range publishedDescriptors {
		toolNames = append(toolNames, toolDescriptor.Name)
	}
	return requesterToolSet.ToolSet.WithAllowedToolNames(toolNames)
}

func markedHostGated(toolDescriptor toolcontract.ToolDescriptor, toolSet *toolcontract.ToolSet) toolcontract.ToolDescriptor {
	toolDescriptor.IsHostGated = toolDescriptor.RequiresApproval && toolSet.HasToolCallGate()
	return toolDescriptor
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
		Meta:        toolcontract.DescriptorMeta(toolDescriptor),
	}
	var decodedOutputSchema map[string]any
	if len(toolDescriptor.OutputSchema) > 0 && json.Unmarshal(toolDescriptor.OutputSchema, &decodedOutputSchema) == nil {
		tool.OutputSchema = decodedOutputSchema
	}
	return tool, true
}

func toolAnnotations(toolDescriptor toolcontract.ToolDescriptor) *mcp.ToolAnnotations {
	isReadOnly := LeavesEnvironmentUnchanged(toolDescriptor.SideEffectClass)
	isDestructive := toolDescriptor.SideEffectClass == toolcontract.ToolSideEffectDestructive
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    isReadOnly,
		DestructiveHint: &isDestructive,
	}
}

func LeavesEnvironmentUnchanged(sideEffectClass string) bool {
	switch sideEffectClass {
	case toolcontract.ToolSideEffectRead, toolcontract.ToolSideEffectNone, toolcontract.ToolSideEffectComputation:
		return true
	}
	return false
}

func invokeThroughToolSet(requesterToolSet RequesterToolSet, toolDescriptor toolcontract.ToolDescriptor, hasOutputSchema bool) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		invocationContext := toolcontract.WithTaskRunID(contextCarryingValuesOf(requesterToolSet.TurnContext, ctx), strings.TrimSpace(requesterToolSet.TaskRunID))
		toolResult, errorValue := requesterToolSet.ToolSet.Invoke(invocationContext, toolcontract.ToolInvocation{
			ToolName: toolDescriptor.Name,
			Input:    request.Params.Arguments,
		})
		if requesterToolSet.ObserveToolInvocation != nil {
			requesterToolSet.ObserveToolInvocation(toolDescriptor.Name, toolResult, errorValue == nil && toolResult.Failure == nil)
		}
		if errorValue != nil {
			return nil, errorValue
		}
		return callToolResult(toolResult, hasOutputSchema, toolDescriptor.Name), nil
	}
}

func callToolResult(toolResult toolcontract.ToolResult, hasOutputSchema bool, toolName string) *mcp.CallToolResult {
	result := &mcp.CallToolResult{
		Content: append(append([]mcp.Content{&mcp.TextContent{Text: resultText(toolResult)}}, imageContents(toolResult)...), fileContents(toolResult)...),
		IsError: toolResult.Failed(),
		Meta:    toolcontract.ResultMeta(toolResult),
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

func imageContents(toolResult toolcontract.ToolResult) []mcp.Content {
	images := []mcp.Content{}
	for _, attachment := range toolResult.Attachments {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.ContentType)), "image/") {
			continue
		}
		data, errorValue := base64.StdEncoding.DecodeString(strings.TrimSpace(attachment.ContentBase64))
		if errorValue != nil || len(data) == 0 {
			continue
		}
		images = append(images, &mcp.ImageContent{Data: data, MIMEType: strings.TrimSpace(attachment.ContentType), Meta: toolcontract.AttachmentMeta(attachment)})
	}
	return images
}

func fileContents(toolResult toolcontract.ToolResult) []mcp.Content {
	files := []mcp.Content{}
	for _, attachment := range toolResult.Attachments {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.ContentType)), "image/") || strings.TrimSpace(attachment.DevicePath) == "" {
			continue
		}
		data, errorValue := base64.StdEncoding.DecodeString(strings.TrimSpace(attachment.ContentBase64))
		if errorValue != nil || len(data) == 0 {
			continue
		}
		files = append(files, &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
			URI:      "file://" + attachment.DevicePath,
			MIMEType: strings.TrimSpace(attachment.ContentType),
			Blob:     data,
			Meta:     toolcontract.AttachmentMeta(attachment),
		}})
	}
	return files
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
