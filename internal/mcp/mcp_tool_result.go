package mcp

import (
	"encoding/json"
	"errors"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type ToolResult struct {
	Content           []json.RawMessage `json:"content"`
	StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
	IsError           bool              `json:"isError"`
}

func parseToolArguments(input string) (map[string]any, error) {
	if strings.TrimSpace(input) == "" {
		return map[string]any{}, nil
	}
	var arguments map[string]any
	if errorValue := json.Unmarshal([]byte(input), &arguments); errorValue != nil {
		return nil, errors.New("mcp tool input must be a JSON object")
	}
	if arguments == nil {
		return nil, errors.New("mcp tool input must be a JSON object")
	}
	return arguments, nil
}

func normalizeToolResult(result *sdkmcp.CallToolResult) (string, error) {
	if result == nil {
		return "", errors.New("mcp tool returned no result")
	}
	content := make([]json.RawMessage, 0, len(result.Content))
	for _, item := range result.Content {
		encoded, errorValue := item.MarshalJSON()
		if errorValue != nil {
			return "", errors.New("mcp tool returned invalid content")
		}
		content = append(content, encoded)
	}
	normalized := ToolResult{Content: content, IsError: result.IsError}
	if result.StructuredContent != nil {
		encoded, errorValue := json.Marshal(result.StructuredContent)
		if errorValue != nil {
			return "", errors.New("mcp tool returned invalid structured content")
		}
		normalized.StructuredContent = encoded
	}
	encoded, errorValue := json.Marshal(normalized)
	if errorValue != nil {
		return "", errors.New("mcp tool result could not be normalized")
	}
	return string(encoded), nil
}

func ParseToolResult(output string) (ToolResult, error) {
	var document struct {
		Content           []json.RawMessage `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
		IsError           *bool             `json:"isError"`
	}
	if errorValue := json.Unmarshal([]byte(output), &document); errorValue != nil || document.IsError == nil {
		return ToolResult{}, errors.New("mcp tool returned invalid normalized result")
	}
	return ToolResult{Content: document.Content, StructuredContent: document.StructuredContent, IsError: *document.IsError}, nil
}
