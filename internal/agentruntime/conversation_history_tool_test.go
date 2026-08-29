package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type recordingConversationHistoryProvider struct {
	historyCursor  string
	limit          int
	visibleContext agentcontract.VisibleContext
}

func (provider *recordingConversationHistoryProvider) FetchHistory(_ context.Context, historyCursor string, limit int) (agentcontract.VisibleContext, error) {
	provider.historyCursor = historyCursor
	provider.limit = limit
	return provider.visibleContext, nil
}

func TestConversationHistoryUsesTrustedCursorAndCanonicalProjection(t *testing.T) {
	historyProvider := &recordingConversationHistoryProvider{
		visibleContext: agentcontract.VisibleContext{
			Messages: []agentcontract.VisibleContextMessage{{
				Speaker:            "Requester",
				SpeakerCallingName: "Lee",
				SpeakerHandle:      "lee",
				Text:               "check the previous file again",
				SentAt:             time.Date(2026, 7, 19, 9, 30, 0, 0, time.UTC),
				Materials: []agentcontract.VisibleContextMaterial{{
					MaterialID:        "internal-material-id",
					URL:               "https://mattermost.local/api/v4/files/file-9",
					Platform:          "mattermost",
					MessageID:         "internal-message-id",
					Filename:          "quarterly-report.docx",
					ContentType:       "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
					SizeBytes:         2048,
					Path:              "/workspace/private/quarterly-report.docx",
					IsAvailable:       true,
					MarkdownPreview:   "Quarterly report",
					ConversionStatus:  "complete",
					ConversionMessage: "Converted",
				}},
			}},
			HasMoreBefore: true,
			HistoryCursor: "next-cursor",
		},
	}
	toolSet := conversationHistoryToolSet(historyProvider, "trusted-cursor")

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.ConversationHistoryToolName,
		Input:    json.RawMessage(`{}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected conversation history success, got %+v", result)
	}
	if historyProvider.historyCursor != "trusted-cursor" || historyProvider.limit != 20 {
		t.Fatalf("expected trusted cursor and default limit, got cursor=%q limit=%d", historyProvider.historyCursor, historyProvider.limit)
	}
	if len(result.Effects) != 0 {
		t.Fatalf("expected read-only result without effects, got %+v", result.Effects)
	}

	var document map[string]any
	if errorValue := json.Unmarshal(result.Output.Data, &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	assertExactKeys(t, document, "messages", "hasMoreBefore", "historyCursor")
	messages := document["messages"].([]any)
	message := messages[0].(map[string]any)
	assertExactKeys(t, message, "speaker", "speakerCallingName", "speakerHandle", "text", "sentAt", "materials")
	materials := message["materials"].([]any)
	material := materials[0].(map[string]any)
	assertExactKeys(t, material, "url", "filename", "contentType", "sizeBytes", "isAvailable", "markdownPreview", "conversionStatus", "conversionMessage")
}

func TestConversationHistoryNormalizesNilArrays(t *testing.T) {
	historyProvider := &recordingConversationHistoryProvider{
		visibleContext: agentcontract.VisibleContext{HistoryCursor: "next-cursor"},
	}
	toolSet := conversationHistoryToolSet(historyProvider, "trusted-cursor")

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.ConversationHistoryToolName,
		Input:    json.RawMessage(`{}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected empty history success, got %+v", result)
	}
	var document struct {
		Messages []any `json:"messages"`
	}
	if errorValue := json.Unmarshal(result.Output.Data, &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	if document.Messages == nil || len(document.Messages) != 0 {
		t.Fatalf("expected normalized empty messages, got %#v", document.Messages)
	}
}

func TestConversationHistoryRejectsNonCanonicalInput(t *testing.T) {
	testCases := []json.RawMessage{
		json.RawMessage(`{"direction":"before"}`),
		json.RawMessage(`{"historyCursor":"   "}`),
		json.RawMessage(`{"limit":0}`),
		json.RawMessage(`{"limit":51}`),
		json.RawMessage(`{"limit":1.5}`),
	}
	for _, input := range testCases {
		t.Run(string(input), func(t *testing.T) {
			historyProvider := &recordingConversationHistoryProvider{}
			toolSet := conversationHistoryToolSet(historyProvider, "trusted-cursor")

			result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
				ToolName: toolcontract.ConversationHistoryToolName,
				Input:    input,
			})

			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !result.Failed() || result.FailureStage() != "tool_input_schema" {
				t.Fatalf("expected strict input rejection, got %+v", result)
			}
			if historyProvider.historyCursor != "" {
				t.Fatalf("expected rejected input not to reach history provider, got %q", historyProvider.historyCursor)
			}
		})
	}
}

func TestConversationHistoryAcceptsExplicitCursorAndLimit(t *testing.T) {
	historyProvider := &recordingConversationHistoryProvider{
		visibleContext: agentcontract.VisibleContext{HistoryCursor: "next-cursor"},
	}
	toolSet := conversationHistoryToolSet(historyProvider, "trusted-cursor")

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.ConversationHistoryToolName,
		Input:    json.RawMessage(`{"historyCursor":"explicit-cursor","limit":50}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected explicit pagination input success, got %+v", result)
	}
	if historyProvider.historyCursor != "explicit-cursor" || historyProvider.limit != 50 {
		t.Fatalf("expected explicit cursor and limit, got cursor=%q limit=%d", historyProvider.historyCursor, historyProvider.limit)
	}
}

func TestConversationHistoryResultContractRejectsMalformedOutput(t *testing.T) {
	handlerToolSet := toolcontract.NewToolSet(nil)
	handlerToolSet.RegisterTool(toolcontract.ToolDefinition{
		Name:        toolcontract.ConversationHistoryToolName,
		Description: "Fetch earlier visible messages.",
		InputSchema: conversationHistoryInputSchema,
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		document := json.RawMessage(`{"messages":null,"hasMoreBefore":false,"historyCursor":"cursor"}`)
		return toolcontract.ToolSuccessData(string(document), document), nil
	})
	toolSet := toolcontract.NewToolSet([]string{toolcontract.ConversationHistoryToolName})
	if errorValue := toolSet.RegisterProvider(context.Background(), kernelToolProvider{handlerToolSet: handlerToolSet}); errorValue != nil {
		t.Fatal(errorValue)
	}

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.ConversationHistoryToolName,
		Input:    json.RawMessage(`{}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "tool_result_contract" {
		t.Fatalf("expected malformed history result rejection, got %+v", result)
	}
}

func conversationHistoryToolSet(historyProvider HistoryProvider, historyCursor string) *toolcontract.ToolSet {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{toolcontract.ConversationHistoryToolName})
	return toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{
		ProfileName:     "default",
		HistoryCursor:   historyCursor,
		HistoryProvider: historyProvider,
	})
}

func assertExactKeys(t *testing.T, document map[string]any, expectedKeys ...string) {
	t.Helper()
	if len(document) != len(expectedKeys) {
		t.Fatalf("expected keys %v, got %v", expectedKeys, document)
	}
	for _, key := range expectedKeys {
		if _, isFound := document[key]; !isFound {
			t.Fatalf("expected key %q in %v", key, document)
		}
	}
}
